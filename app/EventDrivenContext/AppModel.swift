import CryptoKit
import Foundation
import SwiftUI

@MainActor
final class AppModel: ObservableObject {
    @Published var session: SessionCredential?
    @Published var endpoint = "https://context-api.integ.life"
    @Published var username = ""
    @Published var password = ""
    @Published var registerMode = false
    @Published var isBusy = false
    @Published var captureStarting = false
    @Published var projects: [APIProject] = []
    @Published var selectedProjectID = ""
    @Published var privateProjectID = ""
    @Published var captures: [CaptureItem] = []
    @Published var events: [APIEvent] = []
    @Published var inboxEntries: [APIInboxEntry] = []
    @Published var automationRuns: [APIAutomationRun] = []
    @Published var inboxSourceEvents: [String: [APIEvent]] = [:]
    @Published var inboxSourceLoading: Set<String> = []
    @Published var inboxSourceErrors: Set<String> = []
    @Published var activeCaptureID: String?
    @Published var recordingPaused = false
    @Published var recordingElapsed = 0
    @Published var noticeKey: String?

    let recording = RecordingController()
    private let queueStore: CaptureQueueStore
    private let secureStore = SecureSessionStore()
    private let network = NetworkMonitor()
    private var uploadRunning = false
    private var sessionGeneration = 0
    private var eventsRequestID = UUID()
    private var reviewRequestID = UUID()
    private var timer: Timer?
    private let isoFormatter: ISO8601DateFormatter = {
        let value = ISO8601DateFormatter()
        value.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return value
    }()

    init() {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("EventDrivenContext", isDirectory: true)
        self.queueStore = CaptureQueueStore(fileURL: support.appendingPathComponent("capture-queue.json"))
        recording.onUnexpectedStop = { [weak self] in
            Task { @MainActor in await self?.finishRecording(interrupted: true) }
        }
        network.onReachable = { [weak self] in
            Task { @MainActor in await self?.processQueue() }
        }
        Task { await bootstrap() }
    }

    var selectedProject: APIProject? { projects.first { $0.id == selectedProjectID } }

    func bootstrap() async {
        do {
            let interrupted = try await queueStore.recoverInterruptedRecordings()
            _ = try await queueStore.recoverUploadPhases()
            if interrupted > 0 { noticeKey = "notice_recovered_recording" }
        } catch { noticeKey = "error_queue_read" }

        guard let saved = secureStore.load() else { captures = []; return }
        endpoint = saved.endpoint
        username = saved.user.username
        guard saved.expiresAt > Date() else {
            secureStore.delete()
            noticeKey = "error_session_expired"
            return
        }
        sessionGeneration += 1
        session = saved
        await establishSession(saved, generation: sessionGeneration)
    }

    func authenticate() async {
        guard !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        let normalizedEndpoint = endpoint.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let normalizedUsername = username.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalizedUsername.isEmpty, !password.isEmpty else {
            noticeKey = "error_credentials_required"
            return
        }
        do {
            let publicClient = try APIClient(endpoint: normalizedEndpoint)
            if registerMode { _ = try await publicClient.register(username: normalizedUsername, password: password) }
            let response = try await publicClient.login(username: normalizedUsername, password: password)
            let saved = SessionCredential(endpoint: normalizedEndpoint, user: response.user, token: response.token, expiresAt: response.expiresAt)
            try secureStore.save(saved)
            sessionGeneration += 1
            session = saved
            endpoint = normalizedEndpoint
            username = normalizedUsername
            password = ""
            await establishSession(saved, generation: sessionGeneration)
        } catch let error as APIClientError {
            if case .server(let status, _) = error, status == 401 {
                noticeKey = "error_invalid_credentials"
            } else if registerMode, case .server(let status, _) = error, status == 409 {
                noticeKey = "error_username_exists"
            } else {
                noticeKey = localizationKey(for: error)
            }
        } catch { noticeKey = "error_unknown" }
    }

