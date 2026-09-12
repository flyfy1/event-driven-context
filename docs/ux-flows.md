# Event-driven Context: User Journeys and Page Flows

English | [简体中文](ux-flows.cn.md)

Date: 2026-09-12 · Status: Interaction design draft using sample data; it does not represent implemented pages.

Based on the [Product Design](product.md) and [Technical Design](technical-design.md). This document adds the entry points, copy, actions, and feedback that users see without changing the existing data and authorization constraints. The interaction mockup is shown in this conversation.

> Later revision: The main entry point for daily capture has changed to “start/stop recording → automatic upload and organization” in the mobile app. See the [Mobile Capture Flow](mobile-capture-ux.md). This document retains the earlier desktop mockup for comparison; its first-use path and manual recording-selection path no longer represent the main mobile flow.

## 1. Information Architecture

The recommended primary navigation has only four items. Users choose a project first, and all content respects that project's boundary. The personal inbox is an entry point to results; it does not change the visibility of the project to which a result belongs.

~~~mermaid
flowchart TD
    A["Public home page / Sign up / Sign in"] --> B["Create or select a project"]
    B --> C["Records · Default home"]
    B --> D["Use in Conversations"]
    B --> E["Plugins"]
    B --> F["Inbox"]
    C --> C1["New record: Text / Audio / Image"]
    C --> C2["Record details: Original content / Processing results / Append correction"]
    D --> D1["Connect a conversation tool / Select project scope"]
    D --> D2["Test retrieval / Source details"]
    E --> E1["Installable Skills / Installed"]
    E1 --> E2["Processing rules / Schedules / Analysis instructions"]
    E2 --> E3["Execution environment / Run history"]
    F --> F1["Reviews / Reminders / Suggestions / Share drafts"]
    F1 --> C2
~~~

“Rules” and “Run Details” do not need to be peer-level management modules on the first screen. They can be opened from a plugin or a processing result. Put the project switcher, member visibility, and language entry point in the application top bar; put runner details inside each plugin.

On desktop, use left-side navigation and a right-side workspace. On narrow screens, use a top-bar project entry point, the main content, and four-item bottom navigation. In this mockup, the bottom bar appears in the document flow; the real product must account for the safe area and on-screen keyboard. Source details can appear in a side panel on desktop; on narrow screens, open a dedicated details page and preserve the user's return position.

## 2. Journey One: First Use—Save Some Context

User goal: Save a project's background here so they can continue from it next time.

~~~mermaid
flowchart LR
    A["Home: Start recording"] --> B["Sign up / Sign in"]
    B --> C["Create project: Name + Visibility"]
    C --> D["Empty project: Save the first piece of context"]
    D --> E["Original record saved"]
    E --> F["Keep recording"]
    E --> G["Optional: Connect a conversation tool"]
    E --> H["Optional: Install a processing plugin"]
~~~

| Page | What the user sees | Primary action | Feedback after the action |
|---|---|---|---|
| Account entry | Username, password requirements, sign-in switch, language entry point | Create account / Sign in | Proceed to project setup; errors remain on the current page |
| Create project | Name; “Only visible to you”; sharing requires adding members | Create project | Show project name and visibility |
| Empty project | “Save some context first”; text, audio, and image entry points | Create first record | Open record input |
| First record | Original content, recorded time, source, and next-step entry points | Keep recording | Do not force users to configure a model, plugin, or cron first |

Users can write and read original information even without a runner. Audio and images can be saved first; when no processing is configured, clearly show “Automatic processing has not been set up.”

## 3. Journey Two: Upload a Recording and Get Citable Content

User goal: Save what they just said so future conversations can use it.

~~~mermaid
flowchart TD
    A["Records → New → Audio"] --> B["Select file / Source / Optional note"]
    B --> C{"Was the original file saved?"}
    C -->|No| D["Not saved: Preserve the form, explain the error, allow retry"]
    C -->|Yes| E["Original recording saved"]
    E --> F{"Did it match a processing rule?"}
    F -->|No| G["Saved only; automatic transcription can be configured"]
    F -->|Yes| H["Waiting for processing / Transcribing"]
    H -->|Success| I["Transcript + Original recording"]
    H -->|Failed or offline| J["Original recording saved; processing incomplete"]
    J --> K["Reconnect execution environment / Retry this processing run"]
    K --> H
    I --> L["Append correction"]
    L --> M["New version is citable; original record and old versions retained"]
~~~

| Page | Key content | Primary action | Secondary action |
|---|---|---|---|
| Recording input | File name, size, and project; optional source; which rule will apply | Save recording | Change source / View processing requirements |
| Saved | “Original recording saved”; a separate “Waiting for transcription” status | Return to records | View processing progress |
| Transcript details | Verbatim transcript and original file entry point; model-generated label; provenance | Use in conversation | Append correction / View old versions |
| Processing failed | “Your recording was not lost”; the specific reason and latest run | Retry processing or reconnect | View original recording |
| Correction complete | User correction, linked previous result, and current default version | Return to record | View version history |

