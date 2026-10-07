# Google Hub application setup

[English](google-hub-setup.md) | [简体中文](google-hub-setup.cn.md)

One deployer-controlled Google OAuth application supports multiple Gmail and Calendar accounts, each with independent encrypted connections and Agent grants. Creating the application is separate from granting mailbox access. Use a Google account controlled by the deployer in [Google Cloud Console](https://console.cloud.google.com/); do not share account passwords or codes.

1. Choose/create a project and configure its OAuth consent screen (app name, support email, audience and contact). For a testing external application, add each intended Google account as a test user. Restricted Gmail scopes may require verification for wider distribution; testing/refresh behavior depends on Google policy.
2. Enable Gmail API, Google Calendar API and any other selected services. Scope requests are service-specific; authorizing Gmail does not silently connect Calendar or other accounts.
3. Create an OAuth **Web application** client. Register the exact redirect URL `https://YOUR_API/v1/hub/google/callback`. For the maintainer deployment it is `https://context-api.integ.life/v1/hub/google/callback`. This hosted deployment is optional for self-hosters.
4. Download the client configuration privately and use the deployer's existing private environment mechanism to set `EDC_HUB_GOOGLE_CLIENT_ID`, `EDC_HUB_GOOGLE_CLIENT_SECRET` and `EDC_HUB_GOOGLE_REDIRECT_URL`. Keep the existing `EDC_HUB_CREDENTIAL_KEY`; replacing it would make saved credentials unreadable. Never paste secrets in chat or commit them.
5. Restart this server and check `GET /v1/hub/google/status`. `configured: true` proves valid local application settings only. Then sign in to Context as the owner and complete `edc source connect --provider gmail --name 'Personal mail' --open`. Repeat with a distinct account/name and separately for `google-calendar`. Google selects the account and requires consent in the browser.
6. Pair a separate Agent and approve only the requested operation. Calendar content additionally requires exact calendar ID and a positive RFC3339 time window of at most seven days. Verify a real bounded read, second-account isolation, refresh and revocation before calling the accounts connected.

Official references: [Web server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server), [Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes), and [Calendar scopes](https://developers.google.com/workspace/calendar/api/auth). See [CLI commands](data-source-cli.md) and [connector implementation](hub-connectors.md).
