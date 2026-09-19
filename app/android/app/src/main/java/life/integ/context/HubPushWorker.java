package life.integ.context;

import android.content.Context;
import androidx.annotation.NonNull;
import androidx.work.Worker;
import androidx.work.WorkerParameters;

public final class HubPushWorker extends Worker {
    public HubPushWorker(@NonNull Context context, @NonNull WorkerParameters parameters) { super(context, parameters); }

    @NonNull @Override public Result doWork() {
        Context context = getApplicationContext();
        SessionStore.Session expected = new SessionStore(context).load();
        if (expected == null || !HubAuthorization.accountKey(expected).equals(getInputData().getString("account_key"))
                || !HubAuthorization.sameSession(expected, expected) || !AuthorizationReminderWorker.enabled(context, expected)) return Result.success();
        try {
            String token = HubPush.token(context, expected);
            ApiClient client = new ApiClient(expected.endpoint, expected.token);
            if (token != null) client.hubDevice("POST", token);
            if (!isStopped()) AuthorizationReminderWorker.reconcile(context, expected, client.hubOwner(), true);
            return Result.success();
        } catch (ApiClient.ApiException error) {
            return error.retryable() ? Result.retry() : Result.success();
        } catch (Exception error) { return Result.retry(); }
    }
}
