import SwiftUI

struct RootView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        Group {
            if model.session == nil { AuthView() }
            else { MainTabsView() }
        }
        .alert(
            Text(LocalizedStringKey(model.noticeKey ?? "error_unknown")),
            isPresented: Binding(
                get: { model.noticeKey != nil },
                set: { if !$0 { model.noticeKey = nil } }
            )
        ) { Button("ok", role: .cancel) { model.noticeKey = nil } }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { Task { await model.processQueue() } }
        }
    }
}

struct AuthView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("app_locale") private var locale = "en"

    var body: some View {
        NavigationStack {
            Form {
                Section("server") {
                    TextField("https_endpoint", text: $model.endpoint)
                        .textInputAutocapitalization(.never)
                        .keyboardType(.URL)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("endpoint-field")
                    Text("https_explanation").font(.footnote).foregroundStyle(.secondary)
                }
                Section {
                    TextField("username", text: $model.username)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("username-field")
                    if model.registerMode {
                        TextField("email", text: $model.email)
                            .textInputAutocapitalization(.never)
                            .keyboardType(.emailAddress)
                            .textContentType(.emailAddress)
                            .autocorrectionDisabled()
                            .accessibilityIdentifier("email-field")
                    }
                    SecureField("password", text: $model.password)
                        .accessibilityIdentifier("password-field")
                    Toggle("create_account", isOn: $model.registerMode)
                    Button { Task { await model.authenticate() } }
                    label: { Text(LocalizedStringKey(model.registerMode ? "register_and_continue" : "login")) }
                    .disabled(model.isBusy)
                    .accessibilityIdentifier("login-button")
                } header: { Text(LocalizedStringKey(model.registerMode ? "register" : "login")) }
                LanguageSection(locale: $locale)
            }
            .navigationTitle("app_name")
        }
    }
}

struct MainTabsView: View {
    var body: some View {
        TabView {
            RecordsView().tabItem { Label("records", systemImage: "waveform.circle.fill") }
            ReviewView().tabItem { Label("review", systemImage: "text.page") }
            ProfileView().tabItem { Label("profile", systemImage: "person.circle") }
        }
    }
}

struct RecordsView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
            List {
                Section {
                    VStack(spacing: 14) {
                        HStack {
                            Label("recording_destination", systemImage: "lock.shield")
                            Spacer()
                            Text(model.selectedProject?.name ?? "—")
                                .foregroundStyle(.secondary)
                                .accessibilityIdentifier("selected-project")
                        }
                        if model.activeCaptureID == nil {
                            Button {
                                Task { await model.startRecording() }
                            } label: {
                                Label {
                                    Text(LocalizedStringKey(model.captureStarting ? "requesting_permission" : "start_recording"))
                                } icon: { Image(systemName: "mic.circle.fill") }
                                    .font(.title3).frame(maxWidth: .infinity).padding(.vertical, 18)
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(model.isBusy || model.captureStarting || model.selectedProject == nil)
                            .accessibilityIdentifier("start-recording-button")
                            Text("recording_limit_note").font(.footnote).foregroundStyle(.secondary)
                        } else {
                            Text(duration(model.recordingElapsed)).font(.system(.largeTitle, design: .monospaced)).monospacedDigit()
                            Text(LocalizedStringKey(model.recordingPaused ? "capture_status_paused" : "capture_status_recording"))
                                .foregroundStyle(model.recordingPaused ? .orange : .red)
                                .accessibilityAddTraits(.updatesFrequently)
                            HStack {
                                Button { model.togglePause() }
                                label: { Text(LocalizedStringKey(model.recordingPaused ? "resume" : "pause")) }
                                    .buttonStyle(.bordered)
                                Button("finish") { Task { await model.stopRecording() } }
                                    .buttonStyle(.borderedProminent)
                                    .accessibilityIdentifier("finish-recording-button")
                                Button("cancel", role: .destructive) { Task { await model.cancelRecording() } }
                                    .buttonStyle(.bordered)
                            }
                        }
                    }
                    .padding(.vertical, 8)
                }

                Section("on_this_phone") {
                    if model.captures.isEmpty {
                        Text("no_local_recordings").foregroundStyle(.secondary)
                    } else {
                        ForEach(model.captures) { item in CaptureRow(item: item) }
                    }
                }

                Section {
                    if model.events.isEmpty {
                        Text("no_server_records").foregroundStyle(.secondary)
                    } else {
                        ForEach(model.events) { event in EventRow(event: event) }
                    }
                } header: {
                    HStack {
                        Text("server_records")
                        Spacer()
                        Button { Task { await model.refreshEvents() } } label: { Image(systemName: "arrow.clockwise") }
                            .accessibilityLabel("refresh")
                    }
                }
            }
            .navigationTitle("records")
        }
    }

    private func duration(_ seconds: Int) -> String {
        String(format: "%02d:%02d", seconds / 60, seconds % 60)
    }
}

