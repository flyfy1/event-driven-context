# Mobile App: Automatic Capture Pipeline and Screen Flows

English | [简体中文](mobile-capture-ux.cn.md)

Date: 2026-09-12 · Android is the current delivery platform, with its code kept separately under `app/android/`. The implementation scope has moved to [product-V2.md](product-V2.md): photo and file capture reuse the File and note queues, while image-analysis execution remains outside P1. The completed iOS prototype is retained, but its build and test results serve only as historical records. This document preserves the original mobile interaction sketches; the specific synchronization and review protocols follow V2's File → UUID Event and daily-review State models, not the old inbox approach, which is no longer an acceptance criterion.

This user direction takes priority over the manual file-upload entry point in the older desktop sketches. The older version is retained for comparison; the [product design](product.md) and [technical design, section 4.4](technical-design.md) have been updated accordingly.

## 1. Product Form

The mobile app is the everyday entry point: open it and start recording; when the recording ends, it is automatically saved, uploaded, and processed. The resulting content can then be used in conversations and daily reviews. The web app handles more complex project management, MCP integrations, and rule configuration.

This draws inspiration from Biji's low-friction capture experience. Its [official website](https://www.biji.com/) presents multimodal capture, automatic AI organization, and cross-device access as product capabilities. We borrow the capture patterns, while defining the specific screens, states, and backend contracts according to our own context and skill designs.

The current default meaning of “automatic” is that the user decides when to start and stop recording, after which all subsequent operations proceed automatically. Continuous ambient recording, automatic detection of meeting starts, and all-day passive listening are outside the default scope of this iteration.

## 2. End-to-End Flow

~~~mermaid
flowchart TD
    A["Open the app"] --> B["Tap Record"]
    B --> C["Recording: Pause / Stop"]
    C --> D["Automatically save to the phone when stopped"]
    D --> E{"Network available and login valid?"}
    E -->|Yes| F["Upload automatically"]
    E -->|No| G["Wait for network or login; original file remains on phone"]
    G -->|Resume automatically after recovery| F
    F --> H["Server confirmed: Synced"]
    H --> I["Automatic transcription / rule-based organization"]
    I --> J["Record details: Original media + organized results"]
    J --> K["Referenced by conversations as needed"]
    J --> L["Used on schedule by the Daily Review Skill"]
~~~

Users do not need to export recordings, switch apps, choose files, enter MIME types, or tap a second upload button. The default project and source are configured in advance and reused directly when a recording ends.

## 3. Mobile Information Architecture

~~~mermaid
flowchart LR
    A["Mobile app"] --> B["Capture"]
    A --> C["Review"]
    A --> D["Me"]
    B --> B1["Primary action: Start recording"]
    B --> B2["Additional entry points: Camera / Photo library / Files"]
    B --> B3["Recent records and sync status"]
    C --> C1["Daily review / Reminders / Suggestions"]
    D --> D1["Default private project / Account"]
    D --> D2["Skill and source rules"]
    D --> D3["Conversation integrations / Execution environment / Sync settings"]
~~~

The mobile bottom navigation contains “Capture,” “Review,” and “Me.” Recording is the most prominent action on the first screen and does not compete with administrative entry points such as rules and running tasks. The web app's existing “Capture, Use in Conversations, Plugins, Inbox” structure can remain; both clients organize the same set of capabilities according to how frequently they are used.

## 4. Screens Users See

| Screen | Main content | User action | What happens automatically |
|---|---|---|---|
| Initial setup | Sign in; default to “My Records,” visible only to the user | Choose the default location | Reuse it for subsequent recordings instead of asking each time |
| Capture home | Prominent record button, recent records, and pending-sync count | Start recording | Use the configured capture source and rules |
| Permission explanation | Why microphone access is needed; file import remains available if access is denied | Grant permission when using recording | Enter the recording screen once permission is granted |
| Recording | Clear recording status, duration, Pause, Stop, and destination | Speak, pause, or stop | Continuously write to a local file |
| Saved locally | “Recording saved on this phone” | Leave the screen if desired | Add it to the upload queue automatically |
| Uploading | Current upload status; the local original remains available | Usually no action required | Change the status to Synced after server confirmation |
| Waiting for network | “Recording is on your phone and will continue uploading when online” | Continue recording another item | Upload automatically when the required conditions are restored |
| Waiting for login | “Your session has expired; the recording remains on this device” | Sign back in to the original account | Resume that account's pending upload queue |
| Organizing | “Synced, transcribing”; use the time as a temporary title | View the original recording | Generate a title, transcript, and tag suggestions when processing completes |
| Record details | Organized results, original audio, citation sources, and processing status | View the original or append a correction | Become a candidate for future context |
| Review | The day's progress, decisions, gaps, and suggestions | View supporting evidence and accept suggestions | Generate at the scheduled time through the installed skill |

“Leave the screen” does not mean that the mobile operating system guarantees the app will never be suspended. Actual behavior while backgrounded or locked must be validated on a physical device; after returning to the foreground, the app should continue processing pending uploads reliably.

## 5. Keep Configuration from Interrupting Capture

The app or server fills in the source, media type, time, and device capture ID. The default private project is always visible, and the user can change it before the next recording. The system may discover topics and generate tags, but it must not automatically move private information into a shared project.

Transcription settings are configured once, such as “Meeting recording → Verbatim transcript” and “Voice note → Verbatim transcript, then extract ideas.” Images can likewise be processed using rules such as “Whiteboard → Summarize decisions” and “Bill → Extract amount.”

Processed titles, summaries, and topics are derived information with provenance. They must not be written back to replace the original event's metadata. After the user adds a correction, subsequent use should rely on the explicitly updated result, while the original media remains available for inspection.

## 6. Make It Clear Where Content Is Even When Something Fails

~~~mermaid
flowchart TD
    A["Original media on device"] --> B{"Sync status"}
    B --> C["Not uploaded: Recoverable on phone; unavailable on other devices"]
    B --> D["Synced: Server confirmed receipt"]
    D --> E{"Processing status"}
    E --> F["Processing succeeded: Transcript / analysis can be referenced"]
    E --> G["Processing failed: Original retained; retry available"]
    C --> H["Network restored / Original account signed in again"]
    H --> D
~~~

An upload failure must not require the user to record again, and a processing failure must not require another upload. If an acknowledgment is lost, the upload can be retried automatically, but it must reuse the same capture ID to avoid creating duplicate events.

When an interruption occurs, show which parts have been preserved rather than displaying only a failure indicator. If the user signs out or switches accounts, pending media remains bound to the original account and must not be silently uploaded to the new one. Files without server confirmation must not be cleaned up automatically.

## 7. Revised Minimum End-to-End Loop

Make the following work on Android: real recording → local persistence → automatic upload → transcription by a real Skill → reference in a new conversation → view the daily review on mobile. Any short-recording limit must be explained before recording begins. For now, do not promise meetings of unlimited duration, simultaneous availability on every platform, or continuous ambient recording.

Required validation: recording permission; recording interruption; ending a recording while offline; recovery after restart; signing back in to the original account; idempotency for repeated uploads; and separation of server confirmation from analysis status. Tapping “Choose an existing audio file” in a mobile browser is not a substitute for validating the app's recording pipeline.

The mobile sketches in this conversation demonstrate the home screen, recording, automatic saving/uploading/organization, offline recovery, and review. They do not request microphone permission, create a real recording, or upload anything to the server.
