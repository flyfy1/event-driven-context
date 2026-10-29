# Overlord mobile overview layout

## Scope

Keep the project overview usable on narrow screens while the workspace collapses to a single column.

## Changes

- Stack the overview heading and filter toolbar below 560px.
- Keep the filter select and refresh action touch-sized and full-width within the toolbar.
- Render summary metrics as a compact two-column grid and reduce tree/card padding on small screens.
- Allow status and task badges to wrap long labels without widening the page.

## Verification

- `node --test frontend/mobile-layout.test.js`
- `git diff --check`

The existing overview unit tests are currently red in the shared worktree because an in-progress deployment-status change does not return `children` from `buildProjectOverview`; that issue belongs to the status implementation and is reported to the coordinating agent.
