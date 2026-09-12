package life.integ.context;

import java.io.File;

final class CaptureItem {
    static final String READY = "ready";
    static final String UPLOADING = "uploading";
    static final String EVENT_PENDING = "event_pending";
    static final String SYNCED = "synced";
    static final String INTERRUPTED = "interrupted";
    static final String AUTH_REQUIRED = "auth_required";
    static final String FAILED = "failed";

    final String id;
    final String endpoint;
    final String accountId;
    final String projectId;
    final String localPath;
    final String filename;
    final String mediaType;
    final long sizeBytes;
    final String sha256;
    final long durationMs;
    final String occurredAt;
    final String fileId;
    final String state;
    final String lastError;
    final long createdAt;

    CaptureItem(String id, String endpoint, String accountId, String projectId,
                String localPath, String filename, String mediaType, long sizeBytes,
                String sha256, long durationMs, String occurredAt, String fileId,
                String state, String lastError, long createdAt) {
        this.id = id;
        this.endpoint = endpoint;
        this.accountId = accountId;
        this.projectId = projectId;
        this.localPath = localPath;
        this.filename = filename;
        this.mediaType = mediaType;
        this.sizeBytes = sizeBytes;
        this.sha256 = sha256;
        this.durationMs = durationMs;
        this.occurredAt = occurredAt;
        this.fileId = fileId;
        this.state = state;
        this.lastError = lastError;
        this.createdAt = createdAt;
    }

    boolean fileStillMatches() {
        File file = new File(localPath);
        return file.isFile() && file.length() == sizeBytes;
    }

    boolean belongsToAccount(SessionStore.Session session) {
        return session != null
                && endpoint.equals(session.endpoint)
                && accountId.equals(session.userId);
    }
}