There is no “Edit original log” button here. Users can edit a pending correction draft in the editor; clicking “Append correction” saves it as a new record. Changes to a configured prompt also do not directly overwrite an old transcript.

Use different copy for a save failure and an analysis failure. The former does not claim that data was saved; the latter always provides a working entry point to the original file. Run details are a secondary entry point; do not substitute error codes for actionable guidance.

## 4. Journey Three: Continue in a New Conversation

User goal: Get an answer based on the current state without repeating the project's background.

~~~mermaid
flowchart TD
    A["First time: Use in Conversations → Connect tool"] --> B["Sign-in and authorization: Current user / Read capability"]
    B --> C["Select the project scope available to this conversation"]
    C --> D["Return to the external conversation tool"]
    D --> E["Ask: What should we do first this week?"]
    E --> F["Read relevant context"]
    F --> G["Answer + Source citations + Unresolved questions"]
    G --> H["Click source → View original record"]
    F --> I["Insufficient context / Audio not yet processed"]
    I --> J["Add a record / Adjust query scope"]
    J --> E
~~~

| Page / Location | What the user sees | Primary actions |
|---|---|---|
| Use in Conversations / This product | Connection instructions, latest verification status, and a question box for test retrieval | Connect a conversation tool |
| Authorization and scope / This product or the client's authorization page | Account, project, and read scope; write access shown separately | Allow access to the selected projects |
| New conversation / External tool | User's question; model's answer; clickable evidence | Ask a question, view sources |
| Source details / This product | Original content, author, time, current status, and superseded history | Return to conversation / Append correction |
| No results / Conversation or test-retrieval page | “There is not enough information yet”; actual query scope and gaps | Add a record or narrow the question |

Label the conversation area in the mockup “External conversation tool illustration” rather than treating it as another chat product to build. Show a successful connection separately from “the conversation actually called context.” Establishing an MCP connection alone does not warrant showing “All future conversations now automatically have memory.”

Example: The old budget was 50,000, and the user later explicitly changed it to 30,000. The answer shows the current budget of 30,000 and cites the update record; if an unlinked conflict exists, retain the uncertainty. Label the model's “Contact the supplier on Friday” as a suggestion rather than listing it as a task the user has committed to.

## 5. Journey Four: Install an Ongoing Service

User goal: Have the system organize things for them at the appropriate time without first learning automation infrastructure.

~~~mermaid
flowchart TD
    A["Plugins → Add Skill"] --> B["Choose purpose: Recording to text / Daily review"]
    B --> C["When to run: New record / Daily time"]
    C --> D["What to process: Project / Source / File type"]
    D --> E["How to process: Analysis instructions / system prompt"]
    E --> F["Where results go: Project + Personal inbox"]
    F --> G["Select an execution environment and run an example"]
    G -->|Available| H["Review sample result → Enable"]
    G -->|Offline or unsupported| I["Save configuration; reconnect or change execution environment"]
    I --> G
    H --> J["Enabled: Next run time / Edit / Pause"]
~~~

The configuration page uses four plain-language questions as group headings:

1. **When should I help?** When new audio arrives, or every day at 21:00, with the time zone displayed.
2. **What information should I use?** The current project, the user's own new inputs or authorized history, source, and type.
3. **How should I help process it?** An editable prompt; the complete skill is available in Advanced Settings.
4. **Where should I put the results?** The project and the in-product inbox; external sharing is configured separately.

| Page | Key content | Primary action |
|---|---|---|
| Choose Skill | Purpose, version, required tools, and processing example | Configure this service |
| Configure | Trigger, scope, prompt, and output; execution environment status | Run an example |
| Sample result | Generated result, sources, and whether execution completed; “Scheduled task not enabled yet” | Enable |
| Enabled | Next run, current rule, recent results, and pause entry point | View results / Edit |
| Execution environment issue | Distinguish offline, sign-in required, and unsupported capability | Reconnect and verify |

By default, the configuration affects only future records; when users choose to reprocess history, clearly show the scope. When upgrading a pinned skill version, show what changed and request authorization again for new permissions. The rule-match preview explains “why this record was processed” without exposing YAML throughout ordinary pages.

## 6. Journey Five: Receive and Respond to an End-of-Day Review

User goal: See what moved forward today, identify omissions, and decide the next step.

~~~mermaid
flowchart TD
    A["User's configured time arrives"] --> B["Daily Review Skill runs"]
    B --> C["A review appears in the inbox"]
    C --> D["Open: Progress / Decisions / Unresolved items / Suggestions"]
    D --> E["Click evidence → View original record"]
    D --> F["Accept suggestion → Confirm specific content"]
    F --> G["Append user-confirmation record"]
    D --> H["Ignore suggestion"]
    D --> I["Adjust / Pause daily review"]
    I --> J["After pausing, no new tasks start; history is retained"]
    B --> K["Failure or sign-in required → Run issue entry point"]
~~~

