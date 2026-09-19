[English](android-hub.md) | [简体中文](android-hub.cn.md)

# Android authorization inbox and notifications

The existing Android app has an **Access** navigation item that opens **My Authorizations**. Sign in to your own HTTPS Hub with the existing owner account. This screen does not require a project selection and does not store third-party provider credentials.

The inbox defaults to pending, unexpired requests. **Granted access and history** shows existing grants, previous decisions, and connected accounts, including revocation controls for live grants. Opening a notification brings its matching request first after fetching its latest state; already handled, expired, or unavailable requests are explicitly identified. The inbox displays agent pairings, data-access requests, status, and expiry. Multiple connections to the same provider remain separate accounts. Before approving data access, review the agent, provider account, operation, reason, and expiry. The operation applies across the selected account, not only to examples mentioned in the agent-supplied reason. Approval is unavailable if the connected account is missing or disconnected. Approving an agent grants identity access for 7 days and reveals the connected-account catalog; the pending pairing expiry is a 15-minute review deadline. Agent pairing approval requires entering the verification code displayed by the CLI; the app never fills it from the owner response. Approve, deny, and revoke require an explicit confirmation. Expired or malformed pending entries cannot be approved locally, and the server remains authoritative.

## Notifications

Notifications are opt-in for each HTTPS server and owner account. Android 13 and later require notification permission. When notifications are blocked, the inbox remains usable. Notifications contain a generic pending count, and their private app intent contains an account scope and a request identifier; no provider token, document, email, sender, or approval action is placed in the notification.

There are two transports:

- **Periodic checks:** WorkManager polls while connected to a network, no more frequently than every 15 minutes. Android battery policies can delay these checks. These are periodic reminders, not real-time remote push.
- **Optional Firebase Cloud Messaging (FCM):** A configured build registers its device token with the Hub. A data message with `type=authorization_changed` wakes a background worker. The worker fetches the authenticated inbox before showing a notification and does not display the remote payload. Periodic checks remain as a fallback. Delivery timing still depends on the device and Android scheduling.

Session mutations and notification publication share a process-wide lock. UI responses are checked against the original endpoint, owner, session token, and view generation. Logout clears the active session, cancels periodic work and the authorization notification, and attempts to unregister the FCM device before revoking the server session. Requests already in flight cannot publish into a different session. Opt-in settings remain scoped to the original endpoint and account; returning to that account restores its preference. A stale push only causes a fresh read for the currently signed-in, opted-in account. Offline logout cannot guarantee immediate server-side device deletion, so push payloads must remain generic.

## Optional FCM build configuration

Default builds work without a Firebase project and explicitly show that remote push is not configured. To enable the client, register the Android package `life.integ.context` in your own Firebase project and supply all four public Firebase project identifiers in the build environment:

```sh
export EDC_FIREBASE_APP_ID='<Firebase mobile SDK app ID>'
export EDC_FIREBASE_API_KEY='<Firebase Android API key>'
export EDC_FIREBASE_PROJECT_ID='<Firebase project ID>'
export EDC_FIREBASE_SENDER_ID='<Firebase project number>'

cd app/android
JAVA_HOME=/opt/homebrew/opt/openjdk@17 \
ANDROID_SDK_ROOT=/Users/songyy/Library/Android/sdk \
./gradlew testDebugUnitTest assembleDebug lintDebug
```

The build requires all four values or none. It generates the Firebase resources directly; no `google-services.json` or Google Services Gradle plugin is required. Firebase Messaging auto-init is disabled, and a registration token is requested only after the active account has opted in. Firebase Analytics is not included. Never put a service-account private key or third-party access token in these variables or in the APK.

Client configuration alone is insufficient. The Hub must have its matching FCM server configuration and server credentials, and a device with working Google Play services must grant notifications. UnifiedPush is not implemented. See the [Firebase Android setup](https://firebase.google.com/docs/android/setup) and [FCM Android reception guide](https://firebase.google.com/docs/cloud-messaging/android/receive) for platform setup and delivery constraints.

## API contract

The app uses the existing owner bearer session:

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/hub/devices/status` | Check server `push_configured` status. |
| `GET /v1/hub/owner` | Read `agents`, `requests`, and `connections`. |
| `POST /v1/hub/agents/{id}/decision` | Submit `decision`; `verification_code` is required for approval. |
| `POST /v1/hub/requests/{id}/decision` | Submit `decision` (`approve`, `deny`, or `revoke`). |
| `POST /v1/hub/devices` | Register `{token, platform: "android"}` for the signed-in owner. |
| `DELETE /v1/hub/devices` | Remove `{token}` from that owner. |

Owner requests expose `agent_name`, `connection_id`, `connection_name`, `operation`, `reason`, `status`, and RFC3339 `expires_at`. Connections expose `id`, `provider_id`, `account_id`, `display_name`, and `status`. Agent records expose `id`, `name`, `status`, and `expires_at`. The device-token endpoint must enforce ownership and prevent a physical token from remaining associated with different owners on the same Hub.

## Verification boundary

`testDebugUnitTest assembleDebug lintDebug` validates source, resources, packaging, and tests for expired requests and endpoint/account/session changes. It does not prove delivery to a phone. Before claiming end-to-end Android push, configure a real Firebase project and server, install on a selected Android device, enable notifications, trigger a CLI pairing and a scoped data request, approve from the phone, and verify CLI access and revocation. Also verify permission denial, background delivery, logout, account switching, expired requests, offline recovery, and a stale push after logout. No real-device or live FCM delivery is established by the default build.
