import CryptoKit
import Foundation

public actor CaptureQueueStore {
    public enum StoreError: Error, Equatable {
        case duplicateCaptureID(String)
        case unreadableQueue
    }

    private let fileURL: URL
    private var itemsByID: [String: CaptureItem]
    private var loadFailure: StoreError?
    private let encoder: JSONEncoder
    private let decoder: JSONDecoder

    public init(fileURL: URL) {
        self.fileURL = fileURL
        self.encoder = JSONEncoder()
        self.decoder = JSONDecoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601

        self.itemsByID = [:]
        self.loadFailure = nil
        do {
            try FileManager.default.createDirectory(
                at: fileURL.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
            if FileManager.default.fileExists(atPath: fileURL.path) {
                let data = try Data(contentsOf: fileURL)
                let items = try decoder.decode([CaptureItem].self, from: data)
                var decoded: [String: CaptureItem] = [:]
                for item in items {
                    guard decoded[item.captureID] == nil else {
                        loadFailure = .duplicateCaptureID(item.captureID)
                        return
                    }
                    decoded[item.captureID] = item
                }
                self.itemsByID = decoded
            }
        } catch {
            self.loadFailure = .unreadableQueue
        }
    }

    public func all() -> [CaptureItem] {
        itemsByID.values.sorted { $0.createdAt > $1.createdAt }
    }

    public func item(id: String) -> CaptureItem? { itemsByID[id] }

    public func items(endpoint: String, accountID: String) -> [CaptureItem] {
        all().filter { $0.endpoint == endpoint && $0.accountID == accountID }
    }

    public func upsert(_ item: CaptureItem) throws {
        try ensureLoaded()
        var candidate = itemsByID
        candidate[item.captureID] = item
        try persist(candidate)
        itemsByID = candidate
    }

    @discardableResult
    public func update(
        id: String,
        _ change: @Sendable (inout CaptureItem) -> Void
    ) throws -> CaptureItem? {
        try ensureLoaded()
        guard var item = itemsByID[id] else { return nil }
        change(&item)
        item.updatedAt = Date()
        var candidate = itemsByID
        candidate[id] = item
        try persist(candidate)
        itemsByID = candidate
        return item
    }

    /// Remove only the empty placeholder written before AVAudioRecorder starts.
    /// A captured file or finalized queue item is never removed by this path.
    @discardableResult
    public func removeUnstarted(id: String) throws -> Bool {
        try ensureLoaded()
        guard let item = itemsByID[id], item.status == .recording,
              item.sizeBytes == 0, item.sha256.isEmpty,
              !FileManager.default.fileExists(atPath: item.filePath) else { return false }
        var candidate = itemsByID
        candidate.removeValue(forKey: id)
        try persist(candidate)
        itemsByID = candidate
        return true
    }

    /// An app termination while recording never upgrades the fragment to a complete recording.
    @discardableResult
    public func recoverInterruptedRecordings() throws -> Int {
        try ensureLoaded()
        var recovered = 0
        var processed = 0
        var candidate = itemsByID
        for id in candidate.keys {
            guard var item = candidate[id], item.status == .recording else { continue }
            processed += 1
            if let snapshot = try? captureFileSnapshot(path: item.filePath) {
                item.sizeBytes = snapshot.sizeBytes
                item.sha256 = snapshot.sha256
                item.status = .interrupted
                item.errorCode = "recording_interrupted"
                recovered += 1
            } else {
                item.status = .failed
                item.errorCode = "local_file_unreadable"
            }
            item.updatedAt = Date()
            candidate[id] = item
        }
        if processed > 0 {
            try persist(candidate)
            itemsByID = candidate
        }
        return recovered
    }

    /// Uploading has no durable response receipt. Requeue it with identical inputs so the
    /// server idempotency key resolves a request that may already have committed.
    @discardableResult
    public func recoverUploadPhases() throws -> Int {
        try ensureLoaded()
        var recovered = 0
        var candidate = itemsByID
        for id in candidate.keys {
            guard var item = candidate[id], item.status == .localSaved || item.status == .uploading else { continue }
            item.status = .queued
            item.errorCode = nil
            item.nextAttemptAt = nil
            item.updatedAt = Date()
            candidate[id] = item
            recovered += 1
        }
        if recovered > 0 {
            try persist(candidate)
            itemsByID = candidate
        }
        return recovered
    }

    private func persist(_ items: [String: CaptureItem]) throws {
        let ordered = items.values.sorted { $0.createdAt > $1.createdAt }
        let data = try encoder.encode(ordered)
        try data.write(to: fileURL, options: .atomic)
    }

    private func ensureLoaded() throws {
        if let loadFailure { throw loadFailure }
    }
}

private func captureFileSnapshot(path: String) throws -> (sizeBytes: Int64, sha256: String) {
    let attributes = try FileManager.default.attributesOfItem(atPath: path)
    guard attributes[.type] as? FileAttributeType == .typeRegular,
          let number = attributes[.size] as? NSNumber, number.int64Value > 0 else {
        throw CocoaError(.fileReadCorruptFile)
    }
    let handle = try FileHandle(forReadingFrom: URL(fileURLWithPath: path))
    defer { try? handle.close() }
    var hasher = SHA256()
    while true {
        let data = try handle.read(upToCount: 256 * 1024) ?? Data()
        if data.isEmpty { break }
        hasher.update(data: data)
    }
    return (number.int64Value, hasher.finalize().map { String(format: "%02x", $0) }.joined())
}
