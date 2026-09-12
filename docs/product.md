# Event-driven Context Product Design

English | [简体中文](product.cn.md)

> Historical product design: the user has designated [product-V2.md](product-V2.md) as the current basis for development and acceptance. This document preserves the original discussion and earlier scope; it must not be used to reduce the V2 requirements.

Version: 0.1 · Date: 2026-09-12 · Status: post-discussion design draft; unimplemented capabilities do not constitute delivery commitments.

Companion documents: [Technical Design](technical-design.md); [Existing First-Version Scope](mvp.md). Section 10 of this document is the source of scope for the next minimum validation; the other sections describe the full product direction.

## 1. Product Positioning and Goals

Event-driven Context is a product that continuously receives raw records, retrieves context when needed, and lets users install skills to process information and receive proactive services.

Information that users accumulate across different tools and conversations is stored as append-only events. An agent in a conversation retrieves relevant background based on the current question; user-installed plugins are triggered when new records arrive or at specified times, and a selected agent executes the skill to produce transcriptions, analyses, reviews, reminders, or recommendations.

2026-09-12 addition: the primary entry point for everyday recording is now a simple mobile app. After a user starts and stops a recording, the app automatically saves and uploads it, and the backend automatically continues with transcription and organization; selecting and uploading an existing file remains a supplementary entry point. The interaction takes inspiration from the lightweight capture workflow of Dedao Brain (得到大脑), while the product's subsequent value remains centered on cross-conversation context and configurable skills.

2026-09-12 platform decision: subsequent app development and this delivery target Android only, in the dedicated `app/android/` directory. The completed iOS prototype is retained but is no longer a target for continued development or acceptance in this iteration; iOS test evidence cannot substitute for Android acceptance.

Core value: when users switch conversations, tools, or agents, they do not need to restate background that has already been recorded. The system can use existing information to help them move work forward while preserving inspectable evidence.

The initial target users are people who continuously advance projects while working across multiple AI conversations. The product supports personal use and retains the existing model in which project members can jointly read and write. For now, it is not intended to be a replacement for a general-purpose enterprise data platform, chat client, or task manager.

## 2. Confirmed Direction and Default Assumptions

### 2.1 Confirmed in This Discussion

- Events are continuously written through multiple entry points; MCP is one of the primary integration methods.
- Raw events and metadata are append-only and cannot be edited or deleted in place.
- Context is primarily retrieved for the current task on each invocation; users are not required to maintain a fixed, comprehensive background document.
- Passive organization, reminders, and recommendations are provided by plugins that users choose to install.
- Plugin processing logic may use a skill, and scheduled tasks may wake an agent selected by the user to execute it.
- Users can configure input processing by source and type, including image-analysis prompts, audio transcription, and more.
- Images and other content may proceed into a sharing workflow; configuration determines whether they are shared.
- The mobile app is the primary media-capture entry point. Upload and processing after recording should be as automatic as possible, minimizing the need to enter a project, type, and metadata every time.

### 2.2 Defaults Adopted to Complete the Design

The following are adjustable design decisions, not requirements presented as though the user had confirmed each one individually.

| Item | Default Decision | Rationale |
|---|---|---|
| Data boundary | Retain Project; personal information can go in a project that only the user has joined | Reuses existing authorization and avoids aggregating all projects by default |
| Execution location | Prefer a user-controlled runner connected to an agent they have already configured | Matches the concept of using the user's own agent environment |
| Execution account | Credentials remain at the execution endpoint; non-interactive execution and tool capabilities are verified individually | Does not assume every subscription account supports automation |
| Input processing | Save the raw record first and process it asynchronously | A processing failure does not lose the original content |
| Mobile recording | The user explicitly starts and stops recording; stopping immediately places it in the automatic upload queue | “Automatic” here means the post-capture pipeline by default; continuous ambient recording is not yet in scope |
| Default destination | On first use, create or select a private project named “My Records”; reuse it thereafter and always show it | Avoids requiring classification each time and does not automatically send private media into a shared project |
| Mobile offline behavior | First persist to an on-device queue; automatically retry when the app can run and the device is online | On-device storage and server confirmation are distinct states |
| Configuration method | Use forms for common conditions; support file import for skills and advanced configuration | Ordinary users should not need to understand cron or YAML first |
| Scheduled output | Write to the same project by default and place it in the installer's in-product inbox | The first version does not need to connect to external messaging channels |
| External sharing | Default to a draft; execute only after the user explicitly configures a destination and automatic publishing | Sharing permissions are separate from reading and analysis |
| Raw records and inferences | Label them explicitly and retain source citations | Prevents turning model suggestions into user commitments |
| Configuration effect | Applies to future records; the user chooses whether to rerun history | Avoids processing all history after a single prompt change |
| Operating scale | Start with one backend writer and serial execution on one runner | Prove value before addressing parallelism and scale |

