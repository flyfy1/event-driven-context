package life.integ.context;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.media.MediaRecorder;
import android.os.Build;
import android.os.IBinder;
import android.os.SystemClock;

import java.io.File;
import java.time.OffsetDateTime;
import java.time.format.DateTimeFormatter;

public final class RecordingService extends Service {
    static final String ACTION_START = "life.integ.context.START_RECORDING";
    static final String ACTION_PAUSE = "life.integ.context.PAUSE_RECORDING";
    static final String ACTION_RESUME = "life.integ.context.RESUME_RECORDING";
    static final String ACTION_STOP = "life.integ.context.STOP_RECORDING";
    static final String EXTRA_CAPTURE_ID = "capture_id";
    private static final int NOTIFICATION_ID = 7101;
    private static final String CHANNEL_ID = "recording";
    private static final String JOURNAL = "recording-journal";

    private MediaRecorder recorder;
    private CaptureDraft draft;
    private long startedElapsed;
    private boolean normalStop;
    private static volatile boolean active;
    private static volatile boolean paused;

    static boolean isActive() { return active; }
    static boolean isPaused() { return paused; }

    @Override public void onCreate() {
        super.onCreate();
        createChannel();
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent == null) return START_NOT_STICKY;
        try {
            switch (intent.getAction() == null ? "" : intent.getAction()) {
                case ACTION_START: start(intent); break;
                case ACTION_PAUSE: pause(); break;
                case ACTION_RESUME: resume(); break;
                case ACTION_STOP: finish(); break;
                default: break;
            }
        } catch (Exception error) {
            interrupt(error.getMessage());
        }
        return START_NOT_STICKY;
    }

    private void start(Intent intent) throws Exception {
        if (recorder != null) return;
        String captureId = required(intent, EXTRA_CAPTURE_ID);
        draft = new CaptureDraft(captureId, required(intent, "endpoint"), required(intent, "account_id"),
                required(intent, "project_id"), required(intent, "occurred_at"),
                new File(capturesDir(this), captureId + ".m4a"));
        active = true;
        persistDraft(draft);
        startForeground(NOTIFICATION_ID, notification());
        recorder = Build.VERSION.SDK_INT >= 31 ? new MediaRecorder(this) : new MediaRecorder();
        recorder.setAudioSource(MediaRecorder.AudioSource.MIC);
        recorder.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4);
        recorder.setAudioEncoder(MediaRecorder.AudioEncoder.AAC);
        recorder.setAudioEncodingBitRate(96_000);
        recorder.setAudioSamplingRate(44_100);
        recorder.setOutputFile(draft.file.getAbsolutePath());
        recorder.prepare();
        recorder.start();
        startedElapsed = SystemClock.elapsedRealtime();
        paused = false;
        notifyState(null);
    }

    private void pause() {
        if (recorder == null || paused || Build.VERSION.SDK_INT < 24) return;
        recorder.pause();
        paused = true;
        notifyState(null);
    }

    private void resume() {
        if (recorder == null || !paused || Build.VERSION.SDK_INT < 24) return;
        recorder.resume();
        paused = false;
        notifyState(null);
    }

    private void finish() {
        if (recorder == null || draft == null) return;
        normalStop = true;
        try {
            recorder.stop();
            recorder.release();
            recorder = null;
            active = false;
            paused = false;
            FileIdentity identity = FileIdentity.read(draft.file);
            CaptureItem item = draft.item(identity, Math.max(1, SystemClock.elapsedRealtime() - startedElapsed), CaptureItem.READY, null);
            new QueueStore(this).finalizeReady(item);
            clearDraft();
            UploadWorker.enqueue(this, item.id);
            notifyState(null);
        } catch (Exception error) {
            normalStop = false;
            failPreservingJournal(error.getMessage());
            return;
        }
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    private void interrupt(String message) {
        try { if (recorder != null) recorder.release(); } catch (RuntimeException ignored) {}
        recorder = null;
        active = false;
        paused = false;
        boolean persisted = draft != null && saveInterrupted(this, draft, message);
        if (persisted) clearDraft();
        notifyState(message);
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    private void failPreservingJournal(String message) {
        try { if (recorder != null) recorder.release(); } catch (RuntimeException ignored) {}
        recorder = null;
        active = false;
        paused = false;
        notifyState(message);
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    @Override public void onDestroy() {
        if (!normalStop && recorder != null) interrupt("recording process stopped");
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) { return null; }

    static void recoverInterrupted(Context context) {
        SharedPreferences prefs = context.getSharedPreferences(JOURNAL, MODE_PRIVATE);
        if (!prefs.getBoolean("active", false)) return;
        CaptureDraft draft = CaptureDraft.from(prefs);
        if (draft != null && saveInterrupted(context, draft, "recording was interrupted")) prefs.edit().clear().commit();
    }

    private static boolean saveInterrupted(Context context, CaptureDraft draft, String message) {
        if (!draft.file.isFile() || draft.file.length() <= 0) return true;
        try {
            FileIdentity identity = FileIdentity.read(draft.file);
            new QueueStore(context).insert(draft.item(identity, 0, CaptureItem.INTERRUPTED, message));
            return true;
        } catch (Exception ignored) {
            // A missing or empty partial is deliberately not represented as a saved capture.
            return false;
        }
    }

    private Notification notification() {
        Intent stop = new Intent(this, RecordingService.class).setAction(ACTION_STOP);
        PendingIntent pending = PendingIntent.getService(this, 1, stop, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        return new Notification.Builder(this, CHANNEL_ID)
                .setSmallIcon(android.R.drawable.ic_btn_speak_now)
                .setContentTitle(getString(R.string.app_name))
                .setContentText(getString(R.string.recording_notification))
                .setOngoing(true)
                .addAction(new Notification.Action.Builder(null, getString(R.string.stop_recording), pending).build())
                .build();
    }

    private void createChannel() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel(CHANNEL_ID,
                getString(R.string.recording_notification), NotificationManager.IMPORTANCE_LOW));
    }

    private void persistDraft(CaptureDraft value) {
        boolean saved = getSharedPreferences(JOURNAL, MODE_PRIVATE).edit().putBoolean("active", true)
                .putString("id", value.id).putString("endpoint", value.endpoint)
                .putString("account_id", value.accountId).putString("project_id", value.projectId)
                .putString("occurred_at", value.occurredAt).putString("path", value.file.getAbsolutePath()).commit();
        if (!saved) throw new IllegalStateException("recording journal was not persisted");
    }

    private void clearDraft() { getSharedPreferences(JOURNAL, MODE_PRIVATE).edit().clear().commit(); }

    private void notifyState(String error) {
        sendBroadcast(new Intent(MainActivity.ACTION_CAPTURE_CHANGED).setPackage(getPackageName())
                .putExtra("recording", active).putExtra("paused", paused).putExtra("error", error));
    }

    private static File capturesDir(Context context) {
        File result = new File(context.getFilesDir(), "captures");
        if (!result.exists() && !result.mkdirs()) throw new IllegalStateException("could not create capture directory");
        return result;
    }

    private static String required(Intent intent, String key) {
        String value = intent.getStringExtra(key);
        if (value == null || value.isEmpty()) throw new IllegalArgumentException("missing " + key);
        return value;
    }

    static String now() { return OffsetDateTime.now().format(DateTimeFormatter.ISO_OFFSET_DATE_TIME); }

    private static final class CaptureDraft {
        final String id, endpoint, accountId, projectId, occurredAt;
        final File file;
        CaptureDraft(String id, String endpoint, String accountId, String projectId, String occurredAt, File file) {
            this.id = id; this.endpoint = endpoint; this.accountId = accountId;
            this.projectId = projectId; this.occurredAt = occurredAt; this.file = file;
        }
        CaptureItem item(FileIdentity identity, long duration, String state, String error) {
            return new CaptureItem(id, endpoint, accountId, projectId, file.getAbsolutePath(), file.getName(),
                    "audio/mp4", identity.size, identity.sha256, duration, occurredAt, null, state, error,
                    System.currentTimeMillis());
        }
        static CaptureDraft from(SharedPreferences prefs) {
            String id = prefs.getString("id", null), endpoint = prefs.getString("endpoint", null);
            String account = prefs.getString("account_id", null), project = prefs.getString("project_id", null);
            String occurred = prefs.getString("occurred_at", null), path = prefs.getString("path", null);
            if (id == null || endpoint == null || account == null || project == null || occurred == null || path == null) return null;
            return new CaptureDraft(id, endpoint, account, project, occurred, new File(path));
        }
    }
}
