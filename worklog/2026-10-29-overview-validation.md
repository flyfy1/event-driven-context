# Project overview validation — 2026-10-29

## Scope

Added a small contract suite around the project overview hierarchy and its status filter. The checks cover the seeded operational subprojects, parent-preserving filtering, the labelled workspace controls, and filter translations across all supported locales.

Updated the bilingual project-overview specification to make operational status explicit: deployment or traffic signal, latest commit when available, a human-readable blocker/failure reason, and an explicit unknown state when telemetry is missing.

## Verification

```text
node --test frontend/project-overview.test.js frontend/project-overview-contract.test.js
```

The browser-facing behavior remains intentionally static and deterministic; this change does not claim production health without a checked signal.