    private func establishSession(_ expected: SessionCredential, generation: Int) async {
        await loadProjectsAndDefault(expected, generation: generation)
        guard isCurrent(expected, generation: generation) else { return }
        await reloadCaptures()
        await refreshEvents()
        await refreshReview()
        await processQueue()
    }

    func logout() async {
        guard !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        if activeCaptureID != nil { _ = await finishRecording(interrupted: true) }
        let previous = session
        sessionGeneration += 1
        session = nil
        projects = []
        events = []
        inboxEntries = []
        automationRuns = []
        clearInboxSources()
        captures = []
        selectedProjectID = ""
        privateProjectID = ""
        password = ""
        secureStore.delete()
        guard let previous else { return }
        await markPendingWaitingForAuth(session: previous)
        if let client = try? APIClient(endpoint: previous.endpoint, token: previous.token) { try? await client.logout() }
    }

    private func loadProjectsAndDefault(_ expected: SessionCredential, generation: Int) async {
        do {
            let client = try APIClient(endpoint: expected.endpoint, token: expected.token)
            var loaded = try await client.projects()
            guard isCurrent(expected, generation: generation) else { return }
            var privateProject: APIProject?
            for project in loaded where project.name == "我的记录" && project.ownerUserID == expected.user.id {
                let members = try await client.members(projectID: project.id)
                guard isCurrent(expected, generation: generation) else { return }
                if members.count == 1, members.first?.id == expected.user.id { privateProject = project; break }
            }
            if privateProject == nil {
                let created = try await client.createProject(name: "我的记录", description: "")
                guard isCurrent(expected, generation: generation) else { return }
                let members = try await client.members(projectID: created.id)
                guard isCurrent(expected, generation: generation), members.count == 1, members.first?.id == expected.user.id else {
                    throw APIClientError.invalidResponse
                }
                loaded.append(created)
                privateProject = created
            }
            projects = loaded
            privateProjectID = privateProject?.id ?? ""
            let saved = UserDefaults.standard.string(forKey: projectPreferenceKey(session: expected))
            selectedProjectID = saved.flatMap { id in loaded.contains(where: { $0.id == id }) ? id : nil } ?? privateProjectID
        } catch {
            guard isCurrent(expected, generation: generation) else { return }
            noticeKey = localizationKey(for: error)
        }
    }

    func chooseProject(_ id: String) async {
        guard !isBusy, activeCaptureID == nil, !captureStarting, let session,
              projects.contains(where: { $0.id == id }) else { return }
        selectedProjectID = id
        events = []
        inboxEntries = []
        automationRuns = []
        clearInboxSources()
        UserDefaults.standard.set(id, forKey: projectPreferenceKey(session: session))
        await refreshEvents()
        await refreshReview()
    }

    func startRecording() async {
        guard !isBusy, !captureStarting, activeCaptureID == nil,
              let expectedSession = session, let expectedProject = selectedProject else {
            if selectedProject == nil { noticeKey = "error_project_required" }
            return
        }
        captureStarting = true
        defer { captureStarting = false }
        guard await RecordingController.requestPermission() else { noticeKey = "error_microphone_denied"; return }
        guard session == expectedSession, selectedProjectID == expectedProject.id, activeCaptureID == nil else { return }

        let captureID = "capture_\(UUID().uuidString.lowercased())"
        let occurredAt = isoFormatter.string(from: Date())
        let metadata = ["capture_id": captureID, "media_type": "audio/mp4", "source": "phone.recorder"]
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        guard let metadataData = try? encoder.encode(metadata), let metadataJSON = String(data: metadataData, encoding: .utf8) else {
            noticeKey = "error_queue_write"; return
        }
        do {
            let fileURL = try captureDirectory(accountID: expectedSession.user.id).appendingPathComponent("\(captureID).m4a")
            let item = CaptureItem(captureID: captureID, accountID: expectedSession.user.id, endpoint: expectedSession.endpoint,
                                   projectID: expectedProject.id, projectName: expectedProject.name, filePath: fileURL.path,
                                   occurredAt: occurredAt, metadataJSON: metadataJSON)
            try await queueStore.upsert(item)
            guard session == expectedSession, selectedProjectID == expectedProject.id,
                  activeCaptureID == nil else {
                _ = try? await queueStore.removeUnstarted(id: captureID)
                await reloadCaptures()
                return
            }
            try recording.start(at: fileURL)
            activeCaptureID = captureID
            recordingPaused = false
            recordingElapsed = 0
            startTimer()
            await reloadCaptures()
        } catch {
            _ = try? await queueStore.update(id: captureID) { $0.status = .failed; $0.errorCode = "recording_start_failed" }
            await reloadCaptures()
            noticeKey = "error_recording_start"
        }
    }

