# Event-driven Context Android

This independent Android client implements the Product V2 capture slice without changing the retained iOS prototype. It records AAC audio in an M4A container, freezes the local file size and SHA-256, persists a UUIDv7 queue item in SQLite, and uses WorkManager to upload the File before recording the note Event. A capture becomes synced only after the server returns `created` or `duplicate` for that same UUID.

The queue is bound to its original HTTPS endpoint, account, and project. Selecting another project does not retarget queued data. Signing in again as the original account resumes eligible work; a different account cannot upload the old queue. Tokens are encrypted with Android Keystore and are not stored in the queue or logs.

The Record page provides recording, pause/resume/end, camera capture, supported file selection, local queue status, and authenticated server records. Review reads `daily-review/` State and opens authenticated Event sources. Me shows the active account, endpoint, project selection, queue count, plugin status, and sign out.

## Local checks

Use the project wrapper and an explicit JDK/SDK so the result does not depend on shell-global Android settings:

```sh
cd app/android
JAVA_HOME=/opt/homebrew/opt/openjdk@17 \
ANDROID_SDK_ROOT=/Users/songyy/Library/Android/sdk \
./gradlew testDebugUnitTest assembleDebug lintDebug
```

Install only when a selected emulator or real device is already available:

```sh
/Users/songyy/Library/Android/sdk/platform-tools/adb install -r app/build/outputs/apk/debug/app-debug.apk
```

No endpoint, username, password, token, signing key, or private recording belongs in Gradle properties, this repository, screenshots, or logs.

## Acceptance boundary

A build proves only source and resource compatibility. Before calling Android complete, use an isolated HTTPS account and project to verify microphone denial, pause/resume/end, notification stop, interruption, lock screen, offline finish and process restart, lost HTTP response, account/project switching, File and Event deduplication, and authenticated download byte/size/SHA-256 equality. A real Android device is required for microphone, interruption, lock-screen, and vendor background-policy evidence. Processor, transcript, project-brief, and timed daily-review evidence remain tied to the corresponding V2 backend gates.