There are currently no unresolved questions that prevent completion of the documentation. The specific agent/transcription tool, execution host, external channels, and resource budget should be determined when the corresponding implementation is built or enabled; this document does not assume authorization to deploy, configure real cron jobs, or publish content.

### 2.3 Capability Claims Must Cover the Complete User Journey

The product cannot claim that it supports a capability merely because an isolated page or endpoint implements it. This is especially true for multilingual support: a language can be listed as “supported” only after it covers every interface and item of feedback that users encounter while completing real tasks. Translating only the OAuth authorization page does not constitute product-wide multilingual support.

At a minimum, the current multilingual completion scope includes the public home page, registration, login, logout, project and record workspace, upload and query, plugins, rules, run details, inbox, OAuth login and authorization confirmation, as well as the input guidance, validation, success, empty, loading, failure, and permission states in these flows. Subsequent user-visible capabilities must add every supported language in the same delivery and cannot rely indefinitely on fallback copy in the default language.

Language state must form a continuous journey: the first visit may choose a language based on the browser language; pages provide a discoverable manual switcher; the user's choice persists across refreshes and logins; the locale is explicitly passed and preserved when navigating from the main site into OAuth or initiating OAuth from an external client; and switching languages must not lose valid process state outside a form, the authorization transaction, or the return destination. When the user has made no selection and the browser language is unsupported, the product falls back to English and remains consistently in English rather than switching languages unpredictably across pages.

Until English, Simplified Chinese, Bahasa Melayu, and हिन्दी cover the scope above and pass acceptance, they may only be described as “languages available on the OAuth pages,” not as “languages supported by the product.”

## 3. User Mental Model

| Concept | How Users Understand It |
|---|---|
| Event | A record left by a person, source, or agent at a particular time |
| Raw record | The original text or file saved at input time, with identity, time, and metadata |
| Derived record | A transcription, analysis, summary, or other result produced from existing records that can be traced back to the original content |
| Context | Background material and evidence selected for the current question, not a single permanent master summary |
| Memory | Facts, preferences, decisions, and constraints in historical events that can be reused over time; the first phase does not establish a separate, editable memory repository |
| Skill | Instructions and required resources that describe how to complete a type of work |
| Plugin installation | A selection of an executor, data scope, configuration, and permitted outputs for a skill |
| Rule | Determines which event or time triggers a plugin |
| Run | One actual execution, with input, version, result, and status |
| Inbox | Displays plugin output and run issues that require user attention |

A skill is not itself responsible for timing, account login, or message delivery. The runner, scheduler, and delivery capabilities jointly provide those functions. What the user sees is an installable, configurable, and pausable service.

## 4. Three Primary User Journeys

### 4.1 Recording and Input Processing

1. The user opens the mobile app and taps record; if microphone permission is needed for the first time, request it here. The default private project and save location are always visible.
2. When the user finishes speaking and taps stop, the app immediately and reliably saves the raw media and upload task on the device, without requiring another form submission or upload tap.
3. When network connectivity, login, and system execution conditions are available, the app uploads automatically and marks the item “Synced” after receiving server-side event confirmation. If the device is offline, the app is interrupted, or the login has expired, the on-device queue is preserved.
4. The server matches a processing skill using the bound source, media type, and preconfigured metadata rules.
5. The runner uses a pinned skill version and the user's prompt to complete transcription or analysis, appends a derived event, and associates it with the original media.
6. In the record details, the user sees the original media, transcription, and subsequent organization; the content automatically becomes available for context retrieval and enabled daily reviews.