    func togglePause() {
        guard activeCaptureID != nil else { return }
        recordingPaused.toggle()
        if recordingPaused { recording.pause() } else { recording.resume() }
    }

    func stopRecording() async { _ = await finishRecording(interrupted: false) }
    func cancelRecording() async {
        if await finishRecording(interrupted: true) {
            noticeKey = "notice_cancelled_recording_saved"
        }
    }

    @discardableResult
    func finishRecording(interrupted: Bool) async -> Bool {
        guard let captureID = activeCaptureID else { return false }
        activeCaptureID = nil
        timer?.invalidate()
        timer = nil
        recording.stop()
        guard let queued = await queueStore.item(id: captureID) else {
            noticeKey = "error_queue_read"
            return false
        }
        let url = URL(fileURLWithPath: queued.filePath)
        do {
            let attributes = try FileManager.default.attributesOfItem(atPath: queued.filePath)
            let size = (attributes[.size] as? NSNumber)?.int64Value ?? 0
            guard size > 0 else { throw APIClientError.invalidResponse }
            let digest = try sha256(of: url)
            try FileManager.default.setAttributes(
                [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication],
                ofItemAtPath: queued.filePath
            )
            guard try await queueStore.update(id: captureID, {
                $0.sizeBytes = size; $0.sha256 = digest
                $0.status = interrupted ? .interrupted : .localSaved
                $0.errorCode = interrupted ? "recording_interrupted" : nil
            }) != nil else { throw APIClientError.invalidResponse }
            await reloadCaptures()
            guard !interrupted else {
                noticeKey = "notice_interrupted_recording"
                return true
            }
            guard try await queueStore.update(id: captureID, { $0.status = .queued }) != nil else {
                throw APIClientError.invalidResponse
            }
            await reloadCaptures()
            await processQueue()
            return true
        } catch {
            _ = try? await queueStore.update(id: captureID) { $0.status = .failed; $0.errorCode = "local_file_unreadable" }
            await reloadCaptures()
            noticeKey = "error_local_file"
            return false
        }
    }

