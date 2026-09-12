import Foundation
import Testing
@testable import EventDrivenContextCore

struct CaptureQueueStoreTests {
    private func temporaryURL() throws -> URL {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        return directory.appendingPathComponent("queue.json")
    }

    private func item(
        account: String = "usr_a",
        endpoint: String = "https://context.example.com",
        status: CaptureStatus = .queued,
        filePath: String = "/tmp/capture_123.m4a",
        sizeBytes: Int64 = 42,
        sha256: String = "abc"
    ) -> CaptureItem {
        CaptureItem(
            captureID: "capture_123",
            accountID: account,
            endpoint: endpoint,
            projectID: "prj_private",
            projectName: "我的记录",
            filePath: filePath,
            sizeBytes: sizeBytes,
            sha256: sha256,
            occurredAt: "2026-09-12T10:00:00Z",
            metadataJSON: "{\"capture_id\":\"capture_123\",\"source\":\"phone.recorder\"}",
            status: status
        )
    }

    @Test func persistsQueueAcrossRestart() async throws {
        let url = try temporaryURL()
        let first = CaptureQueueStore(fileURL: url)
        try await first.upsert(item())

        let second = CaptureQueueStore(fileURL: url)
        #expect(await second.all().first?.uploadIdentity == item().uploadIdentity)
        #expect(await second.all().first?.status == .queued)
    }

    @Test func retryKeepsFixedUploadIdentity() async throws {
        let url = try temporaryURL()
        let store = CaptureQueueStore(fileURL: url)
        let queued = item()
        try await store.upsert(queued)
        _ = try await store.update(id: queued.captureID) {
            $0.status = .failed
            $0.attemptCount += 1
            $0.nextAttemptAt = Date().addingTimeInterval(10)
            $0.errorCode = "network_error"
        }
        #expect(await store.item(id: queued.captureID)?.uploadIdentity == queued.uploadIdentity)
    }

    @Test func accountAndEndpointQueriesNeverMixPendingMedia() async throws {
        let url = try temporaryURL()
        let store = CaptureQueueStore(fileURL: url)
        try await store.upsert(item(account: "usr_a"))
        var other = item(account: "usr_b")
        other = CaptureItem(
            captureID: "capture_456",
            accountID: other.accountID,
            endpoint: other.endpoint,
            projectID: other.projectID,
            projectName: other.projectName,
            filePath: "/tmp/capture_456.m4a",
            sizeBytes: other.sizeBytes,
            sha256: other.sha256,
            occurredAt: other.occurredAt,
            metadataJSON: "{\"capture_id\":\"capture_456\"}",
            status: other.status
        )
        try await store.upsert(other)
        var otherEndpoint = item(account: "usr_a", endpoint: "https://other.example.com")
        otherEndpoint = CaptureItem(
            captureID: "capture_789",
            accountID: otherEndpoint.accountID,
            endpoint: otherEndpoint.endpoint,
            projectID: otherEndpoint.projectID,
            projectName: otherEndpoint.projectName,
            filePath: "/tmp/capture_789.m4a",
            sizeBytes: otherEndpoint.sizeBytes,
            sha256: otherEndpoint.sha256,
            occurredAt: otherEndpoint.occurredAt,
            metadataJSON: "{\"capture_id\":\"capture_789\"}",
            status: otherEndpoint.status
        )
        try await store.upsert(otherEndpoint)

        #expect(await store.items(endpoint: "https://context.example.com", accountID: "usr_a").map(\.captureID) == ["capture_123"])
        #expect(await store.items(endpoint: "https://context.example.com", accountID: "usr_b").map(\.captureID) == ["capture_456"])
        #expect(await store.items(endpoint: "https://other.example.com", accountID: "usr_a").map(\.captureID) == ["capture_789"])
    }