private struct CaptureRow: View {
    @EnvironmentObject private var model: AppModel
    let item: CaptureItem

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack {
                Text(item.createdAt, format: .dateTime.month().day().hour().minute())
                Spacer()
                Text(LocalizedStringKey(item.status.localizationKey))
                    .font(.caption).foregroundStyle(statusColor)
            }
            Text(item.projectName).font(.caption).foregroundStyle(.secondary)
            if localFileAvailable {
                Text("saved_on_phone").font(.caption).foregroundStyle(.secondary)
            }
            if item.status == .synced {
                Text("synced_processing_unknown").font(.caption).foregroundStyle(.secondary)
            }
            if let errorKey {
                Text(LocalizedStringKey(errorKey)).font(.caption).foregroundStyle(.red)
            }
            HStack {
                Button { model.playLocal(item) } label: { Label("play_original", systemImage: "play.fill") }
                    .buttonStyle(.borderless)
                    .disabled(!localFileAvailable)
                if item.canRetry && item.status != .queued && item.status != .uploading {
                    Button("retry") { Task { await model.retry(item) } }.buttonStyle(.borderless)
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("capture-row")
        .accessibilityValue(item.status.rawValue)
    }

    private var statusColor: Color {
        switch item.status {
        case .failed, .interrupted, .processingFailed: return .red
        case .waitingAuth, .waitingNetwork: return .orange
        case .uploading, .processing: return .blue
        case .synced, .ready: return .green
        default: return .secondary
        }
    }

    private var localFileAvailable: Bool {
        item.sizeBytes > 0 && !item.sha256.isEmpty && FileManager.default.fileExists(atPath: item.filePath)
    }

    private var errorKey: String? {
        switch item.errorCode {
        case "unauthenticated": return "error_session_expired"
        case "forbidden": return "error_project_access"
        case "conflict": return "error_conflict"
        case "too_large", "request_too_large": return "error_file_too_large"
        case "local_file_changed": return "error_local_file_changed"
        case "local_file_unreadable": return "error_local_file"
        case "confirmation_mismatch", "invalid_response": return "error_invalid_response"
        case "network_error": return "error_network"
        case "recording_start_failed": return "error_recording_start"
        case .some: return "error_server"
        case nil: return nil
        }
    }
}

private struct EventRow: View {
    @EnvironmentObject private var model: AppModel
    let event: APIEvent

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            if let file = event.content.file {
                Text(file.filename)
                    .lineLimit(1)
                    .accessibilityIdentifier("server-file-event")
                Text(event.actorUsername).font(.caption).foregroundStyle(.secondary)
                if let run = model.processingRun(for: event) {
                    Text(LocalizedStringKey("server_processing_\(run.status)"))
                        .font(.caption).foregroundStyle(.blue)
                } else {
                    Text("synced_processing_unknown").font(.caption).foregroundStyle(.secondary)
                }
                Button { Task { await model.playRemote(event) } } label: { Label("play_original", systemImage: "play.fill") }
                    .buttonStyle(.borderless)
            } else if let text = event.content.text {
                Text(text).lineLimit(4)
            }
        }
    }
}

struct ReviewView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if model.inboxEntries.isEmpty {
                        Text("inbox_empty").foregroundStyle(.secondary)
                    } else {
                        ForEach(model.inboxEntries) { entry in InboxRow(entry: entry) }
                    }
                } header: {
                    HStack {
                        Text("inbox")
                        Spacer()
                        Button { Task { await model.refreshReview() } } label: { Image(systemName: "arrow.clockwise") }
                            .accessibilityLabel("refresh")
                    }
                }

                Section("processing_runs") {
                    if model.automationRuns.isEmpty {
                        Text("processing_empty").foregroundStyle(.secondary)
                    } else {
                        ForEach(model.automationRuns.prefix(20)) { run in AutomationRunRow(run: run) }
                    }
                }
            }
                .navigationTitle("review")
        }
    }
}