    func processQueue() async {
        guard !uploadRunning, let expected = session else { return }
        let generation = sessionGeneration
        guard expected.expiresAt > Date() else { await expireSession(expected, generation: generation); return }
        uploadRunning = true
        let scoped = await queueStore.items(endpoint: expected.endpoint, accountID: expected.user.id)
        for item in scoped {
            guard isCurrent(expected, generation: generation) else { break }
            let uploadable: Set<CaptureStatus> = [.localSaved, .queued, .uploading, .failed, .waitingNetwork, .waitingAuth]
            guard uploadable.contains(item.status), item.canRetry, item.nextAttemptAt == nil || item.nextAttemptAt! <= Date() else { continue }
            guard network.isReachable else {
                _ = try? await queueStore.update(id: item.captureID) { $0.status = .waitingNetwork }
                continue
            }
            do {
                try validateLocalFile(item)
                _ = try await queueStore.update(id: item.captureID) { $0.status = .uploading; $0.errorCode = nil }
                guard isCurrent(expected, generation: generation) else { break }
                await reloadCaptures()
                let event = try await APIClient(endpoint: expected.endpoint, token: expected.token).upload(item)
                guard isCurrent(expected, generation: generation) else { break }
                try validateConfirmation(event, for: item)
                _ = try await queueStore.update(id: item.captureID) {
                    $0.eventID = event.id; $0.remoteFileID = event.content.file?.id; $0.nextAttemptAt = nil; $0.errorCode = nil
                    $0.status = .synced
                }
            } catch let error as APIClientError {
                guard isCurrent(expected, generation: generation) else { break }
                if case .server(let status, _) = error, status == 401 {
                    _ = try? await queueStore.update(id: item.captureID) { $0.status = .waitingAuth; $0.errorCode = "unauthenticated" }
                    await expireSession(expected, generation: generation)
                    break
                }
                let permanent = isPermanent(error)
                let attempt = item.attemptCount + 1
                let delay = min(pow(2.0, Double(attempt)), 900.0)
                let code = errorCode(for: error)
                _ = try? await queueStore.update(id: item.captureID) {
                    $0.status = .failed; $0.attemptCount = attempt
                    $0.nextAttemptAt = permanent ? nil : Date().addingTimeInterval(delay)
                    $0.errorCode = code
                }
                if !permanent { scheduleRetry(after: delay) }
            } catch {
                guard isCurrent(expected, generation: generation) else { break }
                _ = try? await queueStore.update(id: item.captureID) { $0.status = .failed; $0.nextAttemptAt = nil; $0.errorCode = "local_file_changed" }
            }
            await reloadCaptures()
        }
        uploadRunning = false
        if isCurrent(expected, generation: generation) {
            await reloadCaptures()
            if network.isReachable {
                await refreshEvents()
                await refreshReview()
            }
        }
        else { await processQueue() }
    }

    func retry(_ item: CaptureItem) async {
        guard item.canRetry else { return }
        guard let session, session.user.id == item.accountID, session.endpoint == item.endpoint else {
            noticeKey = "error_original_account_required"; return
        }
        do {
            _ = try await queueStore.update(id: item.captureID) { $0.status = .queued; $0.nextAttemptAt = nil; $0.errorCode = nil }
            await reloadCaptures()
            await processQueue()
        } catch { noticeKey = "error_queue_write" }
    }

    func refreshEvents() async {
        guard let expected = session, !selectedProjectID.isEmpty else { return }
        let generation = sessionGeneration
        let projectID = selectedProjectID
        let requestID = UUID()
        eventsRequestID = requestID
        do {
            let loaded = try await APIClient(endpoint: expected.endpoint, token: expected.token).events(projectID: projectID)
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID, eventsRequestID == requestID else { return }
            events = loaded
        } catch let error as APIClientError {
            guard isCurrent(expected, generation: generation), eventsRequestID == requestID else { return }
            if case .server(let status, _) = error, status == 401 { await expireSession(expected, generation: generation) }
            else { noticeKey = localizationKey(for: error) }
        } catch {
            guard isCurrent(expected, generation: generation), eventsRequestID == requestID else { return }
            noticeKey = "error_network"
        }
    }

    func refreshReview() async {
        guard let expected = session, !selectedProjectID.isEmpty else {
            inboxEntries = []
            automationRuns = []
            return
        }
        let generation = sessionGeneration
        let projectID = selectedProjectID
        let requestID = UUID()
        reviewRequestID = requestID
        do {
            let client = try APIClient(endpoint: expected.endpoint, token: expected.token)
            let loadedInbox = try await client.inbox()
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID, reviewRequestID == requestID else { return }
            let loadedRuns = try await client.automationRuns(projectID: projectID)
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID, reviewRequestID == requestID else { return }
            inboxEntries = loadedInbox.filter { $0.projectID == projectID && $0.recipientUserID == expected.user.id }
            automationRuns = loadedRuns.filter { $0.projectID == projectID }
            let visibleEntryIDs = Set(inboxEntries.map(\.id))
            inboxSourceEvents = inboxSourceEvents.filter { visibleEntryIDs.contains($0.key) }
            inboxSourceErrors = inboxSourceErrors.intersection(visibleEntryIDs)
            inboxSourceLoading = inboxSourceLoading.intersection(visibleEntryIDs)
            await reconcileCaptures(with: automationRuns, session: expected, generation: generation)
        } catch let error as APIClientError {
            guard isCurrent(expected, generation: generation), reviewRequestID == requestID else { return }
            if case .server(let status, _) = error, status == 401 { await expireSession(expected, generation: generation) }
            else { noticeKey = localizationKey(for: error) }
        } catch {
            guard isCurrent(expected, generation: generation), reviewRequestID == requestID else { return }
            noticeKey = "error_network"
        }
    }

