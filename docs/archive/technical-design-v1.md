# Event-driven Context Technical Design

English | [简体中文](technical-design-v1.cn.md)

> Historical design: the implementation baseline has moved to [product-V2.md](../product-V2.md). This document preserves the previous design and descriptions of the existing implementation; see [V2 Implementation and Review](../v2-implementation.md) for the V2 task breakdown and acceptance criteria. The legacy query_context, backend task coordinator, and inbox are not part of the V2 output architecture.

Version: 0.2 · Date: 2026-09-12 · Status: P1 implementation in progress. Sections 1 and 8 identify completed local capabilities; the remaining sections still include design proposals. See the [P1 Review Record](../p1-review.md) for actual acceptance results. This document does not indicate that the production deployment has been updated.

The [Product Design](../product.md) defines product constraints and the acceptance scope; see the [Initial Scope](../mvp.md) for the existing baseline. This document describes the complete flow, but schedules only the next implementation slice from Section 10 of the product document.

## 1. Existing Implementation and New Boundaries

The capabilities below were confirmed by reading the repository code for this work. They do not indicate a verified production runtime state.

| Area | Existing implementation | Increment required by the design |
|---|---|---|
| Identity and sharing | SQLite stores identities and projects; file-backed runner identities, installation authorization, and task constraints | Evaluate third-party plugin authorization later |
| Events | Immutable JSON and original files; platform provenance, explicit corrections/confirmations, and idempotent derived appends by automations | More source types |
| Files | Retain the 1 MiB limit for text/*; add bounded 20 MiB multipart audio uploads, authenticated original downloads, and idempotency | Image uploads later |
| Queries | Exact matching by project, time, and top-level metadata; append-sequence snapshot pagination; keyword context and relationship expansion | Validation against a broader set of real questions; evaluate semantic retrieval later |
| MCP | HTTP and stdio; existing tools plus query_context | Background execution uses a separate restricted interface |
| Frontend | Project pages, media, and context; runner registration, fixed-skill settings, run status, inbox, and sources | Real usage feedback |
| Execution | File-backed coordinator, runner CLI, event and daily triggers; real local ASR and Codex integration | Long-running schedules require a user-configured wake-up mechanism |
| Mobile capture | Retain the existing iOS prototype and its Core/build evidence; backend media and review contracts are reusable | Develop only Android now; accept the independent `app/android/` implementation for recording, durable sync, and result viewing |

Primary code locations:

- [types.go](../../backend/internal/core/types.go): data structures, Backend, and size limits.
- [filestore.go](../../backend/internal/core/filestore.go): event publication, idempotency, append sequence, file reads, and snapshot queries.
- [store.go](../../backend/internal/core/store.go): identity and project authorization, and input validation.
- [schema.sql](../../backend/internal/core/schema.sql): current SQLite tables.
- [HTTP server](../../backend/internal/api/server.go) and [MCP server](../../backend/internal/mcpserver/server.go): transports and scopes.
- [frontend/app.js](../../frontend/app.js): existing user flows.

Existing idempotency keys are isolated by project + author. The existing in-process mutex does not coordinate writes across multiple processes. The existing OAuth context:write scope covers project creation, member addition, and appends together, while static user tokens likewise lack plugin-level least privilege. Neither can be given directly to an arbitrary skill as a restricted execution credential.

## 2. Architecture and Responsibilities

~~~mermaid
flowchart TD
    Phone[Mobile app recording / media capture] --> Local[Local original files and upload queue]
    Local --> Input[Authenticated media upload / web / CLI / MCP]
    Input --> API[Go API and authorization boundary]
    API --> Events[Raw events and files]
    Chat[Conversational agent] --> Context[query_context]
    Context --> API
    Events --> Select[Candidate selection and relationship resolution]
    Select --> Context
    Cron[User-side cron or another scheduler] --> Runner[User-side Runner]
    Runner --> Coordinator[Backend rule and task coordinator]
    Coordinator --> Events
    Coordinator --> Config[Version-pinned Skill installations and rules]
    Coordinator --> Runs[File-backed Run records]
    Runner --> Agent[Verified Agent adapter executes Skill]
    Agent --> Restricted[Task-scoped restricted MCP / file reads]
    Restricted --> API
    Agent --> Candidate[Structured candidate result]
    Candidate --> Commit[Backend validation and idempotent commit]
    Commit --> Events
    Commit --> Inbox[In-product inbox]
    Commit --> Delivery[Separately authorized external delivery]
~~~

- Core is responsible for identity, raw records, relationship validation, candidate extraction, and task authorization. It does not call a model during a raw write request.
- The Coordinator lives in the same Go service and is responsible for creating and claiming tasks, state transitions, result submission, and delivery status. The initial version introduces neither a message queue nor a second business database.
- The Runner is a process in the user's environment, launched periodically by cron or another scheduler. It claims tasks, loads a version-pinned skill, invokes an agent, manages timeouts, and submits results.
- The Agent receives only the inputs and tools required for the current task. It does not receive runner registration credentials, the user's full-scope token, or external publishing credentials.
- In the initial version, query_context for an active conversation synchronously returns evidence, which the calling agent interprets and expresses in natural language. Retrieval does not start another background agent.
- A Skill may perform multistep reasoning or invoke authorized tools, but it cannot expand its project scope, create arbitrary shell tasks, or bypass delivery authorization.

The initial deployment model gives a single service process write access to the data directory. A runner does not mount the server data directory and uses only authenticated interfaces. The database remains limited to identity, projects, and execution authorization; it does not store event content.

## 3. Persistence Model

### 3.1 Storage Layout

Retain the existing paths and place new content under the same Git-ignored data root:

~~~text
data/
  projects/<project_id>/
    events/<event_id>.json
    files/<file_id>
    automation/
      installations/<installation_id>/revisions/<revision_id>.json
      rules/<rule_id>/revisions/<revision_id>.json
      control/<object_id>/<sequence>.json
      runs/<run_id>/request.json
      runs/<run_id>/transitions/<sequence>.json
      runs/<run_id>/attempts/<attempt_id>/candidate.json
      runs/<run_id>/commit.json
      deliveries/<delivery_id>/transitions/<sequence>.json
    cache/
      automation-state.json
      retrieval-index.json
  users/<user_id>/inbox/<entry_id>/transitions/<sequence>.json
  skills/<skill_digest>/...
~~~

- Events, files, configuration versions, candidate results, run transitions, and commit receipts are immutable.
- control contains appended records for enabling, pausing, selecting versions, and similar changes; current state is derived as a projection.
- cache contains replaceable acceleration files that can be deleted and rebuilt from source data. Cache loss must not cause a logical execution to be repeated.
- The inbox stores result references, recipients, and state such as read/unread; it does not copy the project's source text. Project authorization is rechecked whenever an entry is opened.
- Runner registrations, installation execution authorization, and token hashes are authorization data and belong in the existing identity SQLite database; skill packages and user prompts remain in file storage.

All mutations are serialized through one backend coordinator. Writes continue to use temporary files, content synchronization, and immutable publication; implementation must add directory synchronization and failure tests. The manifest is the visibility boundary: the existence of a file cannot be treated as proof that the complete event has been committed.

Backups must include both the identity database and all of data after the coordinator and new writes have stopped. During restoration, revalidate authorization, run transitions, and cache watermarks. Ordinary caches may be discarded; commit and delivery receipts must not be discarded and reconstructed as “never executed.”

### 3.2 Event Extensions

Retain the existing content.kind=text/file, free-form metadata, server-assigned author, and recorded_at. Add optional controlled extensions that remain readable when absent from older events:

~~~json
{
  "id": "evt_transcript_1",
  "project_id": "prj_example",
  "actor_user_id": "usr_installer",
  "content": {"kind": "text", "text": "Transcript content..."},
  "metadata": {"language": "zh", "tags": ["meeting"]},
  "provenance": {
    "origin": "derived",
    "source_id": "src_upload",
    "source_assurance": "authenticated",
    "run_id": "run_example",
    "installation_id": "ins_transcribe",
    "skill_digest": "sha256:<digest>",
    "config_revision": "rev_1",
    "output_slot": "transcript",
    "generation": 1
  },
  "relations": [
    {"type": "derived_from", "event_id": "evt_audio_1"}
  ],
  "interpretation": {
    "kind": "transcript",
    "assertion_status": "machine_generated"
  }
}
~~~

The example omits existing fields such as timestamps to highlight the additions. The server writes provenance based on an authenticated input path or a claimed run; a client cannot directly declare itself a trusted runner. source_assurance proves only that the entry point was bound, not that the content is true.

- Free-form metadata on raw records may continue to use keys such as source, but it remains a user claim. The rule UI clearly distinguishes it from a bound source_id.
- Mark old events as having legacy/unknown sources. Keys in old metadata that resemble system fields are not promoted to trusted fields.
- A runner acts on behalf of an authorized installer: actor_user_id retains the delegated user while the runner and run origins are recorded separately. User confirmation must come from an interactively authorized user path and cannot be self-confirmed by the same generation task.
- interpretation describes the nature of an output, such as transcript, summary, claim, suggestion, or confirmation; it is not a guarantee of accuracy.
- The derived_from, supersedes, retracts, confirms, and contradicts relations must point to accessible existing events in the same project. Cycles and cross-project references are prohibited. P1 supports whole-event relationships, not implicit replacement of partial text. contradicts declares a conflict but does not invalidate either side.
- An automated result may replace only its own older version within the same installation, processing stage, and input lineage. It cannot automatically retract a user's raw record.
- By default, a user correction may explicitly supersede only records written by that user or generated on that user's behalf. A differing opinion from another author remains a conflict; P1 does not implicitly grant additional adjudication authority in shared projects.

An interactive user's RecordInput may add validated relations for appending corrections, retractions, confirmations, or conflict declarations. The server assigns raw/confirmation semantics according to the authorization path. Ordinary record_event cannot accept run_id or trusted provenance. Automated submission permits only the relationship and result types specified by the task grant.

### 3.3 Generated Content and Effective State

“Most recently written” is not a universal truth rule. Effective versions are determined from explicit valid relationships and the generation within the same processing lineage; occurred_at remains a user-declared time, while recorded_at indicates only write order.

A rerun for the same input, installation, and output_slot creates a monotonically increasing generation. Only a successfully committed output with a higher generation becomes the default version; a failed new version does not make the old one disappear. Analyses from different installations remain independent and cannot automatically replace one another.

Retrieval deduplicates by raw-evidence lineage. A summary and its source transcript cannot “vote” as two independent sources. Suggestions are grouped under suggestions by default and reach confirmed state only after user confirmation. When a conflict cannot be resolved, both sources are retained. The initial version does not promise to discover every natural-language contradiction automatically.

## 4. Media Input and Sources

### 4.1 Raw Write Contract

The existing JSON/base64 text interface remains compatible; its global limit is not simply raised for every request. Add a dedicated bounded multipart media endpoint that submits a project, metadata, type, idempotency key, and one file together. After validation, it publishes one file event.

P1 supports the common formats actually verified on both the mobile recording side and processing side. Candidates include audio/mp4 (M4A/AAC), audio/wav, and audio/mpeg; enabled formats are returned through capability information. The app must not produce a format by default that the server rejects. Any required transcoding is an explicit processing step and preserves the original media. The implemented media endpoint enforces fixed limits of 20 MiB per file and 21 MiB per multipart request; it separately configures a 180-second read deadline and a 190-second response deadline. The recording UI shows the short-recording limit in advance, stops at the limit while reliably preserving recorded content, and never silently trims an oversized file.

On upload, validate the declared type, supported container signatures, and byte limit, then save the valid original file first. Decoding, duration, and transcription budgets are checked in an isolated processing stage. The initial transcription duration budget is 10 minutes; an over-limit file is preserved while processing returns limit_exceeded.

The planned image allowlist is JPEG/PNG, with independent byte and decoded-pixel limits; active-content formats are not yet supported. Parsers require bounded memory and timeouts and cannot treat a file extension as its actual type.

### 4.2 MCP File Paths

Retain the existing base64 behavior for small files. Large media uses the authenticated upload endpoint and CLI/web submission, avoiding large base64 payloads in conversational tool context. When an agent must upload media, an adapter that supports attachments or local files uses the same upload protocol and returns an event ID. Large-media MCP uploads are not promised for unverified clients.

The runner downloads authorized files under a task grant and streams them into a task-specific temporary directory. It does not accept server paths or arbitrary URLs found in logs. The media download endpoint authorizes every request and does not issue long-lived public links. When a task ends or times out, the runner cleans up its own temporary media and processes; candidate results needed for retries are preserved on the server.

### 4.3 Source Binding

Authorization for a source integration binds project_id, source_id, and the permitted write types; the server generates the trusted source_id. An ordinary user may declare metadata.source, but that does not satisfy an automated external action that requires an “authenticated source.”

A successful upload means the raw event has been committed. Whether a matching rule was found and whether the current analysis capability is supported are returned as separate states. A raw record must not be rolled back because of a processing configuration error.

### 4.4 Mobile Capture and Automatic Upload

The mobile app is the primary media-capture entry point; web file upload is supplementary. The client handles only capture, reliable sync, and result browsing. Media analysis still uses backend rules and a user-configured runner; a background agent is not required on the phone.

- Initial setup defaults to a private project named “My Records.” Ownership is shown before every recording. A shared project must be selected explicitly or prebound; model classification never changes authorization scope automatically. If no network is available initially and the app has not yet obtained a project ID, it stores an unbound local draft and uploads only after login and binding to the private project.
- At the start of recording, generate a stable capture_id. Media is written continuously to a local file. After recording ends, close the file and confirm that it is readable before durably storing a queue item. The queue contains the owning account, project, capture_id, file location, actual MIME type, size, summary, occurrence time, and fixed metadata.
- The server binds the input source to the registered client identity, such as phone.recorder. A source label in metadata is not a trusted source. The occurrence time comes from the device and is marked as declared; server-side recorded_at continues to indicate receipt time.
- The upload idempotency key uses the stable capture_id and retains the server's project + author isolation. A retry must reuse the same file and input. If the server committed the event but the response was lost, resubmission returns the original event. Local state becomes synced only after the response records the event_id and summary.
- Local states are recording → local_saved → queued/uploading → synced → processing/ready. waiting_network, waiting_auth, interrupted, and failed are visible branches. Processing state is queried from the server and cannot be marked complete merely because an HTTP request was sent.
- Network restoration, foreground entry, and platform-permitted background opportunities trigger pending uploads. The initial version may retry an entire short recording, using backoff and concurrency limits to control consumption; it does not promise resumable chunked uploads or unlimited background execution.
- If the app is terminated or interrupted by a call or audio-session event, reopening it should discover persisted segments and show “Recording interrupted; recoverable portion preserved.” Incomplete media must not be treated as a complete meeting. Lock-screen recording and background upload are verified separately on devices.
- An expired login pauses synchronization without deleting files. The queue is bound to its original account; switching accounts must not upload pending media under another account. When access to the target project is unavailable, retain the local file and show an actionable status. Store credentials in the client's secure credential facility, not in media or ordinary logs.
- Do not automatically delete the original file before the server confirms the commit. After confirmation, a locally playable copy remains; cache cleanup is configured separately later. “Saved on this phone” and “Saved on the server” must not be presented interchangeably.
- After the user ends a recording, upload proceeds without another confirmation form. Processing results may suggest a title and tags while the raw event remains append-only. Image capture, photo-library import, and file import can later reuse the same queue contract.

The current delivery platform is explicitly Android, implemented as an independent project in `app/android/`. Existing `app/EventDrivenContext/`, `app/Core/`, and the Xcode project remain, but their iOS build, test, and failure records are not Android acceptance evidence. First-time unauthenticated offline capture, trusted source registration, and long-running operating-system background uploads remain design goals.

Android uses a microphone foreground service that the user explicitly starts while the app is in the foreground, with a persistent notification and a stop action while recording. It does not automatically turn on the microphone after device restart or while in the background. Permissions and service types follow the [Android foreground service requirements](https://developer.android.com/about/versions/14/changes/fgs-types-required). [WorkManager](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work) resumes the persistent queue with network constraints and backoff retries. Scheduling remains subject to system conditions, so enqueuing work must not be described as a completed upload. A test server's CA may appear only in a debug build's [debug trust configuration](https://developer.android.com/privacy-and-security/security-config); release builds must not relax certificate or hostname validation.

## 5. Skill Packages, Installations, and Rules

### 5.1 Skill Package

Use a portable SKILL.md with optional resource files. The product adds a small manifest that expresses the runtime contract. This manifest is a proposal for this product and does not claim that every agent natively recognizes the same schema.

~~~yaml
id: audio-transcribe
version: 1.0.0
entrypoint: SKILL.md
capabilities:
  - audio_transcription
input_schema: schemas/input.json
output_schema: schemas/output.json
output_slots:
  - transcript
allowed_tools:
  - get_event
  - get_file
~~~

Installation pins a digest of the package contents, including all referenced resources. Reject paths or links outside the package; scripts and network access must be explicitly declared and supported by the adapter. P1 accepts only two reviewed skills bundled with the product or installed locally by the user and does not run automatic update scripts from remote packages.

A Skill describes a semantic workflow and contains no user account passwords, dynamic project tokens, or external delivery credentials. The user prompt may override permitted analysis configuration, but not the authorization policy, output schema, or system data boundaries.

The executor loads the user's configuration through its own interface as the task's system prompt or equivalent analysis instructions, keeping execution constraints, skill instructions, user configuration, and raw material in separate segments. Users may freely change analysis requirements; authorization is still enforced by the server and tool boundaries and must not depend on a model consistently obeying a prompt segment.

Outputs use a common envelope. The following is a review candidate result; a transcript uses the same envelope and adds optional millisecond-level segment locations and inaudible markers. A transcriber that has not passed capability verification cannot fabricate timestamps or speaker identities.

~~~json
{
  "schema_version": 1,
  "outcome": "output",
  "output_slot": "daily_review",
  "kind": "summary",
  "text": "The budget was finalized today; the delivery date still needs confirmation.",
  "source_event_ids": ["evt_budget_change", "evt_schedule_question"],
  "items": [
    {
      "kind": "decision",
      "text": "The budget was changed to 30,000.",
      "source_event_ids": ["evt_budget_change"]
    },
    {
      "kind": "open_question",
      "text": "The delivery date has not been confirmed.",
      "source_event_ids": ["evt_schedule_question"]
    }
  ]
}
~~~

Every evidence reference must appear among the authorized inputs actually provided to this task. The backend validates reference legality, but does not claim that JSON validation proves a conclusion correct. Candidate results cannot include publication destinations, executable commands, or new tool grants. A no_output envelope must include a structured reason and does not create a content event; its run record is still retained.

### 5.2 Installation Instances

An installation instance contains installation_id, owner_user_id, project_id, pinned skill_digest, runner_id, config_revision, read_scope, output_policy, and enabled state. An installer manages an instance with scope=personal; the project creator manages a shared instance with scope=project.

Default event processing reads only the matching input and its related files; a daily review installation may be granted historical reads within the same project. Authorization is checked during installation, claiming, reading, submission, and delivery. Configuration and prompts publish new versions, while existing runs pin their snapshots.

### 5.3 Rule Example

~~~yaml
id: rule_audio_notes
installation_id: ins_transcribe
revision: rev_1
stage: transcript
trigger:
  type: event.appended
  origin: raw
match:
  source_id: src_voice_notes
  media_types: [audio/wav, audio/mpeg]
  actor: installation_owner
  metadata_equals:
    purpose: project_note
priority: 100
activation:
  mode: future_only
configuration:
  user_prompt: Preserve the original wording, mark inaudible passages, and do not guess people's names.
outputs:
  append_to: same_project
  external_delivery: disabled
~~~

Conditions are combined with AND. Missing fields do not match, and invalid configurations such as empty arrays are rejected when saved. MIME types use an explicit allowlist. Metadata comparison retains the existing type-sensitive exact equality for top-level values. Trusted and declared sources are configured separately and never substituted implicitly.

The same input may trigger different installations or stages. When multiple rules match the same installation + stage, choose one by descending priority with rule_id lexicographic order as the tiebreaker. Show a conflict preview; never let iteration order decide.

When a new rule is activated, record the project sequence watermark and process only later events. A configuration change records a new effective watermark interval. When scheduling scans historical events, it selects the rule version for the event's interval and must not process an old pre-outage input with the latest prompt by mistake. A user-requested historical rerun is a separate request.

Only raw matches by default. A derived chain must explicitly identify the producing installation and output_slot and be validated for cycles, maximum depth, and task count. P1 has no general DAG executor; audio transcription has only one processing stage.

## 6. Runner and Scheduled Execution

### 6.1 Runner Protocol

The `edc-runner tick` command is implemented and executes at most one task per invocation. It can be launched periodically by the user's existing cron or another scheduler. `edc-runner watch --interval 30s --max-runtime 10m` provides bounded continuous checks and exits after ten minutes by default. Neither this document nor this implementation cycle installs permanent cron entries.

Each tick:

1. Acquires a local process lock to prevent overlapping execution by the same runner.
2. Uses its registration credentials to request backend dispatch; the backend evaluates only event watermarks and due times for the installations that runner is authorized to execute.
3. Claims one task and receives a lease, attempt_id, fencing_token, pinned configuration, and input allowlist; there is currently no separately issued grant bearer.
4. Validates the local skill package digest and capabilities, then starts the corresponding agent adapter.
5. Renews the lease with heartbeats, collects the structured result, saves the candidate, and requests a commit.
6. Prints the status of this invocation and exits; the next tick continues. watch manages the interval and total runtime limit.

Initial event processing is likewise discovered through tick scans, so write-to-processing latency depends on the polling interval. One minute may be a starting configuration, but real-time response is not promised. After a service restart or prolonged runner outage, scanning resumes from the durable watermark. Advance the watermark only after all matching tasks or explicit skip decisions have been persisted, preventing lost tasks.

The backend's sole coordinator determines whether work is due and claimable; cron only wakes the runner. Manual execution and multiple wake-up sources all follow the same idempotent path and cannot independently create duplicate tasks.

### 6.2 Adapter Boundary

An adapter receives pinned task JSON, a skill directory, and a restricted tool connection. It returns declared structured JSON and an exit status. It also handles timeouts, cancellation, child-process cleanup, and capability declarations. Invocation uses a fixed executable and argument array; metadata and prompts are never concatenated into a shell command.

Adapter capabilities distinguish at minimum noninteractive execution, structured output, restricted tool connections, image reading, and audio transcription. A text agent may invoke a separate transcription tool; the ability to understand text does not imply the ability to read audio.

Codex or another agent may be an adapter candidate, but this design does not depend on specific command-line arguments, login implementations, or subscription allowances. After selecting an adapter, validate it against that provider's current official contract. Do not copy desktop login state, scrape subscription credentials, or automatically substitute a metered API.

The P1 capability probe must run in a real account environment: noninteractive startup, reading one authorized event, processing a small audio file, producing valid output, failure exit, and timeout cleanup. The UI must not show “available” until the probe passes.

### 6.3 Time Semantics

Each P1 scheduled rule stores an IANA time zone and the user's selected HH:MM local time; arbitrary cron expressions are not accepted. The start time comes from the installation version, while the catch-up policy and run budget are fixed values. The server compares UTC timestamps, and the UI displays the user's time zone. Changing the time zone or time creates a new rule version with a future effective boundary.

By default, a daily review covers the half-open interval from the previous scheduled execution time to the current one—for example, 21:00 on the previous day through 21:00 on the current day. The range does not shift with actual startup delay. Select records in the window by recorded_at; there is currently no additional cross-window historical context retrieval. The first future slot also uses a complete window and may contain records from earlier on the installation day; this differs from event transcription, which processes only new inputs after a version is enabled. The default UI may suggest 21:00, but the actual time is user-configured and must not be replaced by the browser's current time zone.

A repeated local time during DST runs only once, choosing the first occurrence. A nonexistent local time shifts forward to the first valid time that day. Scheduler tests must prove these semantics rather than relying on undocumented defaults from a cron library.

By default, outage catch-up executes only the most recent missed review slot and explicitly records other slots as skipped_misfire. A manual request accepts only explicit source events and offers no historical-slot selection UI. If a future reminder plugin finds that a reminder has expired, it skips the reminder and records the reason by default instead of sending a burst after recovery.

A review pins the project snapshot and notes audio still awaiting processing. A transcript completed afterward may appear in the next review under “late processing results”; it does not rewrite or redeliver the previous review.

## 7. Tasks, Retries, and Result Submission

### 7.1 Logical Task Identity

- Event task key: project + installation + stage + input_event + rule_revision + generation.
- Time task key: installation + rule_revision + local calendar slot identifier; also store the resolved UTC due_at.
- Manual request key: owner + installation + user-supplied request_id.
- A Run request pins input IDs, the project snapshot watermark, skill digest, configuration version, authorized installation, and output budget.

The first automatic processing uses generation=1. A failed retry remains a new attempt of the same run; only a user-initiated reanalysis allocates a new generation. Configuration changes do not automatically create historical tasks.

### 7.2 State Machine

~~~text
queued -> leased -> running -> candidate_saved -> committing -> succeeded
                    |                 |
                    +-> retry_wait ---+-> queued (for permitted failure types)
                    +-> failed
                    +-> blocked_auth / blocked_capability
any nonterminal state -> cancelled (when paused, uninstalled, or explicitly canceled)
queued -> skipped (misfire, disabled rule, or explicit no-run decision)
~~~

The backend validates the prior state and appends every transition; current state is a projection. Each attempt uses a monotonically increasing fencing token. Heartbeats, candidates, and submission requests from an expired attempt are all rejected. After a lease expires, the run can be claimed again, and a late result from the old process must not be accepted.

After validating a no_output envelope, running may also transition directly to succeeded. Following candidate_saved, network or disk retries preferentially resume submission of the pinned candidate without rerunning the model. Only a retryable computation failure for which no candidate exists invokes the agent again.

Current values: a 120-second lease, a default 10-second CLI heartbeat, a 10-minute server-side execution deadline per attempt, and at most 3 attempts. Temporary failures back off for one minute and then two minutes; jitter has not yet been added. Candidate results are limited to 64 KiB. Daily input is limited to 128 records and 1 MiB of text in total. When exceeded, the omitted count and coverage range are recorded rather than relying only on the prompt.

Retryable cases include temporary network errors, rate limits, and temporary executor unavailability. Expired authentication enters blocked_auth. Unsupported media or capabilities enter blocked_capability or an explicit failure. Invalid output permits at most one controlled repair attempt, counted against the total budget.

Revoking authorization terminates unfinished tasks. Repairing a login may resume still-valid blocked tasks. Resuming an installation creates future tasks by default; canceled historical tasks require an explicit user rerun.

### 7.3 Commit and Crash Recovery

A transcription run publishes one transcript event. A daily review combines progress, decision, and open_question items into a summary and publishes each suggestion item as a separate suggestion event; the complete candidate is retained in the personal inbox. This prevents default context from mixing unaccepted suggestions into the factual summary. Each output slot has a fixed identity. The run is marked successful and the inbox entry created only after all slots complete; this does not claim atomic transactions across files. A no_output result may end successfully without content.

1. Under a valid attempt, the backend validates the output schema, size, references, permitted output types, and installation state.
2. It first writes candidate.json immutably and records a content digest. Submission retries reuse that candidate and do not invoke the model to generate a different result.
3. The backend uses a fixed run_id + output_slot as the idempotency key and publishes a derived event through the core record path. It retains the existing “same key, different input” conflict semantics and never silently overwrites an inconsistent result.
4. It saves commit.json with the event_id and output digest, then appends succeeded.
5. It generates an inbox or delivery task from deterministic run + output_slot + recipient values.

If a crash occurs after event publication but before the commit is saved, recovery looks up the original event using the same key and candidate, then completes the commit. If the candidate differs from the published content, the run enters a conflict state for review; it is not regenerated or overwritten. When the disk is full, state watermarks do not advance.

This provides at-least-once scheduling with idempotent logical-result submission. It does not promise end-to-end exactly-once behavior for external model calls or arbitrary third-party delivery. If power is lost after a result is computed but before it is saved, a retry may incur a second computation charge.

### 7.4 Pause and Authorization Races

Pause and submission are serialized by the same coordinator: an already successful commit remains in history, while a pause that occurs first rejects a new submission. Pausing revokes the task grant, and the runner should interrupt execution at its next heartbeat. Data already read by an agent cannot be “taken back,” so the input must be minimized before claim time.

Terminating a running local process may take time, but the backend must immediately reject subsequent read, submit, and delivery authorization. P1 has no external sending. Future external sending uses the authorization and uncertain-outcome handling in Section 10.

## 8. On-Demand Context Extraction

### 8.1 Interface Contract

The read-only HTTP `POST /v1/context/query` and MCP `query_context` are implemented. P1 accepts exactly one explicit project_id per request and does not search all projects by default or automatically generate a project-wide summary.

~~~json
{
  "project_id": "prj_example",
  "query": "What should we prioritize this week?",
  "max_output_bytes": 24000,
  "include_suggestions": true
}
~~~

Example of the current response structure:

~~~json
{
  "project_id": "prj_example",
  "snapshot": {"sequence": 128},
  "evidence": [
    {
      "event_id": "evt_budget_change",
      "excerpt": "The budget was changed to 30,000, superseding the previous 50,000.",
      "excerpt_truncated": false,
      "source_event_ids": ["evt_budget_change"],
      "interpretation": "user_correction",
      "state": "active_evidence",
      "retrieval_reason": "relation_context",
      "recorded_at": "2026-09-12T00:00:00Z"
    }
  ],
  "suggestions": [],
  "conflicts": [],
  "coverage": {
    "snapshot_sequence": 128,
    "indexed_through_sequence": 128,
    "pending_processing_count": 1,
    "truncated": false,
    "conflict_detection": "explicit_update_forks_only"
  },
  "warnings": ["keyword_retrieval", "explicit_relations_only", "pending_audio"]
}
~~~

The byte budget applies to the complete serialized response, including the trailing HTTP newline. The default and maximum are 24,000 bytes and the minimum is 512 bytes; it is not an exact token promise. query must be valid UTF-8 and 1–4,096 bytes. The current context request supports only project_id, query, max_output_bytes, and include_suggestions; intent and time_range are not implemented. warnings uses stable codes, which the UI localizes.

Context is a read-only return value and does not automatically append the query or result. A caller that needs an audit record must explicitly record one, preventing retrieval itself from creating event noise and privacy copies.

### 8.2 P1 Extraction Algorithm

1. Check current membership and the task grant, then pin the project's maximum append sequence.
2. Enumerate accessible events and supported text files within that snapshot. Use committed transcripts as searchable text for audio; unprocessed media returns only an explanatory status.
3. Select candidates across the entire project snapshot using text-term matches. Chinese uses simple character fragments and normalized matching; Latin text uses normalized terms. Current context requests do not accept time/metadata filters; use query_events when those filters are required. Recency affects ranking, not validity.
4. Expand each candidate with its raw sources and valid supersession, retraction, and confirmation relationships. Relationship resolution covers the entire snapshot, preventing a match on an old record from omitting an update whose keywords differ.
5. Deduplicate by lineage and retain the newest successfully processed valid version. Group suggestions separately and preserve both sides of explicit conflicts.
6. Within the output budget, extract UTF-8 excerpts around keywords and attach event_id, time, source, and excerpt_truncated. Full content is read by event ID. If the budget is insufficient, omit a whole entry or explicitly mark an excerpt as truncated; never cut JSON or fabricate a complete quotation.
7. Return retrieval coverage, pending-processing count, truncation, and known capability boundaries. If there are no candidates, say so explicitly so the caller can rephrase the question or read more source material.

P1 does not depend on a vector database or a second backend model call, nor does it claim complete semantic recall. First compare correct citations, outdated conclusions, and omissions against an acceptance question set. If keyword recall is insufficient, then evaluate query rewriting, reranking, or a rebuildable semantic index.

conflicts currently encodes only multiple valid, unsuperseded update branches targeting the same event within the query's relationship closure. Its kind is explicit_update_fork, and it includes target_event_id and update_event_ids. The contradicts relation and semantic-conflict classification are not implemented. The calling agent evaluates contradictions in ordinary text from the returned evidence. An empty conflicts array must not be described as “proof that no conflicts exist.” Acceptance covers both explicit relationships and the caller's real behavior with unstructured contradictions.

### 8.3 Cache and Degradation

The initial implementation may scan files and build an in-process text index. A disk cache records the project sequence, index version, and content digest. The index accelerates candidate selection only; final references and authorization use the source manifest.

When the cache lags, scan newly appended events or fall back to a complete snapshot scan. If that cannot finish within the configured time budget, return partial with the actual watermark rather than labeling stale results complete. The proposed default time budget is initially 5 seconds, with explicit degradation after exhaustion; every value must be measured against real data.

Even after returning a context package, subsequently appended events appear only in a new call. The caller can compare snapshots for freshness. A precomputed summary is optional material, not the only memory that replaces raw records.

### 8.4 Review Reuse

The daily review skill uses the same query_context and raw-read tools to assemble inputs from a date window and limited historical context. It uses an explicit task budget to read additional pages and does not recursively trigger the same review installation.

Skill output must distinguish progress, decisions, open_questions, and suggestions and attach sources. Events or transcripts that arrive after the input snapshot is pinned do not enter the same run. P1 acceptance cannot rely solely on “a summary was generated”; it must verify raw references and suggestion types.

## 9. APIs, Identity, and Authorization

### 9.1 Current P1 Interfaces

The following interfaces are wired into the local code. The service must start with an explicit `-skill-root <absolute path to backend/skills>` to enable automation; see the P1 review record for the complete validation boundary. The public interactive MCP adds only query_context and does not expose runner control interfaces.

| Interface or tool | Purpose | Authorization |
|---|---|---|
| POST /v1/media-events | Bounded atomic media upload and record creation | Project append |
| GET /v1/files/{id}/content | Authenticated original download for users | Project read |
| POST /v1/context/query; query_context | Synchronously return context evidence | Project read |
| GET/POST /v1/automation/runners | List or register runners; a new token is returned only once | Current user |
| POST /v1/automation/runners/{id}/actions | Revoke a runner | Runner owner |
| GET/POST /v1/automation/installations | List or install a pinned skill and bind a runner | Installation management |
| POST /v1/automation/installations/{id}/revisions | Publish a prompt or configuration version | Installation management |
| POST /v1/automation/installations/{id}/actions | Pause or resume | Installation management |
| POST /v1/automation/runs | Request a test run or explicit rerun | Installation management |
| GET /v1/automation/runs; GET /v1/automation/runs/{id} | Run state and references | Installation visibility and project read |
| POST /v1/runner/dispatch | Generate due tasks and claim one | Runner registration authorization |
| POST /v1/runner/runs/{id}/heartbeat | Renew a lease | Current runner and attempt |
| GET /v1/runner/runs/{id}/input-files/{file_id}/content | Download audio in the pinned task allowlist | Current attempt grant |
| POST /v1/runner/runs/{id}/submit | Validate, save, and idempotently submit a candidate, or no_output | Current attempt grant |
| POST /v1/runner/runs/{id}/fail | Report failure or blockage | Current attempt grant |
| GET /v1/inbox; POST /v1/inbox/{id}/read | View reviews and mark them read | Recipient and current project read |

In P1, the trigger belongs directly to the installation revision; there is no separate rules interface. List responses use the runners, installations, runs, and entries array fields, respectively. When no task exists, dispatch returns HTTP 204; otherwise, it directly returns a Claim. Other runner requests use a separate bearer token and also provide X-EDC-Attempt-ID and X-EDC-Fencing-Token. An ordinary user token cannot be used as a runner token.

The client management surface primarily uses the web UI and HTTP API. A P1 model subprocess receives only authorized pinned inputs and no MCP, backend, or runner tokens; the runner submits its candidate through the dedicated interface. Interactive MCP continues to use the user's own read identity.

Errors retain the existing error.code/message structure and add stable codes such as capability_unavailable, blocked_auth, lease_expired, budget_exceeded, and invalid_output. Large output is not included in error messages.

### 9.2 Three Identities

1. User identity: retains the existing interactive login and project authorization and manages the user's own installations or shared installations created by that user.
2. Runner registration identity: authorized to claim tasks for specific installations; it cannot add project members and does not automatically gain access to all project history.
3. Task scope: validates the runner registration token in combination with `run_id`, `X-EDC-Attempt-ID`, and `X-EDC-Fencing-Token`, constraining the lease, current installation, project membership, and input allowlist. It is not another independent bearer, and the model subprocess holds none of these credentials.

Effective authorization is the intersection of current user membership, installation authorization, and task scope. Only a hash of a registration token is stored; a user token cannot substitute for runner identity. After a pause, revocation, lease expiration, or invalidation of project authorization, task downloads, heartbeats, and submissions are all rejected. Successfully committed results remain, and a retry after a lost response for the same valid candidate returns the original result.

The P1 model reads only pinned inputs. The runner's media interface exposes only the files listed for the current task; it does not expose a general MCP tool that could expand project-history access to the model. Interactive users access projects under the ordinary read contract; annotations such as MCP readOnlyHint do not replace authorization.

Installation-management authorization and project-content read access remain separate. Under the existing project contract, other members may read published results but cannot view another person's private installation configuration, raw agent logs, or task controls. The inbox is only a personal entry point and does not alter the visibility of results in the project.

## 10. Outputs, Notifications, and External Actions

P1 output_policy permits only append_event, inbox, or no_output. The backend submits outputs; the agent has no direct external-send capability.

The complete product adds draft_share and publish. A delivery record pins output_event_id, content digest, destination, authorization version, and delivery key separately from the computation run. Credentials are stored by a dedicated delivery component or a user-designated controlled endpoint and never enter the skill prompt.

- Draft is the default. Automatic publishing requires an enabled content type, destination, and scope to match.
- Revalidate authorization and record a started state before sending. If a pause precedes the start of sending, it must block the send.
- Once sending has started, retraction cannot be guaranteed. The pause page clearly indicates requests that may still be completing externally.
- Providers that support idempotency receive a fixed delivery_id. If a timeout occurs and a provider lacks idempotency so the outcome is unknown, transition to unknown and query a receipt or ask the user to verify rather than blindly resending.
- The product inbox uses a fixed entry ID so repeated submissions do not create duplicate entries.
- A reminder fingerprint is computed from the installation, root evidence/task, reminder type, and time window. Read does not mean complete. Remind-later changes a future delivery time; user-confirmed completion appends a new fact.
- Publishing a result externally is a separate effect and is not recorded as sent merely because generation succeeded.

## 11. Runtime Constraints and Observability

Logs retain only event/run/installation IDs, durations, status codes, sizes, and available usage statistics. Body text, media, prompts, and credentials do not enter ordinary logs. Authorized run details show necessary input and output references; the output schema neither requests nor stores a model's internal chain of thought.

The runner executes in a dedicated task workspace and restricts file and network tools according to adapter capabilities. An adapter that cannot provide the required isolation cannot run untrusted third-party skills. P1 verifies only one controlled skill environment and does not claim to automatically sandbox arbitrary agents.

The queue view includes at least backlog size, age of the oldest waiting item, last successful heartbeat, blocked_auth, failure reason, and pause state. Resource budgets must be enforced in code: timeouts, task counts, file sizes, and output sizes all have actual constraints. When provider cost cannot be measured accurately, the UI does not show a fictitious remaining allowance.

When disk space is insufficient, reject input or task state that cannot be saved reliably and do not acknowledge success prematurely. If data validation detects corruption, stop the affected task and report it rather than silently skipping content and creating omissions in a review. The initial version does not support multiple backend processes writing directly to the same data directory and requires an exclusive writer lock at startup.

## 12. Compatibility, Rollout, and Validation

### 12.1 Compatibility Strategy

- Preserve old events and metadata exactly. New fields are optional; historical manifests are not rewritten.
- Preserve old HTTP/MCP text input and existing query ordering. Media receives a separate size limit and transport endpoint.
- Use dedicated write paths for controlled provenance and relationships; ordinary record_event does not accept fabricated run origins.
- Caches are rebuildable. Configuration and run history cannot rely on Git tracking; the identity SQLite database does not add tables containing events, transcripts, or vectors.
- Startup recovery checks pending commits before scheduling. Before rolling back software, confirm that it can read new fields. Back up runtime data first; do not delete new events to make an old version “compatible.”

### 12.2 P1 Implementation Order

| Order | Minimum change | Corresponding proof |
|---|---|---|
| 0 | Select one mobile platform and implement a recording, local queue, and account/project-bound automatic upload probe | Start/stop recording on a real device, preserve offline, resume upload after restart, and retry after a lost response without duplicates |
| 1 | Select one real executor and complete noninteractive transcription and restricted-tool probes | Actually process media and exit after a timeout |
| 2 | Extend core event relationships, media intake, and authenticated download while preserving the old text interface | Raw media persists, hashes match, and failures do not lose the source |
| 3 | Add installation/rule files, authorization credentials, the task coordinator, and runner tick | Rule versions, event triggers, idempotent retries, and effective pause/resume |
| 4 | Provide the transcription skill, query_context, and MCP registration | A new conversation cites the transcript and latest valid update |
| 5 | Provide the review skill, time-zone scheduling, and in-product inbox | A real scheduler triggers it; results have sources and do not duplicate |
| 6 | Add capture and review entry points to the mobile app and the necessary configuration, state, result, and pause entry points to the existing web UI | Complete mobile-capture-to-review flow; desktop and narrow-screen web configuration are usable |

Suggested modules: reuse record/authorization logic in backend/internal/core; extraction may live in backend/internal/contextquery, and rules and state may live in backend/internal/automation; the runner entry point is backend/cmd/edc-runner; the two packages live under the repository's skills/. Split according to actual code volume rather than first building a plugin framework or general-purpose engine.

### 12.3 Risks That Must Be Validated

| Risk | Meaningful validation |
|---|---|
| Partial persistence failure | Inject failures at raw file publication, candidate save, event commit, and commit save; after restart, verify that no logical result is lost or duplicated |
| Overlapping or expired execution | Claim the same run multiple times; after lease expiry, reject submission by the old attempt and allow only the valid candidate to commit |
| Pause and authorization bypass | Concurrent reads and submissions around a pause; verify that HTTP, MCP, media, and inbox all enforce scope |
| Configuration changes and outages | Old events use the correct effective interval, prompt changes do not rerun history automatically, and a manual rerun creates a new version |
| Time behavior | Time-zone changes, missing/repeated DST times, outage catch-up, and deduplication across multiple ticks |
| Source and feedback contamination | Metadata cannot fabricate a system source; suggestions cannot promote themselves to user-confirmed; multiple summaries from one source do not increase the independent evidence count |
| Retrieval errors | Explicit budget changes, unrelated conflicts, untranscribed media, empty results, output budgets, and insufficient cache watermarks |
| Trigger loops | Derived output does not trigger itself by default, and invalid explicit cycles are rejected |
| Media input | Size limits, incorrect MIME types, corrupt audio, parser timeouts, and round-tripping original bytes |
| Mobile capture and sync | Microphone denial, audio interruption, offline recording, local queue recovery, login expiry and account switching, and idempotent retry after a lost response |
| Product effect | New conversations with a real agent and reviews from a real scheduler, validating sources, current conclusions, and user feedback |

During implementation, run the existing make check and make build along with tests for the new boundaries above. This document-only change does not describe those future tests as passing.

### 12.4 Later Capabilities

Image analysis can reuse the media, source-rule, and result contracts. Reminders and recommendations can reuse scheduled triggers and feedback. External sharing integrates through a separate delivery component. Stronger retrieval, cross-project aggregation, parallel runners, a third-party marketplace, and hosted execution all require rescoping after P1 evidence exists.

## 13. Decision Record

| Decision | Choice | Condition for reconsideration |
|---|---|---|
| Context generation location | Deterministic backend extraction in the initial version + interpretation by the calling agent | Evaluate an additional model step if real recall and answer evidence are insufficient |
| Persistence | Files are the source of truth for content; SQLite stores identity and authorization only | Expand database responsibilities only after separately confirming the storage direction |
| Scheduler | User-side cron or another mechanism wakes the runner; the backend handles deduplication and due-time decisions | Add managed wake-ups after demand for hosted execution is confirmed |
| Skill compatibility | Pinned package + product-specific manifest + one adapter probe | Expand after verifying a second real agent |
| Sharing | Separate action, draft by default | Enable after the user configures specific automatic publishing authorization |
| Project boundary | One project per run; output returns to the same project | Design a private derived space or cross-project capability separately |
| Next-cycle scope | Mobile recording and automatic upload, audio transcription, on-demand extraction, and daily review | Expand only after acceptance and evaluation of user value |

Environment parameters that must be decided before implementation are concentrated in the executor, supported media formats, runtime host, and resource budgets. They must not be presented as completed integration capabilities.
