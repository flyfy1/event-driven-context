# Event-driven Context mobile clients

Current App development and delivery target: **Android**, in the independent `android/` directory. The existing iOS prototype below is preserved as requested. Its build and test results do not establish Android completion.

The remaining instructions on this page describe the retained iOS prototype. Android build and acceptance instructions belong in `android/README.md`.

## Retained iOS capture prototype

This is the first native capture client. It records AAC audio in an M4A container directly to Application Support, persists an account- and endpoint-bound upload queue, and uploads completed recordings to `POST /v1/media-events` with a stable `capture_id` idempotency key.

The app requires an HTTPS API origin. It never removes an original recording after upload. A recording interrupted by termination, audio-session interruption, or explicit cancellation remains local and requires an explicit retry before it is uploaded as a partial recording. Restart recovery verifies that the file exists and is nonempty, then freezes its size and SHA-256; missing media is reported as unreadable rather than saved. The uploader verifies the exact bytes it will send against that frozen identity.

## Repeatable checks

Run the queue persistence tests on macOS (these test shared pure Swift logic, not iOS execution):

```sh
cd app/Core
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer swift test
```

Type-check the App and shared Core sources against the installed iPhoneOS SDK without changing the machine's global Xcode selection or signing account:

```sh
cd app
sdk_path=$(DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer xcrun --sdk iphoneos --show-sdk-path)
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer xcrun swiftc \
  -typecheck -parse-as-library -target arm64-apple-ios17.0 -sdk "$sdk_path" \
  EventDrivenContext/*.swift Core/Sources/EventDrivenContextCore/*.swift
```

This verifies Swift types only. It does not link, sign, install, launch, or exercise iOS runtime behavior.

## Simulator UI acceptance

The shared `EventDrivenContext` scheme includes `EventDrivenContextUITests`. Build it for a booted Simulator first:

```sh
cd app
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer xcodebuild \
  -project EventDrivenContext.xcodeproj -scheme EventDrivenContext \
  -destination "platform=iOS Simulator,id=$EDC_SIMULATOR_ID" \
  -derivedDataPath /tmp/edc-p1-sim-build build-for-testing
```

The tests read fixture settings in the test runner process. Arbitrary shell variables are not automatically forwarded by a standard `xcodebuild` test invocation, so copy the generated `.xctestrun` beside the original and add the environment there. The file must stay in the generated Products directory because its `__TESTROOT__` paths are relative to that location. Use only an isolated fixture account. The temporary copy is mode `0600`, contains its password, and is removed when the shell exits.

```sh
export EDC_UI_TEST_ENDPOINT=https://fixture-host.example.test
export EDC_UI_TEST_USERNAME=isolated-fixture-user
export EDC_UI_TEST_PASSWORD=isolated-fixture-password
export EDC_XCTESTRUN_SOURCE=$(find /tmp/edc-p1-sim-build/Build/Products -name '*.xctestrun' -print -quit)
export EDC_XCTESTRUN=$(python3 - <<'PY'
import os
import tempfile

descriptor, path = tempfile.mkstemp(
    dir=os.path.dirname(os.environ["EDC_XCTESTRUN_SOURCE"]),
    prefix="EDC-review.",
    suffix=".xctestrun",
)
os.close(descriptor)
print(path)
PY
)
trap 'rm -f "$EDC_XCTESTRUN"' EXIT
cp "$EDC_XCTESTRUN_SOURCE" "$EDC_XCTESTRUN"
chmod 0600 "$EDC_XCTESTRUN"
python3 <<'PY'
import os
import plistlib

path = os.environ["EDC_XCTESTRUN"]
with open(path, "rb") as source:
    document = plistlib.load(source)

targets = []
for configuration in document.get("TestConfigurations", []):
    targets.extend(configuration.get("TestTargets", []))
if not targets:
    targets = [value for value in document.values() if isinstance(value, dict)]

matching = [target for target in targets if target.get("BlueprintName") == "EventDrivenContextUITests"]
if len(matching) != 1:
    raise SystemExit("expected exactly one EventDrivenContextUITests target")
environment = matching[0].setdefault("EnvironmentVariables", {})
for name in ("EDC_UI_TEST_ENDPOINT", "EDC_UI_TEST_USERNAME", "EDC_UI_TEST_PASSWORD"):
    environment[name] = os.environ[name]
if os.environ.get("EDC_UI_TEST_ALLOW_MICROPHONE") == "1":
    environment["EDC_UI_TEST_ALLOW_MICROPHONE"] = "1"

with open(path, "wb") as destination:
    plistlib.dump(document, destination)
PY
```

Run the login and default-private-project path without microphone access:

```sh
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer xcodebuild \
  test-without-building -xctestrun "$EDC_XCTESTRUN" \
  -destination "platform=iOS Simulator,id=$EDC_SIMULATOR_ID" \
  -only-testing:EventDrivenContextUITests/RecordingUploadUITests/testLoginSelectsDefaultPrivateProject
```

`testDenyingMicrophoneCreatesNoCapture` explicitly chooses the system's **Don't Allow** action and verifies that no capture is queued. `testRecordingReachesAuthenticatedServer` is skipped unless `EDC_UI_TEST_ALLOW_MICROPHONE=1` was explicitly inserted into the temporary `.xctestrun`; enabling it records about two seconds from the host Mac microphone. Simulator checks remain separate from real-iPhone acceptance.

## Processing, review, and sources

After a media upload, the app verifies the returned event identity, file size, and SHA-256 before marking the local item `Synced`. It reads `GET /v1/automation/runs?project_id=...` for server processing state and considers only `audio-transcribe` runs whose `inputs[].event_id` matches the uploaded event. When more than one run matches, the highest generation and newest creation identity wins. `succeeded` becomes `Ready` only when the run contains an output event and no `no_output_reason`; a successful no-output run remains synced without claiming that a transcript exists.

The Review tab reads the authenticated `GET /v1/inbox` response, filters entries to the active account and selected project, displays the immutable candidate text and classified items, and marks an entry read through `POST /v1/inbox/{id}/read`. Expanding Sources follows candidate and event `source_event_ids` through authenticated `GET /v1/events/{id}` calls, with a visited set, a depth limit of eight, and a 128-event bound. Text sources are shown directly. Original audio is played only after authenticated `/v1/files/{id}/content` download and size/SHA-256 verification.

Every asynchronous inbox, run, source, and media response is checked against the current endpoint, account, session generation, selected project, and request identity before it can update visible state. Local upload queue queries remain scoped by endpoint and account.

## Device acceptance still required

Use a short non-private test recording on a real iPhone. Verify microphone denial and grant, pause/resume/end, interruption and lock-screen behavior, offline end then relaunch and network recovery, a deliberately lost upload response with one resulting event, original-account re-login, account and project switching during inbox/source requests, server download hash equality, authenticated source playback, processing refresh, and mark-as-read persistence. Background audio capability is declared, but background recording and uploads are not accepted until those device checks pass.

The current evidence covers macOS Core tests, iPhoneOS type-checking, and backend/browser automation flows. It does not prove linking, installation, microphone behavior, lock-screen survival, background execution, or inbox/source interaction on an iOS 17 device.