    func markInboxRead(_ entry: APIInboxEntry) async {
        guard entry.readAt == nil, let expected = session, entry.projectID == selectedProjectID else { return }
        let generation = sessionGeneration
        let projectID = selectedProjectID
        do {
            let updated = try await APIClient(endpoint: expected.endpoint, token: expected.token).markInboxRead(entryID: entry.id)
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID,
                  updated.id == entry.id, updated.projectID == projectID, updated.recipientUserID == expected.user.id else { return }
            if let index = inboxEntries.firstIndex(where: { $0.id == updated.id }) { inboxEntries[index] = updated }
        } catch let error as APIClientError {
            guard isCurrent(expected, generation: generation) else { return }
            if case .server(let status, _) = error, status == 401 { await expireSession(expected, generation: generation) }
            else { noticeKey = localizationKey(for: error) }
        } catch {
            guard isCurrent(expected, generation: generation) else { return }
            noticeKey = "error_network"
        }
    }

    func processingRun(for event: APIEvent) -> APIAutomationRun? {
        latestAudioRun(for: event.id, in: automationRuns)
    }

    func loadInboxSources(_ entry: APIInboxEntry) async {
        guard inboxSourceEvents[entry.id] == nil, !inboxSourceLoading.contains(entry.id),
              let expected = session, entry.projectID == selectedProjectID, entry.recipientUserID == expected.user.id else { return }
        let generation = sessionGeneration
        let projectID = selectedProjectID
        inboxSourceLoading.insert(entry.id)
        inboxSourceErrors.remove(entry.id)
        var queue = entry.candidate.sourceEventIDs.map { ($0, 0) }
        for item in entry.candidate.items ?? [] {
            queue.append(contentsOf: item.sourceEventIDs.map { ($0, 0) })
        }
        var visited: Set<String> = []
        var loaded: [APIEvent] = []
        do {
            let client = try APIClient(endpoint: expected.endpoint, token: expected.token)
            while !queue.isEmpty && visited.count < 128 {
                let (eventID, depth) = queue.removeFirst()
                guard !visited.contains(eventID) else { continue }
                visited.insert(eventID)
                let event = try await client.event(id: eventID)
                guard isCurrent(expected, generation: generation), selectedProjectID == projectID,
                      entry.recipientUserID == expected.user.id else { return }
                guard event.projectID == projectID else { throw APIClientError.invalidResponse }
                loaded.append(event)
                if depth < 8 {
                    queue.append(contentsOf: (event.provenance.sourceEventIDs ?? []).map { ($0, depth + 1) })
                }
            }
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            inboxSourceEvents[entry.id] = loaded
            inboxSourceLoading.remove(entry.id)
        } catch let error as APIClientError {
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            inboxSourceLoading.remove(entry.id)
            inboxSourceErrors.insert(entry.id)
            if case .server(let status, _) = error, status == 401 { await expireSession(expected, generation: generation) }
        } catch {
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            inboxSourceLoading.remove(entry.id)
            inboxSourceErrors.insert(entry.id)
        }
    }

    func retryInboxSources(_ entry: APIInboxEntry) async {
        inboxSourceEvents[entry.id] = nil
        inboxSourceErrors.remove(entry.id)
        await loadInboxSources(entry)
    }

