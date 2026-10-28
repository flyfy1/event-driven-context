# Project Overview hierarchy

## Scope

The workspace overview presents a small, reviewable hierarchy:

```text
Project → Subproject → Task / run
```

The first seeded example is `integ.life`, with four subprojects: Knowledge visual, Knowledge graph, Lingulet, and Deployment & traffic. Each subproject exposes a status, recent activity, blocker, next step, and at least one task or run.

This first slice is a UI projection of the project workspace. It does not write or mutate event data. Existing Records, Files, State, Connect, and Plugins views remain the source of truth for project data.

## Acceptance

- Overview is the first project view after sign-in.
- The `integ.life` example shows all four named subprojects and task/run statuses.
- A non-`integ.life` project still gets a useful Context workspace child.
- The layout remains readable at narrow mobile widths and all status information is text-accessible.
- The overview can open the existing Records view for follow-up work.
- Each subproject can be collapsed or expanded; the control exposes its state to assistive technology.
- The overview filter can show all, blocked, running, or queued subprojects and updates the visible count.

_Codex development: 2026-10-28._
