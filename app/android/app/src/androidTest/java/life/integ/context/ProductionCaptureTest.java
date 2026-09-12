package life.integ.context;

import android.os.Bundle;
import android.content.Context;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;

import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;
import androidx.test.uiautomator.Until;

import org.junit.Test;
import org.junit.runner.RunWith;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

@RunWith(AndroidJUnit4.class)
public class ProductionCaptureTest {
    @Test public void loginRecordAndSyncThroughProductionV2() throws Exception {
        Bundle arguments = InstrumentationRegistry.getArguments();
        String endpoint = required(arguments, "edcEndpoint");
        String username = required(arguments, "edcUsername");
        String password = required(arguments, "edcPassword");
        String projectId = required(arguments, "edcProjectId");

        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            waitFor("login form", 15_000, () -> editTexts(scenario).size() == 3);
            scenario.onActivity(activity -> {
                List<EditText> fields = find(activity.findViewById(android.R.id.content), EditText.class);
                fields.get(0).setText(endpoint);
                fields.get(1).setText(username);
                fields.get(2).setText(password);
                button(activity, activity.getString(R.string.sign_in)).performClick();
            });

            SessionStore store = new SessionStore(InstrumentationRegistry.getInstrumentation().getTargetContext());
            waitFor("authenticated session", 30_000, () -> store.load() != null);
            SessionStore.Session authenticated = store.load();
            assertNotNull(authenticated);
            ApiClient client = new ApiClient(authenticated.endpoint, authenticated.token);
            ApiClient.Project target = null;
            for (ApiClient.Project project : client.projects()) if (projectId.equals(project.id)) target = project;
            assertNotNull("isolated project must be accessible", target);
            store.selectProject(target.id, target.name);
            scenario.recreate();
            waitFor("record screen", 15_000, () -> hasButton(scenario, R.string.start_recording));

            SessionStore.Session selected = store.load();
            QueueStore queue = new QueueStore(InstrumentationRegistry.getInstrumentation().getTargetContext());
            Set<String> before = new HashSet<>();
            for (CaptureItem item : queue.list(selected)) before.add(item.id);

            scenario.onActivity(activity -> button(activity, activity.getString(R.string.start_recording)).performClick());
            waitFor("recorder start", 15_000, RecordingService::isActive);
            Thread.sleep(2_000);
            scenario.onActivity(activity -> button(activity, activity.getString(R.string.finish)).performClick());
            waitFor("recorder finish", 15_000, () -> !RecordingService.isActive());

            final CaptureItem[] captured = new CaptureItem[1];
            waitFor("new capture persisted", 15_000, () -> {
                for (CaptureItem item : queue.list(store.load())) {
                    if (!before.contains(item.id)) { captured[0] = item; return true; }
                }
                return false;
            });
            assertNotNull(captured[0]);
            assertTrue(captured[0].fileStillMatches());
            assertTrue(captured[0].sizeBytes > 0);
            waitFor("File and Event sync", 90_000, () -> {
                CaptureItem current = queue.get(captured[0].id);
                if (current != null) captured[0] = current;
                return current != null && CaptureItem.SYNCED.equals(current.state);
            });
            assertNotNull(captured[0].fileId);

            boolean found = false;
            for (ApiClient.RecordSummary event : client.appEvents(projectId)) {
                if (captured[0].id.equals(event.id)) {
                    assertEquals(captured[0].fileId, event.fileId);
                    assertEquals(captured[0].sha256, event.sha256);
                    assertEquals(captured[0].sizeBytes, event.size);
                    found = true;
                }
            }
            assertTrue("synced capture must be visible through V2 event query", found);
            System.out.println("EDC_ACCEPTED_CAPTURE id=" + captured[0].id + " file=" + captured[0].fileId
                    + " bytes=" + captured[0].sizeBytes + " sha256=" + captured[0].sha256);
        }
    }

    @Test public void offlineRecordingRemainsInPersistentQueue() throws Exception {
        Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
        SessionStore store = new SessionStore(context);
        assertNotNull("normal-flow test must establish a session first", store.load());
        QueueStore queue = new QueueStore(context);
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            waitFor("offline record screen", 15_000, () -> hasButton(scenario, R.string.start_recording));
            Set<String> before = new HashSet<>();
            for (CaptureItem item : queue.list(store.load())) before.add(item.id);
            scenario.onActivity(activity -> button(activity, activity.getString(R.string.start_recording)).performClick());
            waitFor("offline recorder start", 15_000, RecordingService::isActive);
            Thread.sleep(2_000);
            scenario.onActivity(activity -> button(activity, activity.getString(R.string.finish)).performClick());
            waitFor("offline recorder finish", 15_000, () -> !RecordingService.isActive());
            final CaptureItem[] captured = new CaptureItem[1];
            waitFor("offline capture persisted", 15_000, () -> {
                for (CaptureItem item : queue.list(store.load())) {
                    if (!before.contains(item.id)) { captured[0] = item; return true; }
                }
                return false;
            });
            Thread.sleep(3_000);
            captured[0] = queue.get(captured[0].id);
            assertTrue("transport failure must not report synced", !CaptureItem.SYNCED.equals(captured[0].state));
            assertTrue(captured[0].fileStillMatches());
            System.out.println("EDC_OFFLINE_CAPTURE id=" + captured[0].id + " state=" + captured[0].state
                    + " bytes=" + captured[0].sizeBytes + " sha256=" + captured[0].sha256);
        }
    }

    @Test public void pendingRecordingSyncsAfterTransportRecovery() throws Exception {
        String captureId = required(InstrumentationRegistry.getArguments(), "edcCaptureId");
        Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
        SessionStore store = new SessionStore(context);
        QueueStore queue = new QueueStore(context);
        assertNotNull(store.load());
        try (ActivityScenario<MainActivity> ignored = ActivityScenario.launch(MainActivity.class)) {
            UploadWorker.enqueuePending(context);
            waitFor("pending capture recovery", 90_000, () -> {
                CaptureItem current = queue.get(captureId);
                return current != null && CaptureItem.SYNCED.equals(current.state);
            });
            CaptureItem captured = queue.get(captureId);
            assertNotNull(captured.fileId);
            ApiClient client = new ApiClient(store.load().endpoint, store.load().token);
            boolean found = false;
            for (ApiClient.RecordSummary event : client.appEvents(captured.projectId)) {
                if (captureId.equals(event.id)) {
                    assertEquals(captured.fileId, event.fileId);
                    assertEquals(captured.sha256, event.sha256);
                    assertEquals(captured.sizeBytes, event.size);
                    found = true;
                }
            }
            assertTrue(found);
            System.out.println("EDC_RECOVERED_CAPTURE id=" + captured.id + " file=" + captured.fileId);
        }
    }

    @Test public void selectedTextFileUsesTheSameProductionQueue() throws Exception {
        Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
        context.getSharedPreferences("pending-import", Context.MODE_PRIVATE).edit().clear().commit();
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        device.pressBack();
        SessionStore store = new SessionStore(context);
        assertNotNull(store.load());
        QueueStore queue = new QueueStore(context);
        Set<String> before = new HashSet<>();
        for (CaptureItem item : queue.list(store.load())) before.add(item.id);
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            waitFor("record screen for file selection", 15_000, () -> hasButton(scenario, R.string.choose_file));
            scenario.onActivity(activity -> button(activity, activity.getString(R.string.choose_file)).performClick());
            UiObject2 file = device.wait(Until.findObject(By.text("edc-acceptance.txt")), 2_000);
            if (file == null) {
                UiObject2 roots = device.wait(Until.findObject(By.desc("Show roots")), 5_000);
                assertNotNull("document picker roots must be available", roots);
                roots.click();
                UiObject2 downloads = device.wait(Until.findObject(By.text("Downloads")), 5_000);
                assertNotNull("Downloads root must be available", downloads);
                downloads.click();
                file = device.wait(Until.findObject(By.text("edc-acceptance.txt")), 10_000);
            }
            assertNotNull("fixture must appear in the Android document picker", file);
            file.click();
            final CaptureItem[] selected = new CaptureItem[1];
            waitFor("selected file sync", 90_000, () -> {
                for (CaptureItem item : queue.list(store.load())) {
                    if (!before.contains(item.id)) {
                        selected[0] = item;
                        return CaptureItem.SYNCED.equals(item.state);
                    }
                }
                return false;
            });
            assertNotNull(selected[0].fileId);
            assertTrue(selected[0].mediaType.startsWith("text/"));
            boolean found = false;
            ApiClient client = new ApiClient(store.load().endpoint, store.load().token);
            for (ApiClient.RecordSummary event : client.appEvents(selected[0].projectId)) {
                if (selected[0].id.equals(event.id)) {
                    assertEquals(selected[0].fileId, event.fileId);
                    assertEquals(selected[0].sha256, event.sha256);
                    found = true;
                }
            }
            assertTrue(found);
            System.out.println("EDC_SELECTED_FILE id=" + selected[0].id + " file=" + selected[0].fileId);
        }
    }

    private static String required(Bundle arguments, String key) {
        String value = arguments.getString(key);
        if (value == null || value.isEmpty()) fail("missing instrumentation argument " + key);
        return value;
    }

    private interface Check { boolean ready() throws Exception; }
    private static void waitFor(String label, long timeoutMs, Check check) throws Exception {
        long deadline = System.currentTimeMillis() + timeoutMs;
        Throwable last = null;
        while (System.currentTimeMillis() < deadline) {
            try { if (check.ready()) return; } catch (Throwable error) { last = error; }
            Thread.sleep(250);
        }
        AssertionError failure = new AssertionError("timed out waiting for " + label);
        if (last != null) failure.initCause(last);
        throw failure;
    }

    private static boolean hasButton(ActivityScenario<MainActivity> scenario, int text) {
        final boolean[] result = {false};
        scenario.onActivity(activity -> result[0] = findButton(activity, activity.getString(text)) != null);
        return result[0];
    }

    private static List<EditText> editTexts(ActivityScenario<MainActivity> scenario) {
        List<EditText> result = new ArrayList<>();
        scenario.onActivity(activity -> result.addAll(find(activity.findViewById(android.R.id.content), EditText.class)));
        return result;
    }

    private static Button button(MainActivity activity, String text) {
        Button result = findButton(activity, text);
        if (result == null) throw new AssertionError("button not found: " + text);
        return result;
    }

    private static Button findButton(MainActivity activity, String text) {
        for (Button button : find(activity.findViewById(android.R.id.content), Button.class)) {
            if (text.contentEquals(button.getText())) return button;
        }
        return null;
    }

    private static <T extends View> List<T> find(View root, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(root)) result.add(type.cast(root));
        if (root instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) root;
            for (int i = 0; i < group.getChildCount(); i++) result.addAll(find(group.getChildAt(i), type));
        }
        return result;
    }
}
