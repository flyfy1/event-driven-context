package life.integ.context;

import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.database.sqlite.SQLiteDatabase;
import android.database.sqlite.SQLiteOpenHelper;

import java.util.ArrayList;
import java.util.List;

final class QueueStore extends SQLiteOpenHelper {
    private static final String DB_NAME = "capture-queue.db";
    private static final int DB_VERSION = 1;

    QueueStore(Context context) {
        super(context.getApplicationContext(), DB_NAME, null, DB_VERSION);
    }

    @Override public void onCreate(SQLiteDatabase db) {
        db.execSQL("CREATE TABLE captures ("
                + "id TEXT PRIMARY KEY, endpoint TEXT NOT NULL, account_id TEXT NOT NULL, "
                + "project_id TEXT NOT NULL, local_path TEXT NOT NULL, filename TEXT NOT NULL, "
                + "media_type TEXT NOT NULL, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, "
                + "duration_ms INTEGER NOT NULL, occurred_at TEXT NOT NULL, file_id TEXT, "
                + "state TEXT NOT NULL, last_error TEXT, created_at INTEGER NOT NULL)");
        db.execSQL("CREATE INDEX capture_scope ON captures(endpoint, account_id, project_id, created_at)");
    }

    @Override public void onUpgrade(SQLiteDatabase db, int oldVersion, int newVersion) {
        throw new IllegalStateException("No queue migration exists from " + oldVersion + " to " + newVersion);
    }

    synchronized void insert(CaptureItem item) {
        long row = getWritableDatabase().insertWithOnConflict("captures", null, values(item), SQLiteDatabase.CONFLICT_IGNORE);
        if (row == -1) {
            CaptureItem existing = get(item.id);
            if (existing == null || !sameIdentity(existing, item)) {
                throw new IllegalStateException("capture UUID already has different persisted input");
            }
        }
    }

    synchronized void finalizeReady(CaptureItem item) {
        SQLiteDatabase db = getWritableDatabase();
        db.beginTransaction();
        try {
            CaptureItem existing = get(item.id);
            if (existing == null) {
                if (db.insertOrThrow("captures", null, values(item)) == -1) throw new IllegalStateException("capture was not persisted");
            } else if (CaptureItem.INTERRUPTED.equals(existing.state)
                    && existing.endpoint.equals(item.endpoint) && existing.accountId.equals(item.accountId)
                    && existing.projectId.equals(item.projectId) && existing.localPath.equals(item.localPath)) {
                ContentValues replacement = values(item);
                if (db.update("captures", replacement, "id=?", new String[]{item.id}) != 1) {
                    throw new IllegalStateException("capture was not finalized");
                }
            } else if (!sameIdentity(existing, item)) {
                throw new IllegalStateException("capture UUID already has different persisted input");
            }
            db.setTransactionSuccessful();
        } finally {
            db.endTransaction();
        }
    }

    synchronized CaptureItem get(String id) {
        try (Cursor cursor = getReadableDatabase().query("captures", null, "id=?", new String[]{id}, null, null, null)) {
            return cursor.moveToFirst() ? read(cursor) : null;
        }
    }

    synchronized List<CaptureItem> list(SessionStore.Session session) {
        List<CaptureItem> result = new ArrayList<>();
        String selection = null;
        String[] args = null;
        if (session != null && session.projectId != null) {
            selection = "endpoint=? AND account_id=? AND project_id=?";
            args = new String[]{session.endpoint, session.userId, session.projectId};
        }
        try (Cursor cursor = getReadableDatabase().query("captures", null, selection, args, null, null, "created_at DESC")) {
            while (cursor.moveToNext()) result.add(read(cursor));
        }
        return result;
    }

    synchronized List<CaptureItem> pending(SessionStore.Session session) {
        List<CaptureItem> result = new ArrayList<>();
        if (session == null) return result;
        String selection = "endpoint=? AND account_id=? AND state IN (?,?,?,?)";
        String[] args = {session.endpoint, session.userId,
                CaptureItem.READY, CaptureItem.UPLOADING, CaptureItem.EVENT_PENDING, CaptureItem.AUTH_REQUIRED};
        try (Cursor cursor = getReadableDatabase().query("captures", null, selection, args, null, null, "created_at")) {
            while (cursor.moveToNext()) result.add(read(cursor));
        }
        return result;
    }

    synchronized void updateState(String id, String state, String error) {
        ContentValues values = new ContentValues();
        values.put("state", state);
        if (error == null) values.putNull("last_error"); else values.put("last_error", error);
        getWritableDatabase().update("captures", values, "id=?", new String[]{id});
    }

    synchronized void setFileUploaded(String id, String fileId) {
        ContentValues values = new ContentValues();
        values.put("file_id", fileId);
        values.put("state", CaptureItem.EVENT_PENDING);
        values.putNull("last_error");
        getWritableDatabase().update("captures", values, "id=?", new String[]{id});
    }

    synchronized void retryInterrupted(String id) {
        CaptureItem item = get(id);
        if (item != null && CaptureItem.INTERRUPTED.equals(item.state) && item.fileStillMatches()) {
            updateState(id, CaptureItem.READY, null);
        }
    }

    synchronized int pendingCount(SessionStore.Session session) {
        return pending(session).size();
    }

    private static ContentValues values(CaptureItem item) {
        ContentValues values = new ContentValues();
        values.put("id", item.id); values.put("endpoint", item.endpoint);
        values.put("account_id", item.accountId); values.put("project_id", item.projectId);
        values.put("local_path", item.localPath); values.put("filename", item.filename);
        values.put("media_type", item.mediaType); values.put("size_bytes", item.sizeBytes);
        values.put("sha256", item.sha256); values.put("duration_ms", item.durationMs);
        values.put("occurred_at", item.occurredAt); values.put("file_id", item.fileId);
        values.put("state", item.state); values.put("last_error", item.lastError);
        values.put("created_at", item.createdAt);
        return values;
    }

    private static boolean sameIdentity(CaptureItem a, CaptureItem b) {
        return a.id.equals(b.id) && a.endpoint.equals(b.endpoint) && a.accountId.equals(b.accountId)
                && a.projectId.equals(b.projectId) && a.localPath.equals(b.localPath)
                && a.sizeBytes == b.sizeBytes && a.sha256.equals(b.sha256)
                && a.mediaType.equals(b.mediaType) && a.occurredAt.equals(b.occurredAt);
    }

    private static CaptureItem read(Cursor c) {
        return new CaptureItem(
                value(c, "id"), value(c, "endpoint"), value(c, "account_id"), value(c, "project_id"),
                value(c, "local_path"), value(c, "filename"), value(c, "media_type"),
                c.getLong(c.getColumnIndexOrThrow("size_bytes")), value(c, "sha256"),
                c.getLong(c.getColumnIndexOrThrow("duration_ms")), value(c, "occurred_at"),
                nullable(c, "file_id"), value(c, "state"), nullable(c, "last_error"),
                c.getLong(c.getColumnIndexOrThrow("created_at")));
    }

    private static String value(Cursor c, String name) { return c.getString(c.getColumnIndexOrThrow(name)); }
    private static String nullable(Cursor c, String name) {
        int index = c.getColumnIndexOrThrow(name);
        return c.isNull(index) ? null : c.getString(index);
    }
}
