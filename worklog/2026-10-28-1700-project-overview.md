# Project hierarchy overview

- Timestamp: 2026-10-28 17:00 UTC (2026-10-29 01:00 Singapore)
- Prompt: Implement the minimum usable Project → Subproject → Task/run overview, seed integ.life, update specs/worklog, test, and commit locally without deployment.
- Codex development: yes.

Implemented a first overview tab in the static Context workspace. The presentation model seeds `integ.life` with Knowledge visual, Knowledge graph, Lingulet, and Deployment & traffic. Each child includes status, recent activity, blocker, next step, and a task/run row. Other projects receive a generic Context workspace child. Added responsive styling, four locale translations, a Records link, and focused model tests.

Validation and commit are recorded in the final task response.

Follow-up: added per-subproject expand/collapse controls with stable project-child keys, localized labels, mobile-safe action layout, and a model test for key stability.

Follow-up: added status filtering for blocked, running, and queued work with visible subproject counts and a matching filter helper test.
