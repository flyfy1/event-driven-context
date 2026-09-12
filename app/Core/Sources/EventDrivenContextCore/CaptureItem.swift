import Foundation

public enum CaptureStatus: String, Codable, CaseIterable, Sendable {
    case recording
    case interrupted
    case localSaved = "local_saved"
    case queued
    case waitingNetwork = "waiting_network"
    case waitingAuth = "waiting_auth"
    case uploading
    case synced
    case processing
    case processingFailed = "processing_failed"
    case ready
    case failed

    public var localizationKey: String { "capture_status_\(rawValue)" }
    public var canRetry: Bool {
        self == .interrupted || self == .failed || self == .waitingNetwork || self == .waitingAuth
    }
}

public struct CaptureItem: Codable, Equatable, Identifiable, Sendable {
    public var id: String { captureID }
    public let captureID: String
    public let accountID: String
    public let endpoint: String
    public let projectID: String
    public let projectName: String
    public let filePath: String
    public let mediaType: String
    public var sizeBytes: Int64
    public var sha256: String
    public let occurredAt: String
    /// The exact JSON string submitted on every retry.
    public let metadataJSON: String
    public var status: CaptureStatus
    public var eventID: String?
    public var remoteFileID: String?
    public var attemptCount: Int
    public var nextAttemptAt: Date?
    public var errorCode: String?
    public let createdAt: Date
    public var updatedAt: Date

    public init(
        captureID: String,
        accountID: String,
        endpoint: String,
        projectID: String,
        projectName: String,
        filePath: String,
        mediaType: String = "audio/mp4",
        sizeBytes: Int64 = 0,
        sha256: String = "",
        occurredAt: String,
        metadataJSON: String,
        status: CaptureStatus = .recording,
        eventID: String? = nil,
        remoteFileID: String? = nil,
        attemptCount: Int = 0,
        nextAttemptAt: Date? = nil,
        errorCode: String? = nil,
        createdAt: Date = Date(),
        updatedAt: Date = Date()
    ) {
        self.captureID = captureID
        self.accountID = accountID
        self.endpoint = endpoint
        self.projectID = projectID
        self.projectName = projectName
        self.filePath = filePath
        self.mediaType = mediaType
        self.sizeBytes = sizeBytes
        self.sha256 = sha256
        self.occurredAt = occurredAt
        self.metadataJSON = metadataJSON
        self.status = status
        self.eventID = eventID
        self.remoteFileID = remoteFileID
        self.attemptCount = attemptCount
        self.nextAttemptAt = nextAttemptAt
        self.errorCode = errorCode
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    public var uploadIdentity: UploadIdentity {
        UploadIdentity(
            captureID: captureID,
            accountID: accountID,
            endpoint: endpoint,
            projectID: projectID,
            filePath: filePath,
            mediaType: mediaType,
            occurredAt: occurredAt,
            metadataJSON: metadataJSON,
            sha256: sha256
        )
    }

    public var canRetry: Bool {
        guard [.interrupted, .localSaved, .queued, .waitingNetwork, .waitingAuth, .uploading, .failed].contains(status) else {
            return false
        }
        return !["local_file_changed", "local_file_unreadable", "confirmation_mismatch",
                  "invalid_input", "conflict", "too_large", "request_too_large"]
            .contains(errorCode)
    }
}

/// Immutable fields that must remain identical when a lost response is retried.
public struct UploadIdentity: Codable, Equatable, Sendable {
    public let captureID: String
    public let accountID: String
    public let endpoint: String
    public let projectID: String
    public let filePath: String
    public let mediaType: String
    public let occurredAt: String
    public let metadataJSON: String
    public let sha256: String
}
