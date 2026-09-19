package life.integ.context;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;
import android.text.InputType;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Owner-only inbox; no provider credentials or approval actions are placed in notifications. */
public final class AuthorizationsActivity extends Activity {
    private static final int NOTIFICATIONS = 4110;
    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private SessionStore.Session bound;
    private LinearLayout content;
    private TextView status;
    private int generation;
    private boolean resumed;
    private boolean enableAfterPermission;
    private boolean showHistory;
    private String notificationTarget;

    @Override protected void onCreate(Bundle state) {
        super.onCreate(state);
        bound = new SessionStore(this).load();
        String expectedAccount = getIntent().getStringExtra("account_key");
        if (bound == null || (expectedAccount != null && !expectedAccount.equals(HubAuthorization.accountKey(bound)))) {
            startActivity(new Intent(this, MainActivity.class)); finish(); return;
        }
        notificationTarget = getIntent().getStringExtra("request_id");
        ScrollView scroll = new ScrollView(this);
        content = new LinearLayout(this); content.setOrientation(LinearLayout.VERTICAL);
        int padding = (int) (20 * getResources().getDisplayMetrics().density);
        content.setPadding(padding, padding, padding, padding);
        scroll.addView(content); setContentView(scroll);
    }

    @Override protected void onResume() {
        super.onResume(); resumed = true;
        if (!isFinishing()) {
            refresh();
            if (enableAfterPermission) { enableAfterPermission = false; enableReminders(); }
        }
    }
    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        String account = intent.getStringExtra("account_key");
        if (bound == null || (account != null && !account.equals(HubAuthorization.accountKey(bound)))) {
            finish(); return;
        }
        setIntent(intent);
        notificationTarget = intent.getStringExtra("request_id");
        showHistory = false;
        if (resumed) refresh();
    }
    @Override protected void onPause() { resumed = false; generation++; super.onPause(); }
    @Override protected void onDestroy() { generation++; io.shutdownNow(); super.onDestroy(); }

    private boolean current() {
        return resumed && !isFinishing() && !isDestroyed()
                && HubAuthorization.sameSession(bound, new SessionStore(this).load());
    }

    private void refresh() {
        if (!current()) { finish(); return; }
        int version = ++generation;
        content.removeAllViews();
        addText(getString(R.string.hub_title), 26);
        addText(getString(R.string.account, bound.username) + "\n" + bound.endpoint, 14);
        addText(getString(R.string.hub_reminder_explanation), 14);
        boolean enabled = AuthorizationReminderWorker.enabled(this, bound);
        Button reminder = addButton(getString(enabled ? R.string.hub_disable_reminders : R.string.hub_enable_reminders));
        reminder.setOnClickListener(v -> {
            if (enabled) {
                AuthorizationReminderWorker.enable(this, bound, false);
                HubPush.unregister(this, bound);
                refresh();
            } else if (Build.VERSION.SDK_INT >= 33 && !AuthorizationReminderWorker.permission(this)) {
                requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, NOTIFICATIONS);
            } else enableReminders();
        });
        if (enabled && !AuthorizationReminderWorker.permission(this)) addText(getString(R.string.hub_notifications_blocked), 14);
        TextView pushStatus = addText(getString(HubPush.configured(this) ? R.string.hub_push_configured : R.string.hub_push_unconfigured), 14);
        addButton(getString(R.string.refresh)).setOnClickListener(v -> refresh());
        status = addText(getString(R.string.hub_loading), 16);
        io.execute(() -> {
            try {
                JSONObject owner = new ApiClient(bound.endpoint, bound.token).hubOwner();
                AuthorizationReminderWorker.reconcile(this, bound, owner, false);
                if (HubPush.configured(this)) {
                    try {
                        boolean configured = new ApiClient(bound.endpoint, bound.token).hubDeviceStatus().optBoolean("push_configured");
                        runOnUiThread(() -> {
                            if (current() && version == generation) pushStatus.setText(configured ? R.string.hub_push_ready : R.string.hub_push_server_missing);
                        });
                    } catch (Exception ignored) { /* Keep the explicit unverified server configuration label. */ }
                }
                runOnUiThread(() -> {
                    if (!current() || version != generation) return;
                    try { render(owner); }
                    catch (Exception error) { status.setText(R.string.hub_invalid_response); }
                });
            } catch (Exception error) {
                runOnUiThread(() -> { if (current() && version == generation) status.setText(R.string.hub_load_failed); });
            }
        });
    }

    private void enableReminders() {
        if (!current()) return;
        if (!AuthorizationReminderWorker.permission(this)) {
            status.setText(R.string.hub_notifications_blocked); return;
        }
        AuthorizationReminderWorker.enable(this, bound, true);
        HubPush.register(this, bound);
        refresh();
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(requestCode, permissions, results);
        if (requestCode == NOTIFICATIONS) {
            if (current()) enableReminders(); else enableAfterPermission = true;
        }
    }

    private void render(JSONObject owner) throws Exception {
        JSONArray agents = owner.getJSONArray("agents"), requests = owner.getJSONArray("requests");
        JSONArray connections = owner.getJSONArray("connections");
        int pending = 0;
        long now = System.currentTimeMillis();
        for (JSONArray items : new JSONArray[]{agents, requests}) {
            for (int i = 0; i < items.length(); i++) {
                JSONObject item = items.getJSONObject(i);
                if (HubAuthorization.pending(item.optString("status"), item.optString("expires_at"), now)) pending++;
            }
        }
        status.setText(getString(R.string.hub_pending_count, pending));
        addButton(getString(showHistory ? R.string.hub_show_pending : R.string.hub_show_history))
                .setOnClickListener(v -> { showHistory = !showHistory; refresh(); });
        boolean targetFound = false;
        for (String kind : new String[]{"agents", "requests"}) {
            JSONArray items = owner.getJSONArray(kind);
            for (int i = 0; i < items.length(); i++) {
                JSONObject item = items.getJSONObject(i);
                if ((kind + ":" + item.getString("id")).equals(notificationTarget)) {
                    targetFound = true;
                    addText(getString(HubAuthorization.pending(item.optString("status"), item.optString("expires_at"), now)
                            ? R.string.hub_notification_request : R.string.hub_notification_resolved), 20);
                    card(kind, item, connections);
                }
            }
        }
        if (notificationTarget != null && !targetFound) addText(getString(R.string.hub_notification_missing), 16);
        if (pending == 0 && !showHistory) addText(getString(R.string.hub_no_pending), 18);
        for (String kind : new String[]{"agents", "requests"}) {
            JSONArray items = owner.getJSONArray(kind);
            boolean heading = false;
            for (int i = 0; i < items.length(); i++) {
                JSONObject item = items.getJSONObject(i);
                if ((kind + ":" + item.getString("id")).equals(notificationTarget)
                        || !HubAuthorization.visible(item.optString("status"), item.optString("expires_at"), now, showHistory)) continue;
                if (!heading) { addText(getString("agents".equals(kind) ? R.string.hub_agents : R.string.hub_requests), 20); heading = true; }
                card(kind, item, connections);
            }
        }
        if (!showHistory) return;
        addText(getString(R.string.hub_connections), 20);
        if (connections.length() == 0) addText(getString(R.string.hub_no_connections), 16);
        for (int i = 0; i < connections.length(); i++) {
            JSONObject connection = connections.getJSONObject(i);
            addText(connection.optString("display_name") + "\n" + connection.optString("provider_id")
                    + " · " + connection.optString("account_id") + "\n" + connection.optString("status"), 16);
        }
    }

    private void card(String kind, JSONObject item, JSONArray connections) throws Exception {
        String id = item.getString("id"), state = item.getString("status"), expiry = item.optString("expires_at");
        boolean agent = "agents".equals(kind);
        boolean accountAvailable = agent;
        String account = item.optString("connection_name", item.optString("connection_id"));
        for (int i = 0; i < connections.length(); i++) {
            JSONObject connection = connections.getJSONObject(i);
            if (connection.optString("id").equals(item.optString("connection_id"))) {
                accountAvailable = !"disconnected".equals(connection.optString("status"));
                account = connection.optString("display_name") + " · " + connection.optString("provider_id")
                        + " · " + connection.optString("account_id");
                break;
            }
        }
        String description = agent ? item.optString("name") + "\n" + getString(R.string.hub_pairing_scope)
                : getString(R.string.hub_request_details, item.optString("agent_name"), account,
                        item.optString("operation"), item.optString("reason"));
        if (!agent) description += "\n" + getString(R.string.hub_operation_scope);
        if (!accountAvailable) description += "\n" + getString(R.string.hub_account_unavailable);
        description += "\n" + getString(R.string.hub_expiry, expiry) + "\n" + state + "\nID: " + id;
        addText(description, 16);
        boolean pending = HubAuthorization.pending(state, expiry, System.currentTimeMillis());
        if (pending) {
            String details = description;
            if (accountAvailable) addButton(getString(R.string.hub_approve)).setOnClickListener(v -> confirm(kind, id, "approve", details, expiry));
            addButton(getString(R.string.hub_deny)).setOnClickListener(v -> confirm(kind, id, "deny", details, expiry));
        } else if (("approved".equals(state) || "active".equals(state)) && HubAuthorization.unexpired(expiry, System.currentTimeMillis())) {
            String details = description;
            addButton(getString(R.string.hub_revoke)).setOnClickListener(v -> confirm(kind, id, "revoke", details, expiry));
        }
    }

    private void confirm(String kind, String id, String decision, String details, String expiry) {
        if (!current()) { finish(); return; }
        boolean approve = "approve".equals(decision);
        AlertDialog.Builder dialog = new AlertDialog.Builder(this)
                .setTitle(getString(approve ? R.string.hub_approve : "deny".equals(decision) ? R.string.hub_deny : R.string.hub_revoke))
                .setMessage(details).setNegativeButton(android.R.string.cancel, null);
        EditText code = new EditText(this);
        if (approve && "agents".equals(kind)) {
            code.setHint(R.string.hub_pairing_code);
            code.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS);
            dialog.setView(code);
        }
        dialog.setPositiveButton(android.R.string.ok, null);
        AlertDialog view = dialog.create();
        view.setOnShowListener(v -> view.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(button -> {
            if (!current()) { view.dismiss(); finish(); return; }
            if (approve && !HubAuthorization.unexpired(expiry, System.currentTimeMillis())) {
                view.dismiss(); refresh(); return;
            }
            String verification = approve && "agents".equals(kind) ? code.getText().toString().trim() : null;
            if (verification != null && verification.isEmpty()) { code.setError(getString(R.string.hub_pairing_code)); return; }
            view.dismiss(); submit(kind, id, decision, verification);
        }));
        view.show();
    }

    private void submit(String kind, String id, String decision, String code) {
        int version = ++generation;
        // Remove all old action buttons while this decision is in flight.
        content.removeAllViews(); status = addText(getString(R.string.hub_saving), 16);
        io.execute(() -> {
            if (!HubAuthorization.sameSession(bound, new SessionStore(this).load())) return;
            try {
                new ApiClient(bound.endpoint, bound.token).hubDecision(kind, id, decision, code);
                runOnUiThread(() -> { if (current() && generation == version) refresh(); });
            } catch (Exception error) {
                runOnUiThread(() -> {
                    if (!current() || generation != version) return;
                    status.setText(R.string.hub_decision_failed);
                    addButton(getString(R.string.refresh)).setOnClickListener(v -> refresh());
                });
            }
        });
    }

    private TextView addText(String value, int size) {
        TextView view = new TextView(this); view.setText(value); view.setTextSize(size);
        view.setPadding(0, 12, 0, 12); view.setTextIsSelectable(true); content.addView(view); return view;
    }
    private Button addButton(String label) {
        Button button = new Button(this); button.setText(label);
        content.addView(button, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        return button;
    }
}
