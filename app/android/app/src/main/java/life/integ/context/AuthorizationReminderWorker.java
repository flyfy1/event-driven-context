package life.integ.context;

import android.Manifest;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.os.Build;

import androidx.annotation.NonNull;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;
import androidx.work.Constraints;
import androidx.work.Data;
import androidx.work.ExistingPeriodicWorkPolicy;
import androidx.work.NetworkType;
import androidx.work.PeriodicWorkRequest;
import androidx.work.WorkManager;
import androidx.work.Worker;
import androidx.work.WorkerParameters;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.TimeUnit;

/** Periodic polling; this is intentionally not presented as remote push. */
public final class AuthorizationReminderWorker extends Worker {
    private static final String WORK = "hub-authorization-reminders";
    private static final String CHANNEL = "hub-authorizations";
    private static final int NOTIFICATION = 4102;

    public AuthorizationReminderWorker(@NonNull Context context, @NonNull WorkerParameters parameters) {
        super(context, parameters);
    }

    private static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences("hub-reminders", Context.MODE_PRIVATE);
    }

    static boolean enabled(Context context, SessionStore.Session session) {
        return session != null && prefs(context).getBoolean("enabled-" + HubAuthorization.accountKey(session), false);
    }

    static void enable(Context context, SessionStore.Session session, boolean enabled) {
        synchronized (SessionStore.class) {
            if (!HubAuthorization.sameSession(session, new SessionStore(context).load())) return;
            prefs(context).edit().putBoolean("enabled-" + HubAuthorization.accountKey(session), enabled).apply();
            if (enabled) schedule(context, session); else cancel(context);
        }
    }

    static void schedule(Context context, SessionStore.Session session) {
        if (!enabled(context, session)) return;
        PeriodicWorkRequest request = new PeriodicWorkRequest.Builder(AuthorizationReminderWorker.class, 15, TimeUnit.MINUTES)
                .setConstraints(new Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setInputData(new Data.Builder().putString("account_key", HubAuthorization.accountKey(session)).build())
                .build();
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK, ExistingPeriodicWorkPolicy.UPDATE, request);
    }

    static void cancel(Context context) {
        WorkManager.getInstance(context).cancelUniqueWork(WORK);
        context.getSystemService(NotificationManager.class).cancel(NOTIFICATION);
    }

    static boolean permission(Context context) {
        NotificationChannel channel = context.getSystemService(NotificationManager.class).getNotificationChannel(CHANNEL);
        return (channel == null || channel.getImportance() != NotificationManager.IMPORTANCE_NONE)
                && (Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS)
                == PackageManager.PERMISSION_GRANTED) && NotificationManagerCompat.from(context).areNotificationsEnabled();
    }

    static void reconcile(Context context, SessionStore.Session expected, JSONObject owner, boolean notify) throws Exception {
        List<String> pending = new ArrayList<>();
        long now = System.currentTimeMillis();
        for (String kind : new String[]{"agents", "requests"}) {
            JSONArray items = owner.getJSONArray(kind);
            for (int i = 0; i < items.length(); i++) {
                JSONObject item = items.getJSONObject(i);
                if (HubAuthorization.pending(item.optString("status"), item.optString("expires_at"), now)) {
                    pending.add(kind + ":" + item.getString("id"));
                }
            }
        }
        Collections.sort(pending);
        synchronized (SessionStore.class) {
            if (!HubAuthorization.sameSession(expected, new SessionStore(context).load())) return;
            NotificationManager manager = context.getSystemService(NotificationManager.class);
            String key = "seen-" + HubAuthorization.accountKey(expected);
            if (pending.isEmpty()) {
                manager.cancel(NOTIFICATION);
                prefs(context).edit().remove(key).apply();
                return;
            }
            String signature = HubAuthorization.digest(String.join("\n", pending));
            if (!notify || !enabled(context, expected) || !permission(context)
                    || signature.equals(prefs(context).getString(key, ""))) return;
            manager.createNotificationChannel(new NotificationChannel(CHANNEL,
                    context.getString(R.string.hub_title), NotificationManager.IMPORTANCE_DEFAULT));
            if (manager.getNotificationChannel(CHANNEL).getImportance() == NotificationManager.IMPORTANCE_NONE) return;
            Intent intent = new Intent(context, AuthorizationsActivity.class)
                    .putExtra("account_key", HubAuthorization.accountKey(expected))
                    .putExtra("request_id", pending.get(0))
                    .setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
            PendingIntent open = PendingIntent.getActivity(context, NOTIFICATION, intent,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            manager.notify(NOTIFICATION, new NotificationCompat.Builder(context, CHANNEL)
                    .setSmallIcon(android.R.drawable.ic_dialog_info)
                    .setContentTitle(context.getString(R.string.hub_notification_title))
                    .setContentText(context.getString(R.string.hub_notification_body, pending.size()))
                    .setContentIntent(open).setAutoCancel(true).setOnlyAlertOnce(true)
                    .setVisibility(NotificationCompat.VISIBILITY_PRIVATE).build());
            prefs(context).edit().putString(key, signature).apply();
        }
    }

    @NonNull @Override public Result doWork() {
        Context context = getApplicationContext();
        SessionStore.Session expected = new SessionStore(context).load();
        if (expected == null || !HubAuthorization.accountKey(expected).equals(getInputData().getString("account_key"))
                || !HubAuthorization.sameSession(expected, expected) || !enabled(context, expected)) return Result.success();
        try {
            JSONObject owner = new ApiClient(expected.endpoint, expected.token).hubOwner();
            if (!isStopped()) reconcile(context, expected, owner, true);
            return Result.success();
        } catch (ApiClient.ApiException error) {
            return error.retryable() ? Result.retry() : Result.success();
        } catch (Exception error) { return Result.retry(); }
    }
}
