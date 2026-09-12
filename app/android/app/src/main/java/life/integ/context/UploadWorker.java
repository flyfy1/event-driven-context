package life.integ.context;

import android.content.Context;

import androidx.annotation.NonNull;
import androidx.work.BackoffPolicy;
import androidx.work.Constraints;
import androidx.work.Data;
import androidx.work.ExistingWorkPolicy;
import androidx.work.NetworkType;
import androidx.work.OneTimeWorkRequest;
import androidx.work.WorkManager;
import androidx.work.Worker;
import androidx.work.WorkerParameters;

import org.json.JSONException;

import java.io.IOException;
import java.util.concurrent.TimeUnit;

public final class UploadWorker extends Worker {
    private static final String INPUT_ID = "capture_id";

    public UploadWorker(@NonNull Context context, @NonNull WorkerParameters parameters) {
        super(context, parameters);
    }

    @NonNull @Override public Result doWork() {
        String id = getInputData().getString(INPUT_ID);
        if (id == null) return Result.failure();
        QueueStore queue = new QueueStore(getApplicationContext());
        CaptureItem item = queue.get(id);
        if (item == null || CaptureItem.SYNCED.equals(item.state) || CaptureItem.INTERRUPTED.equals(item.state)) {
            return Result.success();
        }
        SessionStore.Session session = new SessionStore(getApplicationContext()).load();
        if (!item.belongsToAccount(session)) {
            queue.updateState(id, CaptureItem.AUTH_REQUIRED, "waiting for the original endpoint and account");
            return Result.success();
        }
        try {
            ApiClient client = new ApiClient(session.endpoint, session.token);
            String fileId = item.fileId;
            if (fileId == null || fileId.isEmpty()) {
                queue.updateState(id, CaptureItem.UPLOADING, null);
                fileId = client.uploadFile(item).id;
                queue.setFileUploaded(id, fileId);
            }
            client.record(item, fileId);
            queue.updateState(id, CaptureItem.SYNCED, null);
            notifyChanged();
            return Result.success();
        } catch (ApiClient.ApiException error) {
            if (error.needsLogin()) {
                queue.updateState(id, CaptureItem.AUTH_REQUIRED, error.getMessage());
                notifyChanged();
                return Result.success();
            }
            if (error.retryable()) {
                queue.updateState(id, item.fileId == null ? CaptureItem.READY : CaptureItem.EVENT_PENDING, error.getMessage());
                notifyChanged();
                return Result.retry();
            }
            queue.updateState(id, CaptureItem.FAILED, error.code + ": " + error.getMessage());
            notifyChanged();
            return Result.failure();
        } catch (IOException | JSONException error) {
            queue.updateState(id, item.fileId == null ? CaptureItem.READY : CaptureItem.EVENT_PENDING, error.getMessage());
            notifyChanged();
            return Result.retry();
        }
    }

    private void notifyChanged() {
        getApplicationContext().sendBroadcast(new android.content.Intent(MainActivity.ACTION_CAPTURE_CHANGED)
                .setPackage(getApplicationContext().getPackageName()));
    }

    static void enqueue(Context context, String captureId) {
        Constraints constraints = new Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build();
        Data data = new Data.Builder().putString(INPUT_ID, captureId).build();
        OneTimeWorkRequest request = new OneTimeWorkRequest.Builder(UploadWorker.class)
                .setInputData(data).setConstraints(constraints)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 15, TimeUnit.SECONDS).build();
        WorkManager.getInstance(context.getApplicationContext()).enqueueUniqueWork(
                "capture-upload-" + captureId, ExistingWorkPolicy.KEEP, request);
    }

    static void enqueuePending(Context context) {
        SessionStore.Session session = new SessionStore(context).load();
        if (session == null) return;
        for (CaptureItem item : new QueueStore(context).pending(session)) enqueue(context, item.id);
    }
}