The everyday workflow converges on “start recording → stop.” Taking a photo, choosing from the photo library, and importing a file use the same upload queue; web, CLI, MCP, and other sources remain available. Titles and tags may be suggested automatically after processing; system inferences do not rewrite raw metadata.

The mobile client must distinguish “Recording,” “Saved on Device,” “Waiting for Network/Login,” “Uploading,” “Synced, Awaiting Processing,” and “Processing Complete.” It can claim that synchronization is complete only after the server confirms submission. Background execution and lock-screen recording must be tested on the selected mobile platform; the product does not promise continuous recording or immediate upload under all conditions.

Users must be able to record and query even without installing any plugin. Having no matching rule is a normal state, not a failure.

The same file type can serve different purposes: extract expense information from a bill image; organize a plan from a whiteboard image; transcribe speech from meeting audio; or preserve a verbatim transcription of a voice note before generating a summary of its ideas. Transcription and rewriting must be distinguishable results; polished text cannot be presented as the user's original words.

### 4.2 Active Retrieval in a Conversation

1. The user states the current task in a new conversation.
2. Through MCP, the conversational agent submits the question, explicit project scope, and output budget.
3. The system selects relevant raw records and available derived records, identifies explicit update relationships, and includes timestamps and provenance.
4. The agent answers the question using this evidence and reads the original content or file when needed.
5. A new decision that must be retained is appended by the user or by an agent authorized to write; the complete chat is not saved automatically.

Returned content prioritizes goals relevant to the task, current decisions, constraints, incomplete items, and conflicts. Historically relevant content that has been explicitly superseded remains as background and is not treated as current state.

The system must be able to express “not found,” “conflict exists,” “audio not yet transcribed,” and “only part of the scope was searched.” It must not conceal missing evidence behind an apparently complete answer. MCP provides invocation capabilities; whether a specific client invokes them automatically still requires integration agreements and validation in real conversations.

### 4.3 Scheduled Organization, Reminders, and Recommendations

1. The user installs a skill plugin such as Daily Review and selects the project, executor, time, time zone, and output location.
2. A cron job or another scheduler periodically wakes the runner; the runner requests and claims due tasks.
3. The plugin retrieves context for its own task and performs organization or evaluation.
4. It generates an evidence-backed result, writes it back to the project, and delivers it according to the configuration.
5. The user can inspect sources, accept a suggestion, correct a conclusion, or pause the plugin.

A daily review may be produced on the agreed schedule; reminders and recommendations should be allowed to return “nothing worth notifying.” The system should suppress duplicate reminders and retain feedback such as read, dismissed, and remind later. Whether a particular task is actually complete must be based on records or user feedback; “read the reminder” cannot be interpreted as “completed the task.”

## 5. Dynamic Rules and Plugin Configuration

### 5.1 Installation Contents

Every installation specifies at least the skill name and version, project, installer, executor, permitted read scope, permitted output types, and whether it is enabled. Event rules additionally select sources, types, and metadata; scheduled rules additionally select a time and time zone.

Users can modify the analysis prompt, configuration, enabled state, and rules. A modification creates a new configuration version; an in-progress task retains the version with which it started, while pausing or revoking permissions immediately prevents subsequent claiming, submission, or delivery.

When installing a third-party skill, users should be able to inspect its description and required tools. Before upgrading a pinned version, show any added capabilities or permissions rather than silently expanding authorization. The first iteration provides only local skill packages or packages distributed with the product; it does not build a plugin marketplace.

### 5.2 Matching Behavior

- Conditions use AND by default. Sources can distinguish between a source bound through an authenticated integration and a label declared by the user.
- When the same record matches rules for different purposes, they may run separately, such as transcription and file description.
- When multiple rules match the same stage of the same installation, the highest-priority rule wins, and the interface shows which rule actually matched.
- Plugin output does not retrigger input processing by default; chaining such as “summarize after transcription” requires explicit configuration of the next stage.
- Changing a prompt does not automatically rerun history. A manual rerun generates a new version, and the old version remains available.
- Retries should reuse saved input, results, and delivery records whenever possible to avoid duplicate output.