    func playLocal(_ item: CaptureItem) {
        guard session?.endpoint == item.endpoint, session?.user.id == item.accountID else { noticeKey = "error_original_account_required"; return }
        do { try recording.play(url: URL(fileURLWithPath: item.filePath)) }
        catch { noticeKey = "error_audio_playback" }
    }

    func playRemote(_ event: APIEvent) async {
        guard let expected = session, let file = event.content.file,
              !selectedProjectID.isEmpty, event.projectID == selectedProjectID else { return }
        let generation = sessionGeneration
        let projectID = selectedProjectID
        if let capture = captures.first(where: { $0.eventID == event.id }), FileManager.default.fileExists(atPath: capture.filePath) {
            playLocal(capture); return
        }
        do {
            let data = try await APIClient(endpoint: expected.endpoint, token: expected.token).download(fileID: file.id)
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
            guard data.count == file.sizeBytes, digest == file.sha256 else { throw APIClientError.invalidResponse }
            let directory = try remoteAudioDirectory(session: expected)
            let fileExtension = URL(fileURLWithPath: file.filename).pathExtension
            let url = directory.appendingPathComponent("\(file.id).\(fileExtension.isEmpty ? "m4a" : fileExtension)")
            try data.write(to: url, options: .atomic)
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            try recording.play(url: url)
        } catch {
            guard isCurrent(expected, generation: generation), selectedProjectID == projectID else { return }
            noticeKey = localizationKey(for: error)
        }
    }

    private func markPendingWaitingForAuth(session: SessionCredential) async {
        for item in await queueStore.items(endpoint: session.endpoint, accountID: session.user.id) {
            guard [.localSaved, .queued, .uploading, .waitingNetwork, .failed].contains(item.status) else { continue }
            _ = try? await queueStore.update(id: item.captureID) { $0.status = .waitingAuth; $0.errorCode = "unauthenticated" }
        }
        await reloadCaptures()
    }

    private func expireSession(_ expected: SessionCredential, generation: Int) async {
        guard isCurrent(expected, generation: generation) else { return }
        isBusy = true
        defer { isBusy = false }
        if activeCaptureID != nil { _ = await finishRecording(interrupted: true) }
        guard isCurrent(expected, generation: generation) else { return }
        sessionGeneration += 1
        session = nil
        projects = []
        events = []
        inboxEntries = []
        automationRuns = []
        clearInboxSources()
        captures = []
        secureStore.delete()
        await markPendingWaitingForAuth(session: expected)
        noticeKey = "error_session_expired"
    }

    private func reloadCaptures() async {
        guard let expected = session else { captures = []; return }
        let generation = sessionGeneration
        let loaded = await queueStore.items(endpoint: expected.endpoint, accountID: expected.user.id)
        guard isCurrent(expected, generation: generation) else { return }
        captures = loaded
    }

    private func reconcileCaptures(with runs: [APIAutomationRun], session expected: SessionCredential, generation: Int) async {
        let scoped = await queueStore.items(endpoint: expected.endpoint, accountID: expected.user.id)
        guard isCurrent(expected, generation: generation) else { return }
        for item in scoped {
            guard isCurrent(expected, generation: generation), let eventID = item.eventID,
                  let run = latestAudioRun(for: eventID, in: runs),
                  let status = captureStatus(for: run), status != item.status else { continue }
            _ = try? await queueStore.update(id: item.captureID) {
                $0.status = status
                $0.errorCode = status == .processingFailed ? (run.failureCode ?? run.failureKind ?? "processing_failed") : nil
            }
            guard isCurrent(expected, generation: generation) else { return }
        }
        await reloadCaptures()
    }

