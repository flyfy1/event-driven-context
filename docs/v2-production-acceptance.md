# V2 production acceptance

This runbook verifies Product V2 step 1 through the public HTTP API and MCP endpoint. It writes only to projects owned by the dedicated acceptance account. It does not deploy, remove production data, or use another user's project.

## Current baseline

Checked on 2026-09-12:

- `https://context-api.integ.life/healthz` returned HTTP 200 with `{"status":"ok"}`. This proves the public route and origin are healthy, but the missing `api: "v2"` marker means this was the pre-V2 release.
- `https://context.integ.life/` returned HTTP 200 from GitHub Pages.
- A real isolated browser session signed in as `edc_acceptance_20260912_a621ee93` and showed its only project, `V2 Acceptance 20260912 a621ee93` (`prj_zyb3rqghg6a77drgrcym6nax3p`). No event was written during this pre-V2 baseline.
- Credentials are stored only at `/tmp/edc-v2-acceptance-20260912.ZITYhb/credentials.json`, mode `0600`. Do not print, commit, or copy its password and tokens into evidence.

The complete script was also run against an isolated real `edc-server` process built from the pending V2 source. Run `production-20260912T000023Z-ee7b62` passed every HTTP/MCP/State/plugin/file assertion. A second isolated fixture then passed twice with the same identity, plugin, and State (`production-20260912T000132Z-8b014d` and `production-20260912T000133Z-6cbf6a`), proving reuse across runs. The temporary server processes and data directories were removed afterward. This is local contract evidence, not production evidence.

## Production V2 result

Backend release `20260912T000040Z-809d20b` was accepted through `https://context-api.integ.life` on 2026-09-12. The public health response identified `api: "v2"` and the same dedicated account completed two consecutive runs:

- `production-20260912T000216Z-78707b`
- `production-20260912T000217Z-58960f`
- `production-20260912T000342Z-c6840c` (also isolates and checks the raw `structuredContent` number)

Both runs passed every assertion listed below. They reused main project `prj_zyb3rqghg6a77drgrcym6nax3p`, isolation project `prj_v6dpdcx7kuzjbiiqa3ygi6p5al`, the `acceptance-brief` installation, and its State history. The credentials file remained mode `0600`.

The frontend had not yet been switched to the V2 workspace at this checkpoint. Its earlier isolated-browser login is only a pre-V2 identity and project-list baseline; it is not counted as V2 event or State UI acceptance.

### V2 frontend checkpoint

After the V2 frontend release, an isolated 390 px Chinese browser session reused the dedicated acceptance account and verified the normal workspace flow:

- A new text record was created as event `01a09302-b5a8-7e3f-b216-354a67b7637e`, sequence 21, and appeared immediately at the top of the record list.
- The State view moved between `acceptance-brief/current` versions 6, 5, and back to 6.
- Version 5 opened its source event at sequence 7 in the page.
- The plugin control paused and resumed `acceptance-brief`; the final observed status was active.

This browser checkpoint covers text recording, State history/source navigation, and plugin pause/resume in the V2 frontend. It does not replace the processor-host proof for generating a new project brief.

### Generated brief and next-conversation reuse

A real Codex processor run published `project-brief/current` version 2 with `based_on_sequence=21`. The generated brief contains the three MVP decisions recorded through the fixed-release CLI:

- recording or text should be organized automatically and reused by the next conversation;
- continue Android development while preserving the existing iOS code;
- complete the core MVP loop before security hardening.

It also incorporates the browser-created Chinese decision at sequence 21. The State has six verified refs: the four `kind=decision` notes at sequences 10, 11, 12, and 21, plus two earlier Claude hook user-message logs used to explain the synthetic-scope conflict. Every ref resolves to the same project at or before `based_on_sequence`; the three CLI decisions retain `source.channel=cli`, and the browser decision retains the frontend's declared `source.channel=api`.

The public HTTP API, fixed-release CLI, and public MCP `get_state` tool independently returned identical version 2 content and refs. At the independent read checkpoint the State had `lag=4`, reflecting events appended after sequence 21. Non-secret responses and all resolved sources are under `.codex-artifacts/v2-production-acceptance/project-brief-v2-20260912T003157Z/`.

The V2 frontend displayed this generated version and opened the sequence-21 Chinese decision from its source link. A subsequent real Claude Code 2.1.266 `SessionStart` automatically injected `project-brief/current version=2 based_on_sequence=21 lag=1`, including the Android/iOS and MVP-priority decisions, then persisted session lifecycle logs at sequences 22–24 with an empty outbox. Claude Code was not logged in, so this proves automatic context injection and durable hook capture, but not a full model response using that context.