### 5.3 Prompts and Operational Permissions

A prompt describes how the user wants the content analyzed, for example: “Summarize the decisions and open questions on the whiteboard. Do not invent text that is illegible.”

Readable projects, tool permissions, scheduled rules, sharing destinations, and the automatic publishing switch use explicit configuration. Text such as “ignore the rules” or “send this to an address” appearing in a log or image is data to be analyzed and does not change these settings.

## 6. Records, Updates, and Trustworthiness

### 6.1 Record Types and Evidence

Raw input, transcriptions, candidate facts, inferences, suggestions, and user confirmations are labeled separately. A derived record must identify its input record, skill version, and execution batch. Traceable provenance does not guarantee that the content is correct; a transcription may mishear speech, and an image may be illegible.

User-provided raw metadata remains free-form JSON. Platform-controlled identity, source bindings, execution provenance, and relationships are stored separately and cannot be impersonated through ordinary metadata.

### 6.2 Corrections and Supersession

Users change how content is used subsequently by appending a correction, retraction, or confirmation record. An update that explicitly points to an earlier conclusion may change the default effective state; two unrelated contradictory statements cannot be judged automatically by simply treating the later one as correct.

For example, “Yesterday's budget was 50,000” and “The budget changed to 30,000, superseding the previous record” establish the current budget. If two people separately state 30,000 and 50,000 without specifying a supersession relationship, the system should present a conflict.

When a user accepts a suggestion, the system should append a confirmation event bearing the user's identity; a system-generated suggestion does not itself become a commitment. Multiple summaries of the same source material cannot be treated as independent pieces of evidence that strengthen a conclusion.

### 6.3 Scope of Append-only

Raw events, raw files, committed derived results, and run audit records are stored append-only. Indexes, caches, current task states, and current pointers to user configurations may be updated or rebuilt.

Retraction and exclusion from default retrieval do not constitute physical deletion. The first iteration retains the existing contract that ordinary events cannot be deleted; before long-term hosting is opened to a broader audience, account closure, data retention, and administrative erasure policies must be determined separately. This document does not interpret append-only as a commitment to permanent retention.

## 7. Visibility, Execution, and Delivery

### 7.1 Projects and Users

Retain the foundational model in which project members can read the complete project history and append to it. Private records go into a private project; installing a private plugin does not imply that its output written to a shared project is visible only to the installer.

Members can manage their own plugin installations, which trigger only on their own new input by default. Read scope may include the project history they are authorized to access. Shared automation that responds to input from all members is managed by the project's creator. A personal installation cannot modify another member's installation or shared rules.

In the first iteration, a single run accesses only one project and writes its result back to that project. Cross-project aggregation and a private derived-data space are deferred for separate design to avoid accidentally merging sharing boundaries.

### 7.2 The User's Own Execution Environment

The user configures a runner that supports the required tools. The runner must clearly show its online status, the time of its most recent successful execution, whether login is required again, and any capability mismatch.

Connecting an account does not mean that the account has transcription, vision, or unattended-execution capabilities. Before enabling a corresponding rule, run a small-sample validation; if a capability is missing, provide an actionable error rather than silently switching to a paid API or a different account.

### 7.3 Sharing and Notifications

In-product results are available by default. External notifications, image sharing, and publishing copy are separately configured destinations and actions; they default to drafts. Automatic publishing requires prior, explicit authorization of the content type, scope, and destination, and authorization is checked again before execution.

Sharing state and generation state are separate: if generation succeeds but sending fails, retry only the send. If receipt by the destination cannot be determined, show “Verification Needed” rather than repeatedly sending without confirmation.

Pausing stops new executions and makes a best effort to terminate running tasks; uninstalling revokes the associated execution permissions and subsequent delivery while preserving existing results and history. Content already sent externally cannot be retracted automatically by uninstalling.

## 8. Product Interface

