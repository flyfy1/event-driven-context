# V2 storage scaling

Internal engineering proposal. Part A is implemented; the target design and
migration below are not implemented or validated on production hardware.

## Current storage and the bounded improvement

`backend/internal/v2/storage.go` persists version 1 of `data/v2/index.json`.
Each successful event in a batch is persisted separately; a later failure does
not undo earlier successful items. The service holds its mutex while validating,
serializing, syncing a temporary file, renaming it, and syncing the directory.

Part A writes compact JSON and replaces the JSON round-trip clone of the entire
snapshot with a typed deep copy of the touched project plus shallow copies of
the project and token maps. Other projects remain read-only and shared. File
cleanup spans projects, so it copies project structs and file maps only. Memory
publication still follows successful persistence. Existing indented JSON opens
normally, and the schema/version are unchanged; no migration is needed here.

This reduces constants, not asymptotic write cost. Copying a large active project,
scanning event IDs/references, marshaling all projects, rewriting the index, and
holding a global mutex remain. Temporary memory still scales with stored data.
The existing atomic writer also has an ambiguous failure window: a directory
sync failure after rename can leave the new disk snapshot installed while the
caller receives an error and does not publish memory. Part A preserves that
behavior; it does not claim transactional rollback after the rename point.

## Proposed target: per-project transaction log segments

Use one exclusively owned data directory, with independent append-only logs per
project. A transaction frame contains a format version, project sequence/commit
number, operation type, payload length, payload, and checksum. Impose a bounded
frame size and rotate at approximately 64 MiB. A complete frame is the atomic
recovery unit, including compound operations such as event plus file-reference
status, installation plus token hash, or manual request plus mailbox state.
Preserve the current per-event batch commit semantics.

Keep events, original content, metadata, actor information, timestamps, IDs,
sequences, and provenance relationships immutable. Append state versions,
configuration revisions, status changes and file lifecycle records. Mutable
views (latest state, active installations, referenced files) are projections of
these records. A new derived output points to its source events and processor
identity rather than replacing them. Uploaded but unreferenced orphan files may
still follow the existing cleanup policy; referenced originals are retained.

The recording path is:

1. Acquire the project's writer lock and validate against its current indexes.
2. Resolve deduplication, references and permissions with keyed lookups; assign
   the next sequence without scanning historical events.
3. Encode and append the bounded transaction, then sync the log before success.
4. Publish index changes and release the lock. Readers use committed boundaries.

Keep ID-to-offset, sequence-to-offset, file-ID, latest-state and installation
indexes in memory initially. Hash lookups make expected validation cost O(1)
per ID/reference; payload encoding and append cost O(B), for B new bytes. Reads
load immutable records by segment offset. Index memory grows with record count,
but writing does not copy the index or any historical payload. A query can still
cost O(N) for arbitrary metadata predicates: deterministic queries do not imply
constant-time queries. Add time/type/reference indexes where measured workloads
justify them, and retain sequence-bounded cursor pagination and its signing key.

Use per-project serialization rather than a service mutex held across disk I/O.
A short directory/authentication lock protects project discovery and token
routing only. Project-local installation revision/status is authoritative and
must be rechecked under the project lock before a plugin operation commits.
Tokens and their installation changes are one project transaction; the global
token lookup is a rebuildable projection, not a second persistence transaction.
Independent projects can progress concurrently, although disk bandwidth and
sync latency remain shared constraints. Group commit can later amortize syncs,
but each caller must wait for its own durable commit boundary.

SQLite is a credible alternative: append-only event/state/revision tables,
transactional file and token indexes, and indexed deterministic queries would
reduce custom recovery code. Indexed inserts are O(log N), with bounded page
writes rather than full-dataset rewrites; a single database still serializes
writers, and checkpoints require care. Prefer the log design if strict bounded
foreground append work and per-project isolation justify its implementation
cost. Prototype both against the same recovery and Raspberry Pi workload before
committing to this target; Part A introduces neither backend nor dependencies.

## Index snapshots and recovery

Write versioned index checkpoints for sealed segments in the background, with
checksums and a manifest containing the last included commit and segment offset.
A checkpoint contains offsets and projections, not copies of original payloads.
Freeze an index generation briefly or capture a committed boundary and replay
sealed segments outside the writer lock; do not clone all indexes in the write
path. Sync checkpoint files, atomically publish their manifest, and sync its
parent directory before dropping superseded checkpoints. Checkpoint work is
O(N) if rebuilding a full index, so prefer immutable per-segment index files
plus a small checkpoint for the active tail. Throttle background I/O and measure
its effect on foreground latency; the total storage system is not cost-free.

On startup, verify the manifest and segment checksums, load valid checkpoints,
and replay only the uncheckpointed tail. Invalid/missing indexes can be rebuilt
from the authoritative logs. Retain history segments; checkpointing never deletes
source events or provenance. Periodic projection snapshots include latest state,
plugin/token status and sequence watermarks; their size can grow with the number
of distinct keys, so checkpoint them asynchronously as well.

