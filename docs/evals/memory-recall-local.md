# Memory Recall local retrieval trial

Date: 12 September 2026.

The first fictional-data trial passed for answer correctness and source attribution, both in a manual retrieval and in a fresh subagent using the distributed skill. The subagent retrieved more Events than necessary. This is one behavioral smoke test, not evidence of benchmark accuracy or consistently bounded context at scale.

## Setup

- Built the actual repository `edc` CLI.
- Used a loopback-only Python HTTP fixture implementing notes export, Event reads, and filtered/paginated Event queries. It is a test double, not the production backend.
- Generated 53 published Markdown notes (72,928 content bytes) and 27 fictional Events for `prj_recall_fixture`.
- Added one unpublished local note that contradicted the real fixture evidence. Sync preserved it and excluded it from the tracked publication manifest.
- Used separate local cache/config directories for the manual and subagent clients. No production credentials, connection, or data were used.
- Gave the subagent only the question, copied skill, configured notes folder, and authenticated fixture CLI path. It did not inherit the parent conversation or expected answer. Isolation from the fixture source and answer key was by task instruction, not a filesystem access boundary.

Question:

> In project prj_recall_fixture, what is the current Lumen pilot launch date, who owns it, what still blocks launch and who owns that work? Has the 500-account expansion been approved? Cite your sources.

The notes summarize an older decision. Updates beyond the first 20-Event page move the launch, resolve one blocker, and confirm the remaining blocker. Forty unrelated garden notes test whether the agent avoids reading the whole tree. Similar person names and an ID-based link with a different title and folder name test routing.

## Results

| Check | Expected evidence | Manual | Subagent |
| --- | --- | --- | --- |
| Current launch date | 27 September 2026; Event 25 corrects Event 1 | Pass | Pass |
| Pilot owner | Mira Tan, not Mira Teo | Pass | Pass |
| Remaining blocker and owner | Security sign-off; Owen Shah; Event 27 | Pass | Pass |
| Resolved blocker | Accessibility passed; Event 26 | Pass | Pass |
| Expansion status | 500 accounts is a proposal, not approved; Event 4 | Pass | Pass |
| Stale notes | Later Events take precedence over the September 20 note | Pass | Pass |
| Unpublished local draft | Preserved but excluded as published evidence | Pass | Pass |
| Remote operations | Only export, get, or query; no write attempts in fixture request log | Pass | Pass |

The subagent explicitly cited Events 25, 26, 27, and 4, and stated that it checked all 27 Events through September 12. Its final page had no continuation cursor. It did not claim that the pending security review had a completion date.

Manual retrieval read four Markdown notes totaling 1,102 bytes, searched for a person note by stable ID, queried Events recorded since the notes' last-summary date, opened the expansion proposal, and checked references to that proposal. This was an informed walkthrough by the fixture author, not a blind accuracy trial.

The subagent reported reading the manifest, root index, goals index/priorities/release-room note, work index, September 10 daily note, and persons index. It queried the first 20 Events and followed cursor `20` for the remaining seven. It did not dump the garden notes.

| Retrieval metric | Manual | Subagent |
| --- | --- | --- |
| HTTP calls, including sync | 4 | 3 |
| Event API response bytes | 2,103 | 12,225 |
| Event pagination | Used targeted filters | Followed two pages correctly |

Both clients downloaded the complete export into local files; sync returned only its small result to the agent. HTTP response byte counts above are service payload measurements, not model token counts. Local filesystem read behavior for the subagent is based on its reported tool usage; the HTTP log does not instrument file reads.

The [recorded fixture requests](memory-recall-local-requests.jsonl) preserve the HTTP evidence. The `fixture-self` and `fixture-agent` labels are fictional credentials used only by this local test.

## Interpretation and next trial

The simple skill was sufficient for this question. No skill change was made after this trial. The efficiency result suggests the next test should increase irrelevant Event history substantially and check whether agents use known dates or source references before scanning all history. Only then decide whether the skill needs a more explicit freshness-query hint.

This run did not test MCP authentication, a live backend, note generation, absent notes, multilingual retrieval, concurrent updates, malicious note content, or repeated model runs. Local sandbox restrictions required escalation for loopback serving and CLI access; these were environment restrictions, not retrieval failures.

## Reproduce

From the repository root, build the CLI and start a fresh fixture:

```sh
GOCACHE=/tmp/edc-recall-go-cache go -C backend build -o /tmp/edc-memory-recall-test ./cmd/edc
recall_fixture_dir=$(mktemp -d /tmp/edc-recall-trial.XXXXXX)
python3 scripts/evals/memory_recall_fixture.py \
  --root "$recall_fixture_dir" --edc /tmp/edc-memory-recall-test
```

Keep this process running during the trial. It prints the root directory and local server address. Each client directory (`self/` and `agent/`) contains a copied `SKILL.md`, an `edc` wrapper using only fictional credentials, and a `notes/` folder. Use each wrapper from its client directory. It removes inherited EDC connection environment variables before invoking the real CLI.

Give a fresh agent the question above plus its client directory, skill path, CLI wrapper path, and notes folder. Tell it to inspect only its client folder and fixture responses, and not to inspect the fixture implementation, other client, or expected answer. Do not supply this results document to that agent. Ask for an answer with citations and a short retrieval log.

Compare the answer to `expected.json` at the fixture root and inspect `requests.jsonl` for pagination, filters, response sizes, and unexpected routes. The service rejects unsupported routes, but the request log is still necessary to detect attempted mutations. Stop the fixture server with Ctrl-C when done. No automatic teardown deletes the local evidence.

Fixture implementation: [memory_recall_fixture.py](../../scripts/evals/memory_recall_fixture.py).