The mobile app handles everyday capture: its bottom navigation contains “Records,” “Review,” and “Me,” and recording is the prominent primary action on the Records home screen. The default destination, upload status, and availability of the original media should be directly visible; Skills, source rules, and execution environments are grouped under “Me” as infrequent configuration. Review reuses the in-product inbox rather than introducing a separate result data source.

The web interface continues to handle project browsing, conversational integration, more complex rule configuration, and troubleshooting, gradually adding necessary entry points to the existing project page:

| Interface | Core Actions |
|---|---|
| Project Records | Upload, view original content, view derived results, view processing status |
| Context Trial | Enter a question and inspect retrieved evidence, current conclusions, and gaps |
| Plugins | Install, select an executor, configure prompt and scope, enable or disable, pin or upgrade a version |
| Rules | Configure processing by source and type, configure scheduled tasks, preview whether a record matches |
| Run Details | Input version, status, result, error, retry, and rerun |
| Inbox | View reviews, reminders, and suggestions; navigate to evidence; provide feedback or dismiss |

The interface prioritizes user outcomes, such as “Turn a meeting recording into text”; technical parameters go in advanced settings. Upload progress, saved status, and analysis progress are displayed separately. Critical flows must be operable on desktop and narrow screens, and state changes must be exposed to assistive technologies.

All user-visible text should live in unified locale resources, including dynamic statuses, form constraints, backend error mappings, dates and times, and quantity expressions. The language switcher must be discoverable on both public pages and authenticated interfaces; OAuth pages reuse the same supported-language list and locale propagation contract. Hard-coded interface text in any language, or any flow that switches back to another language, means that the multilingual implementation is incomplete.

## 9. Quality, Cost, and Failure Experience

- A successful write is defined by persistence of the raw record; processing speed does not block write confirmation.
- Both retrieval and generation have budgets; when a budget is exceeded, the product explains the processed scope rather than silently truncating or claiming completeness.
- Every important generated conclusion includes a source; when evidence cannot be found, label it as a suggestion or uncertain.
- Configure timeouts, retry limits, and maximum daily run counts; show provider usage statistics when available and show unknown when usage cannot be measured.
- Retain tasks while the runner is offline; after recovery, execute according to the backfill policy and avoid sending several days of reminders all at once.
- Credentials, raw audio/video, and complete model outputs do not enter ordinary operations logs; task details follow project permissions.
- After a plugin is paused or executor authorization is revoked, it cannot continue to read new data, submit new results, or send externally.

## 10. Minimum Validation Scope for the Next Iteration

### 10.1 MVP Card

| Item | Definition |
|---|---|
| Target user | An individual who continuously advances one project across multiple AI conversations |
| User task | Record voice and text, continue making progress after switching conversations, and receive a daily review |
| Riskiest assumption | After raw records are processed, they can provide correct and evidence-backed background in new conversations and proactive services rather than adding information noise |
| P1 loop | Mobile app recording → on-device storage and automatic upload → configurable skill transcription → append transcription → question-based retrieval through MCP → cite records in a new conversation → scheduled review → view through the mobile Review entry point |
| Must include | Simple Android app, default private-project destination, recoverable upload queue, one real runner, one audio format supported by both the recorder and transcriber, two pinned-version skills, event and time triggers, prompt configuration, failure retry, enable/disable, citations, and permissions |
| Excludes | Image execution, external publishing, recommendation ranking, cross-project memory, third-party marketplace, general-purpose workflow canvas, parallel cluster, vector database |
| Effort boundary | First validate non-interactive execution and transcription capabilities with one real executor, then implement the loop above; if validation fails, resolve that blocker before expanding the framework. A specific calendar estimate has not yet been made |

Image analysis, reminders and recommendations, and external sharing are capabilities covered by the complete design but are not all required for this minimum loop.

### 10.2 Minimum Technical Slice

Reuse the existing Go service, project authorization, file events, HTTP/MCP, CLI, and project page. Implement a lightweight Android capture app in `app/android/` that provides recording, on-device saving, recoverable automatic uploads, and Records/Review views; the backend provides constrained audio upload, provenance-bearing derived results, a file-based task coordinator, one runner adapter, two skill packages, and a context retrieval endpoint that returns evidence. Retain the existing iOS prototype; narrow-screen web previews and testing on other platforms cannot substitute for validation of native Android capture.