A crash during append can leave an incomplete final frame. Ignore/truncate only
that uncommitted tail before accepting writes. Corruption in a previously sealed
segment is a hard error requiring recovery from backup, not silent truncation.
After an append or sync error, stop writes for that project and recover/replay
before proceeding: an uncertain write may already be durable. Client retries
use the original event/request ID and recover deduplication results. A complete
unacknowledged frame may survive a crash, but an acknowledged frame must survive
restart. Sync newly created segments and directory entries before acknowledging
records in them. Segment rotation and manifest replacement need explicit
crash-injection tests at every write/sync/rename boundary.

Blob creation remains immutable: write, verify hash/length, sync, publish, then
append the referencing transaction. A crash may leave an orphan blob, never an
acknowledged reference to an unpublished blob. Delete eligible unreferenced
blobs only after their lifecycle transaction is durable. Preserve the existing
single-process writer lock even with internal per-project concurrency.

## One-way production migration

This is an offline maintenance operation, not a compatibility layer or dual-write
period. Determine the actual production data directory and owning service before
execution; do not infer them from a developer checkout.

1. Stop writers/processors, drain requests, and acquire the exclusive data lock.
   Check disk space and record the old executable/configuration and schema.
2. Make a verified backup of `index.json`, all referenced/orphan blob files and
   the associated identity database using its supported consistent backup method.
   Record file hashes, sizes and project/record counts; verify the backup can be
   opened in an isolated copy. Retain it outside the conversion destination.
3. Parse the old snapshot strictly. Validate unique project/event IDs, sequence
   order and watermarks, file hashes, references, all state/config versions,
   installations, token hashes and manual requests. Preserve the cursor signing
   key and original values exactly; do not synthesize provenance. Report and
   stop on inconsistent data instead of silently repairing or discarding it.
4. Convert into a separate sibling directory. Import historical records with
   their original IDs, timestamps and sequences, then import projections and
   revision histories without claiming an original chronology that the snapshot
   did not store. Build indexes/checkpoints and sync all output and directories.
5. Reopen using only the new reader. Compare canonical logical exports, counts,
   watermarks, token mappings, every blob hash and every state/config version.
   Compare deterministic query results, pagination and reference resolution;
   exercise dedupe/retry and permissions in an isolated writable verification
   copy. Persist a conversion report and input/output hashes.
6. With writers still stopped, atomically select the new directory on the same
   filesystem and sync the parent. Start the new binary, verify recording,
   reading, plugin permissions and restart recovery through the actual service.
   Enable processors only after acceptance; keep the old directory and backup.

Before new writes are admitted, a failed cutover can select the old binary/data.
After new writes, do not restart the old binary against its stale snapshot: freeze
writes and restore/replay into the new format or implement a separately verified
export. The old binary must never open new-format storage. Remove the converter
from normal startup after rollout; retain backup/report per operator policy.

## Rough costs and acceptance

These are planning estimates, not Raspberry Pi measurements or promises:

| Workload | Snapshot approach | Proposed segmented log |
| --- | --- | --- |
| One ~400-byte text event | Rewrite O(total bytes); copy O(active project) | Approximately 0.8–1.5 KiB framed append plus one sync |
| 10,000 such events | Several MiB rewritten per write | Approximately 8–15 MiB retained log, excluding indexes/files |
| 1,000,000 such events | Order of 1 GiB rewritten per write | Approximately 0.8–1.5 GiB log; roughly 100–300 MiB ID/offset index memory, to measure in Go |
| Grow from zero to 10,000 events | Approximately 40–75 GiB cumulative JSON writes at 0.8–1.5 KiB/event | Approximately 8–15 MiB log appends, plus index/checkpoint writes |
| Migration | Read O(old bytes + blob bytes) for verification | Write O(new bytes); budget old + backup + new data + temporary checkpoints |

Sync latency sets a floor. A 2–20 ms sync budget would cap a single synchronous
writer near 50–500 commits/second before other work; this is a sensitivity range,
not measured SD-card performance. Filesystem/device write amplification and
background checkpoints add physical writes. Large state values, many references
or installations increase per-operation work; history size alone should not.

Allow roughly 2–4 engineer-weeks for log framing/recovery, indexed reads and
project locking, plus 1–2 weeks for migration, fault injection and Pi soak tests.
Reassess against a SQLite prototype before selecting an implementation.

The Part A benchmark is opt-in:

```sh
go -C backend test ./internal/v2 -run '^$' -bench '^BenchmarkRecordEventAtScale$' -benchtime=20x -count=3
```

It preloads 1,000 or 10,000 events in one project, persists the seed outside the
timer, then measures one validated durable event write at that fixed size.
Identity/HTTP overhead and seed construction are excluded; each write still
includes mutex acquisition, persistence, file sync and directory sync. It reports
ns/op and allocations. On an Apple M5 Pro (darwin/arm64), three-run medians were
14.84 → 8.34 ms/write at 1,000 events and 75.99 → 20.34 ms/write at 10,000 events.
These results demonstrate a constant-factor improvement, not bounded write cost.

Target acceptance should extend this benchmark to 100k/1m events, multiple
projects, real files/state/plugin workloads, and concurrent readers/writers.
Measure p50/p95/p99 latency, resident memory, restart time, disk bytes written,
checkpoint interference, and injected crash recovery on the production-class Pi.
No cloud model participates in recording, reading, migration or deterministic
queries. Deployers retain control of data location and access permissions; using
an external model for optional processing still sends that selected content
outside the local deployment.
