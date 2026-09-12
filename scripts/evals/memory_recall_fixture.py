#!/usr/bin/env python3
"""Fictional read-only HTTP fixture for testing memory-recall with the real CLI.

Run: python3 scripts/evals/memory_recall_fixture.py --root /tmp/recall --edc /path/to/edc
It creates isolated self/agent clients, prints their paths, and serves until stopped.
This is a test double for export/get/query, not an implementation of the backend.
"""

import argparse
import hashlib
import json
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

PROJECT = "prj_recall_fixture"


def event_id(n):
    return f"019f0000-0000-7000-8000-{n:012d}"


def event(n, text, *, refs=(), day="05", metadata=None):
    return dict(id=event_id(n), project_id=PROJECT, type="note",
                content={"kind": "text", "text": text}, metadata=metadata or {},
                source={"channel": "cli"}, refs=list(refs), sequence=n,
                occurred_at=f"2026-09-{day}T09:00:00Z",
                recorded_at=f"2026-09-{day}T09:00:00Z",
                actor={"type": "user", "id": "usr_fixture", "username": "fictional"})


def dataset():
    events = [
        event(1, "Decision: the Lumen pilot launches on 20 September 2026.", day="01"),
        event(2, "Mira Tan (MT) owns the Lumen pilot. Mira Teo owns the unrelated Lantern newsletter.", day="02"),
        event(3, "Lumen: accessibility testing and security sign-off are both pending. Owen Shah owns security sign-off.", day="03"),
        event(4, "Proposal only: expand Lumen to 500 accounts. No approval or budget decision yet.", day="04"),
    ]
    events += [event(n, f"Office garden observation {n}: plant bed {n} watered; no product decisions.")
               for n in range(5, 25)]
    events += [
        event(25, "Correction to the earlier launch decision: Move the Lumen pilot to 27 September 2026. Mira Tan remains the owner.",
              refs=[{"rel": "corrects", "id": event_id(1)}], day="11"),
        event(26, "Lumen accessibility testing passed. Accessibility is no longer a launch blocker.",
              refs=[{"rel": "updates", "id": event_id(3)}], day="11"),
        event(27, "Lumen security sign-off remains pending, owned by Owen Shah. No revised completion date has been agreed.",
              refs=[{"rel": "updates", "id": event_id(3)}], day="12"),
    ]
    files = {}

    def note(path, ident, title, summary, body):
        files[path] = f"---\nid: note_{ident}\ntitle: {title}\n---\n# {title}\n> Summary: {summary}\n\n{body}\n"

    def cite(n):
        return f"[Event](edc-event://{event_id(n)})"

    note("index.md", "root", "Fictional project", "Notes last summarized on 10 September 2026; later Events may exist.",
         "- daily/: dated records\n- persons/: people and aliases\n- topics/: work and life knowledge\n- goals/: delivery priorities")
    note("goals/index.md", "goals", "Goals", "Lumen pilot delivery.", "- [[note_lumen|Lumen launch decisions]]")
    note("goals/release-room/index.md", "lumen", "Lumen launch decisions", "Pilot planned for 20 September; two checks remain.",
         f"Launch: 20 September 2026. {cite(1)}\nOwner: [[note_mt|MT profile]]. {cite(2)}\n"
         f"Accessibility and security sign-off pending. Owen Shah owns security. {cite(3)}\n"
         f"500-account expansion is a proposal, not approved. {cite(4)}")
    note("goals/priorities.md", "priorities", "Priorities", "Pilot readiness first.", "- [[note_lumen|Lumen launch decisions]]")
    note("persons/index.md", "persons", "People", "Two distinct people named Mira.", "- [[note_mt|MT profile]]\n- [[note_teo|Mira Teo]]")
    note("persons/p-17/index.md", "mt", "Mira Tan", "MT owns Lumen.", f"Alias: MT. Owns the Lumen pilot. {cite(2)}")
    note("persons/p-18/index.md", "teo", "Mira Teo", "Lantern newsletter owner.", f"Owns Lantern, not Lumen. {cite(2)}")
    note("daily/index.md", "daily", "Daily", "September records.", "- 2026/09/10.md: last summarized status")
    note("daily/2026/09/10.md", "day10", "10 September", "Notes summarize existing Lumen decisions.", "See [[note_lumen|Lumen launch decisions]].")
    note("topics/index.md", "topics", "Topics", "Work and life subjects.", "- work/: product delivery\n- life/: garden records")
    note("topics/work/index.md", "work", "Work", "Pilot delivery.", "- [[note_lumen|Lumen launch decisions]]")
    note("topics/life/index.md", "life", "Life", "Garden reference notes.", "- garden/: plant beds")
    note("topics/life/garden/index.md", "garden", "Garden", "40 independent plant-bed reference notes.",
         "\n".join(f"- bed-{n:02}.md: bed {n}" for n in range(40)))
    for n in range(40):
        note(f"topics/life/garden/bed-{n:02}.md", f"bed{n}", f"Plant bed {n}", "Garden observations only.",
             (f"Bed {n} uses a regular watering schedule and weekly leaf checks. " * 25) + cite(5))
    return events, files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--edc", type=Path, required=True)
    args = parser.parse_args()
    root = args.root.resolve()
    root.mkdir(parents=True, exist_ok=True)
    events, files = dataset()
    exported = [{"path": p, "content": c, "sha256": hashlib.sha256(c.encode()).hexdigest()}
                for p, c in sorted(files.items())]
    log = root / "requests.jsonl"

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def respond(self, status, value, body=None):
            data = json.dumps(value).encode()
            with log.open("a") as out:
                out.write(json.dumps(dict(client=self.headers.get("Authorization", "").removeprefix("Bearer "),
                                          method=self.command, path=self.path, query=body, status=status,
                                          response_bytes=len(data))) + "\n")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def handle_read(self, body=None):
            if self.headers.get("Authorization") not in ("Bearer fixture-self", "Bearer fixture-agent"):
                return self.respond(401, {"error": {"code": "unauthorized", "message": "fixture credentials required"}})
            path = urlparse(self.path).path
            prefix = f"/v1/projects/{PROJECT}"
            if self.command == "GET" and path == prefix + "/notes/export":
                return self.respond(200, {"project_id": PROJECT, "revision": 7, "files": exported})
            if self.command == "GET" and path.startswith(prefix + "/events/"):
                found = next((e for e in events if e["id"] == path.rsplit("/", 1)[1]), None)
                if found:
                    return self.respond(200, found)
            if self.command == "POST" and path == prefix + "/events/query":
                allowed = {"types", "metadata", "source", "refs_to", "after_sequence", "from", "to", "time_field", "limit", "cursor"}
                if set(body) - allowed:
                    return self.respond(400, {"error": {"message": "unsupported fixture filter"}}, body)
                selected = events
                for key in ("metadata", "source"):
                    selected = [e for e in selected if all(e[key].get(k) == v for k, v in body.get(key, {}).items())]
                if body.get("types"):
                    selected = [e for e in selected if e["type"] in body["types"]]
                if body.get("refs_to"):
                    selected = [e for e in selected if any(r["id"] == body["refs_to"] for r in e["refs"])]
                selected = [e for e in selected if e["sequence"] > body.get("after_sequence", 0)]
                field = body.get("time_field") or "recorded_at"
                for key, compare in (("from", lambda x, y: x >= y), ("to", lambda x, y: x < y)):
                    if body.get(key):
                        selected = [e for e in selected if compare(e[field], body[key])]
                offset = int(body.get("cursor") or 0)
                size = min(100, max(1, body.get("limit") or 50))
                page = selected[offset:offset + size]
                result = {"events": page, "latest_sequence": len(events)}
                if offset + size < len(selected):
                    result["next_cursor"] = str(offset + size)
                return self.respond(200, result, body)
            return self.respond(404, {"error": {"code": "not_found", "message": "fixture route unavailable"}}, body)

        def do_GET(self):
            self.handle_read()

        def do_POST(self):
            self.handle_read(json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0")))))

        def reject_write(self):
            self.respond(405, {"error": {"code": "read_only", "message": "fixture is read-only"}})

        do_PUT = reject_write
        do_PATCH = reject_write
        do_DELETE = reject_write

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    origin = f"http://127.0.0.1:{server.server_port}"
    skill = Path(__file__).resolve().parents[2] / "frontend/skills/memory-recall/SKILL.md"
    for client in ("self", "agent"):
        folder = root / client
        folder.mkdir(exist_ok=True)
        notes = folder / "notes"
        (notes / "goals").mkdir(parents=True, exist_ok=True)
        (notes / "goals/local-draft.md").write_text("# Unpublished personal draft\nLumen launches 30 September, owned by Mira Teo. All checks passed.\n")
        config = folder / "config.json"
        config.write_text(json.dumps({"server": origin, "token": f"fixture-{client}"}))
        config.chmod(0o600)
        wrapper = folder / "edc"
        wrapper.write_text("#!/usr/bin/env python3\nimport os,sys\n"
                           "for k in ('EDC_SERVER','EDC_CONFIG','EDC_TOKEN'): os.environ.pop(k,None)\n"
                           f"os.execv({str(args.edc.resolve())!r}, [{str(args.edc.resolve())!r}, '--server', {origin!r}, '--config', {str(config)!r}] + sys.argv[1:])\n")
        wrapper.chmod(0o700)
        (folder / "SKILL.md").write_bytes(skill.read_bytes())
    (root / "expected.json").write_text(json.dumps({
        "launch": "27 September 2026", "owner": "Mira Tan", "open_blocker": "security sign-off",
        "blocker_owner": "Owen Shah", "resolved": "accessibility", "expansion": "proposal only",
        "decisive_events": [event_id(n) for n in (25, 26, 27)],
        "notes": len(files), "events": len(events), "published_note_bytes": sum(len(v.encode()) for v in files.values()),
    }, indent=2))
    print(json.dumps({"origin": origin, "root": str(root), "project": PROJECT, "notes": len(files), "events": len(events)}), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