private struct InboxRow: View {
    @EnvironmentObject private var model: AppModel
    let entry: APIInboxEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(LocalizedStringKey("candidate_kind_\(entry.candidate.kind)"))
                    .font(.headline)
                Spacer()
                if entry.readAt == nil {
                    Text("unread").font(.caption).foregroundStyle(.blue)
                }
            }
            Text(entry.candidate.text)
            ForEach(Array((entry.candidate.items ?? []).enumerated()), id: \.offset) { _, item in
                VStack(alignment: .leading, spacing: 2) {
                    Text(LocalizedStringKey("candidate_item_\(item.kind)"))
                        .font(.caption).foregroundStyle(.secondary)
                    Text(item.text).font(.callout)
                }
            }
            DisclosureGroup(isExpanded: $sourcesExpanded) {
                if model.inboxSourceLoading.contains(entry.id) {
                    ProgressView("source_loading")
                } else if model.inboxSourceErrors.contains(entry.id) {
                    Text("source_load_failed").font(.caption).foregroundStyle(.red)
                    Button("retry") { Task { await model.retryInboxSources(entry) } }
                        .buttonStyle(.borderless)
                } else if let sources = model.inboxSourceEvents[entry.id] {
                    if sources.isEmpty {
                        Text("source_empty").font(.caption).foregroundStyle(.secondary)
                    } else {
                        ForEach(sources) { source in InboxSourceRow(event: source) }
                    }
                }
            } label: {
                Text("sources")
            }
            .onChange(of: sourcesExpanded) { _, expanded in
                if expanded { Task { await model.loadInboxSources(entry) } }
            }
            Text(entry.createdAt).font(.caption2).foregroundStyle(.secondary)
            if entry.readAt == nil {
                Button("mark_read") { Task { await model.markInboxRead(entry) } }
                    .buttonStyle(.borderless)
            }
        }
    }

    @State private var sourcesExpanded = false
}

private struct InboxSourceRow: View {
    @EnvironmentObject private var model: AppModel
    let event: APIEvent

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(LocalizedStringKey("candidate_kind_\(event.provenance.kind)"))
                .font(.caption).foregroundStyle(.secondary)
            if let text = event.content.text {
                Text(text).font(.callout)
            } else if let file = event.content.file {
                Text(file.filename).font(.callout)
                if file.mediaType.hasPrefix("audio/") {
                    Button { Task { await model.playRemote(event) } } label: {
                        Label("play_original", systemImage: "play.fill")
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
        .padding(.vertical, 3)
    }
}

private struct AutomationRunRow: View {
    let run: APIAutomationRun

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack {
                Text(run.skillID).font(.callout)
                Spacer()
                Text(LocalizedStringKey("server_processing_\(run.status)"))
                    .font(.caption).foregroundStyle(runColor)
            }
            if let detailKey {
                Text(LocalizedStringKey(detailKey)).font(.caption)
                    .foregroundStyle(run.status.hasPrefix("blocked_") || run.status == "failed" ? .red : .secondary)
            }
            if let diagnostic {
                Text(verbatim: diagnostic).font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private var runColor: Color {
        switch run.status {
        case "failed", "blocked_auth", "blocked_capability", "cancelled": return .red
        case "succeeded", "skipped": return .green
        default: return .blue
        }
    }

    private var detailKey: String? {
        if !(run.noOutputReason ?? "").isEmpty { return "processing_detail_no_output" }
        switch run.status {
        case "blocked_auth": return "processing_detail_blocked_auth"
        case "blocked_capability": return "processing_detail_blocked_capability"
        case "failed": return "processing_detail_failed"
        default: return nil
        }
    }

    private var diagnostic: String? {
        let value = run.failureCode ?? run.failureKind ?? run.noOutputReason
        return value?.isEmpty == false ? value : nil
    }
}

struct ProfileView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("app_locale") private var locale = "en"

    var body: some View {
        NavigationStack {
            Form {
                Section("account") {
                    LabeledContent("username", value: model.session?.user.username ?? "")
                    LabeledContent("server", value: model.session?.endpoint ?? "")
                }
                Section("recording_destination") {
                    Picker("project", selection: Binding(
                        get: { model.selectedProjectID },
                        set: { id in Task { await model.chooseProject(id) } }
                    )) {
                        ForEach(model.projects) { project in
                            HStack {
                                Text(project.name)
                                if project.id == model.privateProjectID { Text("private_project_marker") }
                            }.tag(project.id)
                        }
                    }
                    .disabled(model.isBusy || model.activeCaptureID != nil || model.captureStarting)
                    Text("shared_project_warning").font(.footnote).foregroundStyle(.secondary)
                }
                LanguageSection(locale: $locale)
                Section {
                    Button("retry_pending") { Task { await model.processQueue() } }
                    Button("logout", role: .destructive) { Task { await model.logout() } }
                        .disabled(model.isBusy)
                        .accessibilityIdentifier("logout-button")
                }
            }
            .navigationTitle("profile")
        }
    }
}

private struct LanguageSection: View {
    @Binding var locale: String
    var body: some View {
        Section("language") {
            Picker("language", selection: $locale) {
                Text("English").tag("en")
                Text("简体中文").tag("zh-Hans")
                Text("Bahasa Melayu").tag("ms")
                Text("हिन्दी").tag("hi")
            }
        }
    }
}