    @Test func unfinishedRecordingRecoversAsInterrupted() async throws {
        let url = try temporaryURL()
        let partialURL = url.deletingLastPathComponent().appendingPathComponent("partial.m4a")
        try FileManager.default.createDirectory(at: partialURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        let partial = Data("partial audio bytes".utf8)
        try partial.write(to: partialURL)
        let first = CaptureQueueStore(fileURL: url)
        try await first.upsert(item(status: .recording, filePath: partialURL.path))

        let second = CaptureQueueStore(fileURL: url)
        #expect(try await second.recoverInterruptedRecordings() == 1)
        let recovered = await second.item(id: "capture_123")
        #expect(recovered?.status == .interrupted)
        #expect(recovered?.sizeBytes == Int64(partial.count))
        #expect(recovered?.sha256.isEmpty == false)
        #expect(recovered?.canRetry == true)
    }

    @Test func missingInterruptedRecordingIsNotReportedAsSaved() async throws {
        let url = try temporaryURL()
        let missingURL = url.deletingLastPathComponent().appendingPathComponent("missing.m4a")
        let first = CaptureQueueStore(fileURL: url)
        try await first.upsert(item(status: .recording, filePath: missingURL.path))

        let second = CaptureQueueStore(fileURL: url)
        #expect(try await second.recoverInterruptedRecordings() == 0)
        let recovered = await second.item(id: "capture_123")
        #expect(recovered?.status == .failed)
        #expect(recovered?.errorCode == "local_file_unreadable")
        #expect(recovered?.canRetry == false)
    }

    @Test func unstartedPlaceholderCanBeRemovedWithoutDeletingCapturedData() async throws {
        let url = try temporaryURL()
        let store = CaptureQueueStore(fileURL: url)
        let missingURL = url.deletingLastPathComponent().appendingPathComponent("not-started.m4a")
        try await store.upsert(item(status: .recording, filePath: missingURL.path, sizeBytes: 0, sha256: ""))

        #expect(try await store.removeUnstarted(id: "capture_123"))
        #expect(await store.item(id: "capture_123") == nil)

        let existingURL = url.deletingLastPathComponent().appendingPathComponent("started.m4a")
        try Data("audio".utf8).write(to: existingURL)
        try await store.upsert(item(status: .recording, filePath: existingURL.path, sizeBytes: 0, sha256: ""))
        #expect(try await store.removeUnstarted(id: "capture_123") == false)
        #expect(await store.item(id: "capture_123") != nil)
    }

    @Test func failedDiskWriteDoesNotAdvanceInMemoryState() async throws {
        let url = try temporaryURL()
        let store = CaptureQueueStore(fileURL: url)
        let queued = item()
        try await store.upsert(queued)

        let directory = url.deletingLastPathComponent()
        try FileManager.default.removeItem(at: directory)
        try Data("blocking file".utf8).write(to: directory)

        await #expect(throws: (any Error).self) {
            _ = try await store.update(id: queued.captureID) { $0.status = .synced }
        }
        #expect(await store.item(id: queued.captureID)?.status == .queued)
    }

    @Test func duplicateCaptureIDsAreReportedWithoutCrashing() async throws {
        let url = try temporaryURL()
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode([item(), item()]).write(to: url)

        let store = CaptureQueueStore(fileURL: url)
        await #expect(throws: CaptureQueueStore.StoreError.duplicateCaptureID("capture_123")) {
            _ = try await store.recoverInterruptedRecordings()
        }
    }

    @Test(arguments: [CaptureStatus.localSaved, .uploading])
    func crashUploadPhasesReturnToQueuedWithoutChangingIdentity(status: CaptureStatus) async throws {
        let url = try temporaryURL()
        let first = CaptureQueueStore(fileURL: url)
        let original = item(status: status)
        try await first.upsert(original)

        let second = CaptureQueueStore(fileURL: url)
        #expect(try await second.recoverUploadPhases() == 1)
        #expect(await second.item(id: original.captureID)?.status == .queued)
        #expect(await second.item(id: original.captureID)?.uploadIdentity == original.uploadIdentity)
    }
}
