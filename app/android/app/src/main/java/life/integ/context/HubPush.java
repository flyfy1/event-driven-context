package life.integ.context;

import android.content.Context;

import com.google.firebase.FirebaseApp;
import com.google.firebase.messaging.FirebaseMessaging;

import androidx.work.Data;
import androidx.work.OneTimeWorkRequest;
import androidx.work.WorkManager;

/** FCM is an optional wake-up transport. Every notification requires an authenticated fresh read. */
final class HubPush {
    static boolean configured(Context context) { return !FirebaseApp.getApps(context).isEmpty(); }

    static void register(Context context, SessionStore.Session expected) {
        Context app = context.getApplicationContext();
        if (!configured(app) || !AuthorizationReminderWorker.enabled(app, expected)) return;
        FirebaseMessaging.getInstance().getToken().addOnSuccessListener(token -> {
            synchronized (SessionStore.class) {
                if (!HubAuthorization.sameSession(expected, new SessionStore(app).load())
                        || !AuthorizationReminderWorker.enabled(app, expected)) return;
                app.getSharedPreferences("hub-push", Context.MODE_PRIVATE).edit()
                        .putString("token-" + HubAuthorization.accountKey(expected), token).apply();
                wake(app, expected);
            }
        });
    }

    static void wake(Context context, SessionStore.Session expected) {
        if (expected == null || !AuthorizationReminderWorker.enabled(context, expected)) return;
        WorkManager.getInstance(context).enqueue(new OneTimeWorkRequest.Builder(HubPushWorker.class)
                .setInputData(new Data.Builder().putString("account_key", HubAuthorization.accountKey(expected)).build()).build());
    }

    static String token(Context context, SessionStore.Session expected) {
        return context.getSharedPreferences("hub-push", Context.MODE_PRIVATE)
                .getString("token-" + HubAuthorization.accountKey(expected), null);
    }

    static void unregister(Context context, SessionStore.Session expected) {
        Context app = context.getApplicationContext();
        new Thread(() -> unregisterBlocking(app, expected), "hub-push-unregister").start();
    }

    static void unregisterBlocking(Context context, SessionStore.Session expected) {
        String token = token(context, expected);
        if (token == null) return;
        try { new ApiClient(expected.endpoint, expected.token).hubDevice("DELETE", token); }
        catch (Exception ignored) { /* No private data is included in stale push messages. */ }
    }
}
