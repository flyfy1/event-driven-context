# Project overview deployment status

- Added a normalized deployment snapshot to the project overview model.
- The overview now renders the latest known environment, commit, branch, public route, check time, deployment status, and an explicit blocker/unknown message.
- Deployment metadata remains honest when no CI or route probe is attached: the card shows `Unverified` and explains that metadata is not connected yet. Projects can provide `deployment` or `checked_at` fields without changing the overview shape.
- Added localized labels for English, Simplified Chinese, Malay, and Hindi.
- Verification: `node --test frontend/project-overview.test.js`, `node --check frontend/app.js`, `node --check frontend/project-overview.js`, and `git diff --check`.