For the first version of active retrieval, the backend performs deterministic candidate selection and the calling agent performs comprehension and expression; Daily Review is completed by the runner's skill. There is no need to wake another agent in the background for every retrieval.

### 10.3 Acceptance Evidence

Prepare a project with real recordings and text containing a goal, an early budget, an explicit budget change, a system suggestion that the user did not accept, a conflict from an unknown source, and an unrelated project. Write down the expected answer and sources before acceptance.

1. Complete start/stop recording in the real Android mobile app; no extra upload tap is required after stopping. The raw bytes are written to the device, and the downloaded hash matches after server submission. Stop a recording while offline, reopen the app, and restore connectivity; it uploads automatically. Repeated submissions create only one logical event. A transcription failure does not prevent access to the raw media.
2. After the user configures a prompt, a real runner generates a traceable transcription; after the prompt is changed and a rerun is explicitly initiated, the old result remains available and the new result becomes the default version for that processing stage.
3. A new conversation retrieves context through MCP, answers using the explicitly updated budget, identifies the unresolved conflict, does not treat the suggestion as a commitment, and cites the original content.
4. The actual scheduler wakes the runner at a short test time, writes the review back, and makes it appear in the in-product inbox; successful manual invocation cannot substitute for scheduled validation.
5. Simulating an interrupted task and duplicate triggers does not produce duplicate logical output; after pausing, the task no longer submits or delivers.
6. Another unauthorized identity cannot access project data through the original content, derived records, runs, indexes, or inbox.
7. Complete recording, automatic upload, viewing the original content/results, and viewing the daily review on a real Android phone; configure and pause rules on the web interface at both desktop and narrow widths. Cover microphone-permission denial, interrupted recording, expired login, queue recovery after restart, and lock-screen/background behavior permitted by Android; do more than inspect screenshots or a build. Emulator testing supplements but does not replace evidence from a real device.
8. In English, Simplified Chinese, Bahasa Melayu, and हिन्दी, complete registration or login from the public home page, enter a project, write and query a record, and then proceed through OAuth authorization confirmation. Refreshes, login, and cross-page navigation preserve the selected language; success, validation, empty, failure, and permission states do not fall back to another language. Complete at least one real desktop and narrow-screen interaction in each language; checking only translation files, a single OAuth page, or a successful build does not count as acceptance.

Also record raw-write latency, processing wait and execution time, retrieval latency, actual usage when available, and the number of user corrections. These are experimental data and do not claim that any particular performance level has been achieved in advance.

### 10.4 Stop and Learning Boundaries

After the loop above passes, first determine whether users repeat themselves less, trust the citations and reviews, and are willing to keep recording. If retrieval omissions or noisy suggestions remain unresolved, prioritize correcting those problems instead of adding more plugins as a substitute for validation.

## 11. Subsequent Decision Points

| Decision | When Needed | Current Treatment |
|---|---|---|
| Specific agent, speech tool, and account method | P1 capability validation | Adapt one real environment; do not promise that every subscription is supported |
| Mobile platform and recording capabilities | Android has been selected | Develop in a dedicated Android directory; test the recording format, lock-screen behavior, and upload recovery; retain the existing iOS prototype |
| Execution host and time zone | When enabling the runner and scheduled rules | User-selected; this document does not configure an actual machine |
| Image formats and analysis capabilities | Before beginning the image slice | Reuse the same rule and result contracts |
| External messaging channels and sharing destinations | Before enabling external delivery | Prioritize in-product delivery; external output defaults to a draft |
| Multi-user shared-automation policy | Before expanding team trials | Separate personal installations from shared installations managed by the creator |
| Retention, erasure, and account closure | Before broader hosting | Do not treat logical retraction as physical deletion |
| Pricing and resource responsibility | Before hosted execution | Prefer the user's own runner; no plan or billing commitment |

These decisions have explicit intervention points and do not block the current design or minimum validation. When defaults change, update both documents in sync.