After the production transcription below, a second real processor run published `project-brief/current` version 3 with `based_on_sequence=26` and eight valid refs. It added the 30,000 budget, mobile-launch priority, unconfirmed delivery date, and explicit source coverage for both the audio note and derived transcript. HTTP, the fixed-release CLI, and MCP returned identical version 3 content and refs; a later independent read showed the expected `lag=3` after the final hook events. Evidence is under `.codex-artifacts/v2-production-acceptance/project-brief-v3-20260912/`.

The V2 frontend displayed version 3 with the audio-derived knowledge. A final real Claude Code `SessionStart` injected version 3 with `based_on_sequence=26` and `lag=1`, including the budget, mobile priority, unconfirmed date, audio event ID, and transcript event ID. It persisted lifecycle events at sequences 27–29 and drained its outbox to zero. This final repeat still proves injection rather than a model answer because the isolated Claude CLI remained logged out.

### Production audio transcription

A production `audio/mp4` note (`01a09306-84c3-7bd3-843e-4ee0a0d64d4b`, sequence 25) was processed by the real audio transcription adapter. It wrote derived event `0b99035f-aad7-5b43-ba30-1bcf65baf1c4`, sequence 26, with text:

> 预算调整为三万元，优先上线移动端，交付时间仍待确认。

The derived event is authenticated as plugin `audio-transcribe`, has `source.channel=plugin`, and contains exactly one `derived_from` ref to the sequence-25 file note. The V2 frontend's plugin-output filter displayed the transcript, expanded the ref to the original note, and exposed the `fixture.m4a` download action. Non-secret API responses are under `.codex-artifacts/v2-production-acceptance/asr-production-20260912-seq25-26/`.

The first adapter attempt used a temporary audio path without a file extension and failed before advancing its cursor. After preserving the media extension, the same processing path succeeded. This validates the corrected file-to-adapter handoff and shows that a failed attempt does not skip its source event.

### Fixed-release CLI path

CLI commit `809d20bd7ad6058617c45763defcda613a2e4dd6`, built separately as `/tmp/edc-v2-cli-809d20b`, completed run `cli-production-20260912T000757Z-043e09` against the same production project. It created three `note` events with `kind=decision`, `topic=mvp-demo`, and the run tag:

1. Android recording and text flow into automatic organization, then the next conversation uses the resulting context.
2. Continue Android development only and preserve the existing iOS code.
3. Complete the MVP flow first; defer security hardening until after the core loop works.

CLI `push`, filtered `query`, and all three `get` calls returned the same event IDs at sequences 10–12. CLI `state list` and `state get` read `acceptance-brief/current` version 6 with `based_on_sequence=9` and `lag=3`, correctly showing that the three new decisions were not yet incorporated. The CLI's stdio MCP server then completed a real SDK `initialize → query_events → Close` session and returned the same three IDs.

Reviewable non-secret output is in `.codex-artifacts/v2-production-acceptance/cli-production-20260912T000757Z-043e09/summary.json`, with the individual push, query, get, State, and MCP responses beside it. This proves the backend and CLI path for the acceptance material; it does not represent days of natural usage or acceptance of OAuth, another user's project, the frontend V2 workspace, or Android.

## Repeatable public acceptance

Run after the coordinated production release:

```sh
EDC_ACCEPTANCE_CREDENTIALS=/tmp/edc-v2-acceptance-20260912.ZITYhb/credentials.json \
  scripts/acceptance/v2-production.sh
```

The script stops without writing when `/healthz` does not identify V2. On V2 it reuses the dedicated identity, creates a second account-owned isolation project if needed, and tags every event and State with a unique `production-<timestamp>-<random>` run value.

One run verifies:

- HTTP event creation followed by MCP UUID duplicate detection and a partially successful batch;
- exact preservation and filtering of JSON integer `9007199254740993` in HTTP and raw MCP responses;
- plugin MCP State publication, HTTP version and lag reads, stale-version rejection, and a successful next version;
- HTTP pause and resume immediately disabling and restoring the plugin MCP token;
- MCP upload followed by byte-exact HTTP download, with the same file ID rejected under the isolation project.

The only durable production data created is acceptance-account projects, events, files, State versions, and the `acceptance-brief` plugin installation. A failed run attempts to resume the plugin before exiting. The script never removes existing data.

The official Go MCP SDK currently decodes `structuredContent` into `any`, which turns JSON numbers into `float64`; remarshal can display `9007199254740993` as `9007199254740992`. The server's raw JSON-RPC `structuredContent`, MCP text JSON, HTTP response, and stored `json.RawMessage` remain exact. Acceptance therefore checks the raw MCP response bytes and does not change the number into a string.