| Page | Key content | Primary action / Consequence |
|---|---|---|
| Inbox | Title, date, owning project, and unread status | Open review; read status means only that it was read |
| Review details | Separate progress, decisions, and suggestions; sources for each item | View evidence; accept or ignore suggestions |
| Confirm feedback | “Record ‘Contact the supplier on Friday’ as your plan” | Append a record only after explicit confirmation |
| Accepted | “Recorded as your plan”; the original suggestion remains labeled as model-generated | View the new record |
| Paused | The current plugin is paused; the next run has been canceled | Re-enable / Return to inbox |

When there is nothing worth surfacing, the reminder plugin stays quiet. A daily review can still be generated according to the user's arrangement, but it should say “There were no new records today” or “A recording is waiting to be processed” rather than inventing progress.

Notification delivery, reading, acceptance, and task completion are four different events. Remind later changes only the notification time; it does not automatically change the user's commitment.

## 7. Journey Six: Image Understanding and Optional Sharing

This is an extension of the full product direction. The mockup covers the experience without expanding the scope of the existing next-round audio MVP.

~~~mermaid
flowchart TD
    A["Upload whiteboard image"] --> B["Match Image Analysis Skill by source"]
    B --> C["Analysis result: Decisions / Needs confirmation / Original image"]
    C --> D["Generate share draft"]
    D --> E["Preview image and copy; choose a connected destination"]
    E --> F["Confirm published content and visibility"]
    F --> G["Send"]
    G -->|Explicit receipt| H["Sent: Receipt and destination"]
    G -->|Uncertain result| I["Delivery awaiting verification; avoid sending twice"]
    I --> J["Check channel receipt"]
    J --> H
    C --> K["Keep analysis private"]
~~~

Example image-analysis prompt: “Summarize the decisions and items awaiting confirmation on the whiteboard. Mark any text that is illegible.” The analysis page also retains the original image and does not present OCR output or model inferences as text from the image.

The sharing entry point defaults to “Generate draft,” and the draft can be edited before submission. The confirmation page clearly states what will be sent, where it will go, and who can see it. The first sharing path requires confirmation each time; automatic sharing is a rule that must be enabled separately during installation and cannot be authorized by text within an image.

A failed send retries only delivery; when the send result is uncertain, check the receipt first. Uninstalling a plugin does not retract content that has already been sent externally. Every send button in the mockup changes only the demonstration state and does not access an external channel.

## 8. Shared Page Rules

| User-visible state | Copy direction | Available action |
|---|---|---|
| Empty project | “Save something you want your next conversation to know” | New record |
| Saved, no processing installed | “Original file saved; automatic transcription has not been set up” | Set up transcription / Keep recording |
| Runner offline | “Recording saved; waiting for your execution environment to come online” | View environment / Retry after recovery |
| Analysis failed | “This transcription did not complete; the original recording is still available” | Retry this run |
| No retrieval results | “Not enough evidence was found in the selected projects” | Add a record / Adjust scope |
| Conflicting conclusions | “Two records disagree; it is not currently possible to determine which is correct” | View each source / Append clarification |
| Suggestion not accepted | “System suggestion” | Accept / Ignore |
| Paused | “Paused; historical results are retained” | Re-enable |
| Delivery uncertain | “Delivery cannot be confirmed yet” | Check receipt |

Every submit button needs to distinguish clearly between submitting and successfully submitted. Preserve reusable input after a failure so users do not have to infer state by clicking repeatedly. Important states cannot be conveyed by color alone. Forms and actions must be keyboard-accessible, buttons must be tappable on narrow screens, and error messages must be associated with their fields.

Language behavior follows the end-to-end constraints in the product document: public pages, authorization, the workspace, status, and external return flows retain the user's selected locale. This round's Chinese mockup demonstrates only the Chinese information architecture and interaction. It is not evidence of four-language implementation or acceptance; do not present a nonfunctional language dropdown as completed work.

## 9. How to Review This Round's Interaction Mockup

The mockup in this conversation has six journey entry points. Above each journey, reviewers can switch among its main pages; key exceptions have dedicated entry points. Each page's primary button advances the corresponding flow. Inputs such as project name, query, and prompt can be edited within the demonstration's scope. All data and connections are samples and do not trigger real writes, installation, sign-in, scheduling, or publishing.

Suggested priorities for discussion: whether the four primary entry points feel intuitive; whether record details clearly distinguish original content from processing results; whether installation is too burdensome; whether conversation citations and suggestion confirmation are clear; and whether users would find the daily review worth opening.

Implementation acceptance remains governed by the product document. Normal states in the mockup are for discussing the expected experience, not proof that backend capabilities are live.

Checks for this round: The interaction mockup contains 31 pages/states. Local-browser testing covered all six journeys and key exceptions through actual clicks, verifying input changes, source-condition matches and mismatches, pause and resume, suggestion acceptance, and share-receipt feedback. At a 390px viewport, plugin configuration, review, conversation, recording input, run details, image, and draft views showed no horizontal overflow or controls extending beyond their bounds. The browser reported no script errors. These checks validate only the mockup; they do not replace acceptance of the real backend, agent, or four-language implementation.