    private func captureStatus(for run: APIAutomationRun) -> CaptureStatus? {
        switch run.status {
        case "queued", "leased", "running", "candidate_saved", "retry_wait": return .processing
        case "blocked_auth", "blocked_capability", "failed": return .processingFailed
        case "succeeded":
            let hasOutput = !(run.outputEventID ?? "").isEmpty || !(run.outputEventIDs ?? []).isEmpty
            return hasOutput && (run.noOutputReason ?? "").isEmpty ? .ready : .synced
        case "cancelled", "skipped": return .synced
        default: return nil
        }
    }

    private func latestAudioRun(for eventID: String, in runs: [APIAutomationRun]) -> APIAutomationRun? {
        runs.filter { run in
            run.skillID == "audio-transcribe" && run.inputs.contains { $0.eventID == eventID }
        }.max { lhs, rhs in
            if lhs.generation != rhs.generation { return lhs.generation < rhs.generation }
            if lhs.createdAt != rhs.createdAt { return lhs.createdAt < rhs.createdAt }
            return lhs.id < rhs.id
        }
    }

    private func clearInboxSources() {
        inboxSourceEvents = [:]
        inboxSourceLoading = []
        inboxSourceErrors = []
    }

    private func validateLocalFile(_ item: CaptureItem) throws {
        let attributes = try FileManager.default.attributesOfItem(atPath: item.filePath)
        let size = (attributes[.size] as? NSNumber)?.int64Value ?? -1
        guard size == item.sizeBytes, !item.sha256.isEmpty,
              try sha256(of: URL(fileURLWithPath: item.filePath)) == item.sha256 else {
            throw APIClientError.server(status: 0, code: "local_file_changed")
        }
    }

    private func validateConfirmation(_ event: APIEvent, for item: CaptureItem) throws {
        guard event.projectID == item.projectID, event.actorUserID == item.accountID, event.content.kind == "file",
              let file = event.content.file, Int64(file.sizeBytes) == item.sizeBytes, file.sha256 == item.sha256 else {
            throw APIClientError.server(status: 0, code: "confirmation_mismatch")
        }
    }

    private func startTimer() {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self, self.activeCaptureID != nil else { return }
                if !self.recordingPaused { self.recordingElapsed += 1 }
                if self.recordingElapsed >= 600 { self.noticeKey = "notice_recording_limit"; await self.stopRecording() }
            }
        }
    }

    private func scheduleRetry(after delay: TimeInterval) {
        Task { [weak self] in try? await Task.sleep(for: .seconds(delay)); await self?.processQueue() }
    }

    private func captureDirectory(accountID: String) throws -> URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("EventDrivenContext/Captures", isDirectory: true).appendingPathComponent(accountID, isDirectory: true)
        try FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base
    }

    private func remoteAudioDirectory(session: SessionCredential) throws -> URL {
        let endpointHash = SHA256.hash(data: Data(session.endpoint.utf8)).prefix(8).map { String(format: "%02x", $0) }.joined()
        let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("EventDrivenContext/RemoteAudio", isDirectory: true)
            .appendingPathComponent(endpointHash, isDirectory: true).appendingPathComponent(session.user.id, isDirectory: true)
        try FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base
    }

    private func projectPreferenceKey(session: SessionCredential) -> String { "selected_project.\(session.endpoint).\(session.user.id)" }
    private func isCurrent(_ expected: SessionCredential, generation: Int) -> Bool { session == expected && sessionGeneration == generation }
    private func localizationKey(for error: Error) -> String { (error as? APIClientError)?.localizationKey ?? "error_unknown" }
    private func errorCode(for error: APIClientError) -> String {
        if case .server(_, let code) = error { return code }
        switch error { case .transport: return "network_error"; case .invalidEndpoint: return "invalid_endpoint"; case .invalidResponse: return "invalid_response"; case .server: return "server_error" }
    }
    private func isPermanent(_ error: APIClientError) -> Bool {
        if case .server(let status, let code) = error {
            return [400, 403, 409, 413].contains(status) || ["local_file_changed", "confirmation_mismatch"].contains(code)
        }
        if case .invalidResponse = error { return true }
        return false
    }
}
