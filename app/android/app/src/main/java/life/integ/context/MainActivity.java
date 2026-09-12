package life.integ.context;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Typeface;
import android.media.MediaPlayer;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.MediaStore;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;
import android.widget.Toast;

import androidx.core.content.FileProvider;
import androidx.core.content.ContextCompat;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.time.Instant;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    public static final String ACTION_CAPTURE_CHANGED = "life.integ.context.CAPTURE_CHANGED";
    private static final int REQUEST_RECORD = 301;
    private static final int REQUEST_FILE = 302;
    private static final int REQUEST_PHOTO = 303;
    private static final long MAX_FILE_BYTES = 50L * 1024 * 1024;

    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private SessionStore sessions;
    private QueueStore queue;
    private SessionStore.Session session;
    private List<ApiClient.Project> projects = new ArrayList<>();
    private FrameLayout content;
    private LinearLayout navigation;
    private TextView transientStatus;
    private File pendingPhoto;
    private String pendingPhotoId;
    private boolean receiverRegistered;
    private MediaPlayer player;

    private final BroadcastReceiver changes = new BroadcastReceiver() {
        @Override public void onReceive(Context context, Intent intent) {
            showRecord();
            String error = intent.getStringExtra("error");
            if (error != null && !error.isEmpty()) toast(getString(R.string.capture_failed, error));
        }
    };

    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        sessions = new SessionStore(this);
        queue = new QueueStore(this);
        if (!RecordingService.isActive()) RecordingService.recoverInterrupted(this);
        session = sessions.load();
        buildShell();
        restorePendingImport();
        if (session == null) {
            showLogin();
        } else if (session.projectId != null) {
            buildNavigation();
            showRecord();
            refreshProjectsInBackground();
            UploadWorker.enqueuePending(this);
        } else {
            loadProjects(true);
        }
    }

    @Override protected void onStart() {
        super.onStart();
        if (!receiverRegistered) {
            IntentFilter filter = new IntentFilter(ACTION_CAPTURE_CHANGED);
            ContextCompat.registerReceiver(this, changes, filter, ContextCompat.RECEIVER_NOT_EXPORTED);
            receiverRegistered = true;
        }
    }

    @Override protected void onStop() {
        if (receiverRegistered) { unregisterReceiver(changes); receiverRegistered = false; }
        super.onStop();
    }

    @Override protected void onDestroy() {
        stopPlayback();
        io.shutdownNow();
        queue.close();
        super.onDestroy();
    }

    private void buildShell() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xfff7f8fa);
        content = new FrameLayout(this);
        root.addView(content, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        navigation = new LinearLayout(this);
        navigation.setOrientation(LinearLayout.HORIZONTAL);
        navigation.setGravity(Gravity.CENTER);
        navigation.setPadding(dp(8), dp(4), dp(8), dp(8));
        root.addView(navigation, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        setContentView(root);
    }

    private void showLogin() {
        navigation.setVisibility(View.GONE);
        LinearLayout form = page();
        form.addView(title(getString(R.string.login_title)));
        EditText endpoint = field(getString(R.string.endpoint), InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        EditText username = field(getString(R.string.username), InputType.TYPE_CLASS_TEXT);
        EditText password = field(getString(R.string.password), InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        SessionStore.Session previous = sessions.load();
        if (previous != null) { endpoint.setText(previous.endpoint); username.setText(previous.username); }
        form.addView(endpoint); form.addView(username); form.addView(password);
        transientStatus = text(""); form.addView(transientStatus);
        Button login = button(getString(R.string.sign_in)); form.addView(login);
        login.setOnClickListener(v -> {
            final String normalized;
            try { normalized = SessionStore.normalizeEndpoint(endpoint.getText().toString()); }
            catch (IllegalArgumentException invalid) { transientStatus.setText(R.string.invalid_https); return; }
            login.setEnabled(false);
            transientStatus.setText("");
            io.execute(() -> {
                try {
                    ApiClient.Login result = ApiClient.login(normalized, username.getText().toString().trim(), password.getText().toString());
                    sessions.save(normalized, result.userId, result.username, result.token, result.expiresAt);
                    session = sessions.load();
                    runUi(() -> loadProjects(true));
                } catch (Exception error) {
                    runUi(() -> { login.setEnabled(true); transientStatus.setText(getString(R.string.login_failed, message(error))); });
                }
            });
        });
        replace(form);
    }

    private void loadProjects(boolean openRecord) {
        if (session == null) { showLogin(); return; }
        navigation.setVisibility(View.GONE);
        LinearLayout loading = page();
        loading.addView(title(getString(R.string.app_name)));
        transientStatus = text(getString(R.string.refresh)); loading.addView(transientStatus);
        replace(loading);
        io.execute(() -> {
            try {
                List<ApiClient.Project> loaded = new ApiClient(session.endpoint, session.token).projects();
                ApiClient.Project selected = chooseProject(loaded, session);
                if (selected != null && (session.projectId == null || !selected.id.equals(session.projectId))) {
                    sessions.selectProject(selected.id, selected.name, selected.timezone);
                    session = sessions.load();
                }
                projects = loaded;
                UploadWorker.enqueuePending(this);
                runUi(() -> { buildNavigation(); if (openRecord) showRecord(); else showMe(); });
            } catch (Exception error) {
                runUi(() -> {
                    session = sessions.load();
                    if (session != null && session.projectId != null) {
                        buildNavigation(); showRecord(); return;
                    }
                    transientStatus.setText(getString(R.string.load_failed, message(error)));
                    Button retry = button(getString(R.string.retry)); retry.setOnClickListener(v -> loadProjects(openRecord)); loading.addView(retry);
                });
            }
        });
    }

    private void refreshProjectsInBackground() {
        SessionStore.Session expected = sessions.load();
        if (expected == null) return;
        io.execute(() -> {
            try {
                List<ApiClient.Project> loaded = new ApiClient(expected.endpoint, expected.token).projects();
                ApiClient.Project selected = chooseProject(loaded, expected);
                if (selected != null && !selected.id.equals(expected.projectId)) {
                    sessions.selectProject(selected.id, selected.name, selected.timezone);
                    session = sessions.load();
                }
                projects = loaded;
            } catch (ApiClient.ApiException error) {
                if (error.needsLogin()) runUi(this::showLogin);
            } catch (Exception ignored) {
                // Offline startup keeps the frozen local project and queue available.
            }
        });
    }

    private ApiClient.Project chooseProject(List<ApiClient.Project> loaded, SessionStore.Session current) {
        for (ApiClient.Project p : loaded) if (p.id.equals(current.projectId)) return p;
        for (ApiClient.Project p : loaded) if (p.ownerUserId.equals(current.userId) && "我的记录".equals(p.name)) return p;
        for (ApiClient.Project p : loaded) if (p.ownerUserId.equals(current.userId)) return p;
        return loaded.isEmpty() ? null : loaded.get(0);
    }

    private void buildNavigation() {
        navigation.removeAllViews();
        navigation.setVisibility(View.VISIBLE);
        Button record = navButton(getString(R.string.record)); record.setOnClickListener(v -> showRecord());
        Button review = navButton(getString(R.string.review)); review.setOnClickListener(v -> showReview());
        Button me = navButton(getString(R.string.me)); me.setOnClickListener(v -> showMe());
        navigation.addView(record); navigation.addView(review); navigation.addView(me);
    }

    private void showRecord() {
        if (session == null) { showLogin(); return; }
        session = sessions.load();
        LinearLayout page = page();
        page.addView(title(getString(R.string.record)));
        page.addView(text(session.projectName == null ? getString(R.string.no_project) : getString(R.string.current_project, session.projectName)));
        TextView state = text(RecordingService.isPaused() ? getString(R.string.recording_paused)
                : RecordingService.isActive() ? getString(R.string.recording_now) : "");
        state.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        page.addView(state);
        LinearLayout primary = row();
        Button start = button(getString(R.string.start_recording));
        Button pause = button(RecordingService.isPaused() ? getString(R.string.resume) : getString(R.string.pause));
        Button finish = button(getString(R.string.finish));
        start.setEnabled(!RecordingService.isActive() && session.projectId != null);
        pause.setEnabled(RecordingService.isActive()); finish.setEnabled(RecordingService.isActive());
        start.setOnClickListener(v -> requestRecording());
        pause.setOnClickListener(v -> sendRecordingAction(RecordingService.isPaused() ? RecordingService.ACTION_RESUME : RecordingService.ACTION_PAUSE));
        finish.setOnClickListener(v -> sendRecordingAction(RecordingService.ACTION_STOP));
        primary.addView(start); primary.addView(pause); primary.addView(finish); page.addView(primary);
        LinearLayout imports = row();
        Button photo = button(getString(R.string.take_photo)); photo.setOnClickListener(v -> takePhoto());
        Button file = button(getString(R.string.choose_file)); file.setOnClickListener(v -> chooseFile());
        imports.addView(photo); imports.addView(file); page.addView(imports);
        Button refresh = button(getString(R.string.refresh)); refresh.setOnClickListener(v -> showRecord()); page.addView(refresh);

        List<CaptureItem> captures = queue.list(session);
        if (captures.isEmpty()) page.addView(text(getString(R.string.no_records)));
        for (CaptureItem item : captures) page.addView(captureView(item));
        replace(page);
        loadServerRecords(page, session);
    }

    private View captureView(CaptureItem item) {
        LinearLayout box = card();
        box.addView(strong(item.filename));
        box.addView(text(status(item) + " · " + humanBytes(item.sizeBytes)));
        if (item.lastError != null && !item.lastError.isEmpty()) box.addView(text(item.lastError));
        if (CaptureItem.INTERRUPTED.equals(item.state)) {
            Button retry = button(getString(R.string.retry));
            retry.setOnClickListener(v -> { queue.retryInterrupted(item.id); UploadWorker.enqueue(this, item.id); showRecord(); });
            box.addView(retry);
        }
        if (item.mediaType.startsWith("audio/") && item.fileStillMatches()) {
            Button play = button(getString(R.string.play));
            play.setOnClickListener(v -> playFile(new File(item.localPath)));
            box.addView(play);
        }
        return box;
    }

    private void loadServerRecords(LinearLayout target, SessionStore.Session expected) {
        if (expected.projectId == null) return;
        io.execute(() -> {
            try {
                List<ApiClient.RecordSummary> records = new ApiClient(expected.endpoint, expected.token).appEvents(expected.projectId);
                runUi(() -> {
                    if (!sameView(expected)) return;
                    if (!records.isEmpty()) target.addView(strong("Server"));
                    for (ApiClient.RecordSummary record : records) {
                        LinearLayout box = card();
                        box.addView(strong(record.filename.isEmpty() ? record.id : record.filename));
                        box.addView(text(getString(R.string.recorded_by, record.actor.displayName(), record.actor.id)));
                        box.addView(text(record.recordedAt + " · " + humanBytes(record.size)));
                        box.addView(text(getString(R.string.event_source, record.sourceChannel)));
                        if (record.mediaType.startsWith("audio/") && !record.fileId.isEmpty()
                                && !record.sha256.isEmpty() && record.size > 0) {
                            Button play = button(getString(R.string.play));
                            play.setOnClickListener(v -> downloadAndPlay(expected, record));
                            box.addView(play);
                        }
                        target.addView(box);
                        loadTranscripts(box, expected, record.id);
                    }
                });
            } catch (Exception error) {
                runUi(() -> { if (sameView(expected)) target.addView(text(getString(R.string.load_failed, message(error)))); });
            }
        });
    }

    private void loadTranscripts(LinearLayout target, SessionStore.Session expected, String eventId) {
        io.execute(() -> {
            try {
                List<String> values = new ApiClient(expected.endpoint, expected.token).derivedTexts(expected.projectId, eventId);
                runUi(() -> {
                    if (!sameView(expected)) return;
                    for (String value : values) target.addView(text(getString(R.string.transcript, value)));
                });
            } catch (Exception ignored) {
                // The original capture remains usable while processing is pending or failed.
            }
        });
    }

    private void showReview() {
        session = sessions.load();
        if (session == null) { showLogin(); return; }
        LinearLayout page = page();
        page.addView(title(getString(R.string.review)));
        page.addView(text(session.projectName == null ? getString(R.string.no_project) : getString(R.string.current_project, session.projectName)));
        transientStatus = text(getString(R.string.refresh)); page.addView(transientStatus);
        replace(page);
        SessionStore.Session expected = session;
        if (expected.projectId == null) { transientStatus.setText(R.string.no_project); return; }
        io.execute(() -> {
            try {
                List<ApiClient.ReviewState> reviews = new ApiClient(expected.endpoint, expected.token).dailyReviews(expected.projectId);
                runUi(() -> {
                    if (!sameView(expected)) return;
                    transientStatus.setText(reviews.isEmpty() ? getString(R.string.no_reviews) : "");
                    for (ApiClient.ReviewState review : reviews) page.addView(reviewView(review, expected));
                });
            } catch (Exception error) {
                runUi(() -> { if (sameView(expected)) transientStatus.setText(getString(R.string.load_failed, message(error))); });
            }
        });
    }

    private View reviewView(ApiClient.ReviewState review, SessionStore.Session expected) {
        LinearLayout box = card();
        box.addView(strong(review.key));
        box.addView(text(getString(R.string.review_coverage, review.version, review.basedOnSequence, review.lag)));
        box.addView(text(review.text));
        for (String ref : review.refs) {
            Button source = button("↗ " + ref);
            source.setContentDescription("Open source " + ref);
            source.setOnClickListener(v -> loadSource(expected, ref));
            box.addView(source);
        }
        return box;
    }

    void loadSource(SessionStore.Session expected, String eventId) {
        io.execute(() -> {
            try {
                ApiClient.EventDetail detail = new ApiClient(expected.endpoint, expected.token).eventDetail(expected.projectId, eventId);
                String source = getString(R.string.source_detail, detail.actor.displayName(), detail.actor.id,
                        detail.actor.type, detail.recordedAt, detail.source, detail.content);
                runUi(() -> { if (sameView(expected)) new android.app.AlertDialog.Builder(this).setTitle(eventId).setMessage(source).setPositiveButton(android.R.string.ok, null).show(); });
            } catch (Exception error) { runUi(() -> toast(getString(R.string.load_failed, message(error)))); }
        });
    }

    private void downloadAndPlay(SessionStore.Session expected, ApiClient.RecordSummary record) {
        io.execute(() -> {
            try {
                File directory = new File(getCacheDir(), "authenticated-media");
                if (!directory.exists() && !directory.mkdirs()) throw new java.io.IOException("could not create media cache");
                File target = new File(directory, record.fileId);
                new ApiClient(expected.endpoint, expected.token).downloadFile(expected.projectId, record.fileId,
                        record.sha256, record.size, target);
                runUi(() -> { if (sameView(expected)) playFile(target); });
            } catch (Exception error) { runUi(() -> toast(getString(R.string.load_failed, message(error)))); }
        });
    }

    private void playFile(File file) {
        stopPlayback();
        try {
            player = new MediaPlayer();
            player.setDataSource(file.getAbsolutePath());
            player.setOnCompletionListener(done -> stopPlayback());
            player.prepare();
            player.start();
        } catch (Exception error) {
            stopPlayback(); toast(getString(R.string.load_failed, message(error)));
        }
    }

    private void stopPlayback() {
        if (player != null) {
            try { player.stop(); } catch (RuntimeException ignored) {}
            player.release(); player = null;
        }
    }

    private void showMe() {
        session = sessions.load();
        if (session == null) { showLogin(); return; }
        LinearLayout page = page();
        page.addView(title(getString(R.string.me)));
        page.addView(text(getString(R.string.account, session.username)));
        page.addView(text(getString(R.string.server, session.endpoint)));
        page.addView(text(getString(R.string.queue_count, queue.pendingCount(session))));
        if (!projects.isEmpty()) {
            Spinner spinner = new Spinner(this);
            ArrayAdapter<ApiClient.Project> adapter = new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item, projects);
            spinner.setAdapter(adapter);
            int selected = 0;
            for (int i = 0; i < projects.size(); i++) if (projects.get(i).id.equals(session.projectId)) selected = i;
            spinner.setSelection(selected, false);
            spinner.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener() {
                @Override public void onItemSelected(AdapterView<?> parent, View view, int position, long id) {
                    ApiClient.Project project = projects.get(position);
                    SessionStore.Session current = sessions.load();
                    if (current != null && !project.id.equals(current.projectId)) {
                        sessions.selectProject(project.id, project.name, project.timezone); session = sessions.load(); UploadWorker.enqueuePending(MainActivity.this);
                    }
                }
                @Override public void onNothingSelected(AdapterView<?> parent) {}
            });
            page.addView(spinner);
        }
        ApiClient.Project selectedProject = selectedProject();
        String currentTimezone = selectedProject == null ? session.projectTimezone : selectedProject.timezone;
        page.addView(text(getString(R.string.project_timezone, currentTimezone == null ? "" : currentTimezone)));
        if (selectedProject != null && session.userId.equals(selectedProject.ownerUserId)) {
            EditText timezone = field(getString(R.string.timezone_hint), InputType.TYPE_CLASS_TEXT);
            timezone.setText(selectedProject.timezone);
            page.addView(timezone);
            Button saveTimezone = button(getString(R.string.save_timezone));
            saveTimezone.setOnClickListener(v -> updateTimezone(selectedProject, timezone, saveTimezone));
            page.addView(saveTimezone);
        }
        EditText projectName = field(getString(R.string.new_project_name), InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        page.addView(projectName);
        Button createProject = button(getString(R.string.create_project));
        createProject.setOnClickListener(v -> createProject(projectName, createProject));
        page.addView(createProject);
        TextView plugins = text(getString(R.string.plugins) + ": …"); page.addView(plugins);
        Button logout = button(getString(R.string.logout));
        logout.setOnClickListener(v -> {
            SessionStore.Session old = sessions.load(); sessions.clear(); session = null;
            if (old != null) io.execute(() -> new ApiClient(old.endpoint, old.token).logout());
            showLogin();
        });
        page.addView(logout);
        replace(page);
        loadPlugins(plugins, session);
    }

    private ApiClient.Project selectedProject() {
        if (session == null || session.projectId == null) return null;
        for (ApiClient.Project project : projects) if (session.projectId.equals(project.id)) return project;
        return null;
    }

    private void updateTimezone(ApiClient.Project project, EditText field, Button button) {
        String timezone = field.getText().toString().trim();
        try { ZoneId.of(timezone); }
        catch (RuntimeException invalid) { toast(getString(R.string.invalid_timezone)); return; }
        button.setEnabled(false);
        SessionStore.Session expected = sessions.load();
        io.execute(() -> {
            try {
                ApiClient.Project updated = new ApiClient(expected.endpoint, expected.token).updateProjectTimezone(project.id, timezone);
                runUi(() -> {
                    if (!sameView(expected)) return;
                    List<ApiClient.Project> revised = new ArrayList<>(projects);
                    for (int i = 0; i < revised.size(); i++) if (revised.get(i).id.equals(updated.id)) revised.set(i, updated);
                    projects = revised;
                    sessions.selectProject(updated.id, updated.name, updated.timezone);
                    session = sessions.load();
                    toast(getString(R.string.timezone_saved));
                    showMe();
                });
            } catch (Exception error) {
                runUi(() -> { button.setEnabled(true); toast(getString(R.string.load_failed, message(error))); });
            }
        });
    }

    private void createProject(EditText field, Button button) {
        String name = field.getText().toString().trim();
        if (name.isEmpty()) { toast(getString(R.string.project_name_required)); return; }
        String timezone = deviceTimezone();
        button.setEnabled(false);
        SessionStore.Session expected = sessions.load();
        io.execute(() -> {
            try {
                ApiClient.Project created = new ApiClient(expected.endpoint, expected.token).createProject(name, timezone);
                runUi(() -> {
                    SessionStore.Session current = sessions.load();
                    if (current == null || !expected.endpoint.equals(current.endpoint) || !expected.userId.equals(current.userId)) return;
                    List<ApiClient.Project> revised = new ArrayList<>(projects); revised.add(created); projects = revised;
                    sessions.selectProject(created.id, created.name, created.timezone); session = sessions.load();
                    toast(getString(R.string.project_created));
                    UploadWorker.enqueuePending(this);
                    showMe();
                });
            } catch (Exception error) {
                runUi(() -> { button.setEnabled(true); toast(getString(R.string.load_failed, message(error))); });
            }
        });
    }

    static String deviceTimezone() {
        String id = ZoneId.systemDefault().getId();
        return id.contains("/") ? id : "Etc/UTC";
    }

    private void loadPlugins(TextView target, SessionStore.Session expected) {
        if (expected.projectId == null) return;
        io.execute(() -> {
            try {
                List<String> values = new ApiClient(expected.endpoint, expected.token).plugins(expected.projectId);
                runUi(() -> { if (sameView(expected)) target.setText(getString(R.string.plugins) + ":\n" + String.join("\n", values)); });
            } catch (Exception error) { runUi(() -> { if (sameView(expected)) target.setText(getString(R.string.load_failed, message(error))); }); }
        });
    }

    private void requestRecording() {
        List<String> permissions = new ArrayList<>();
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            permissions.add(Manifest.permission.RECORD_AUDIO);
        }
        if (Build.VERSION.SDK_INT >= 33
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            permissions.add(Manifest.permission.POST_NOTIFICATIONS);
        }
        if (permissions.isEmpty()) { startRecording(); return; }
        requestPermissions(permissions.toArray(new String[0]), REQUEST_RECORD);
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode != REQUEST_RECORD) return;
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            if (Build.VERSION.SDK_INT >= 33
                    && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                toast(getString(R.string.notification_denied));
            }
            startRecording();
        }
        else toast(getString(R.string.microphone_denied));
    }

    private void startRecording() {
        session = sessions.load();
        if (session == null || session.projectId == null) { toast(getString(R.string.no_project)); return; }
        Intent intent = new Intent(this, RecordingService.class).setAction(RecordingService.ACTION_START)
                .putExtra(RecordingService.EXTRA_CAPTURE_ID, StableIds.uuidV7().toString())
                .putExtra("endpoint", session.endpoint).putExtra("account_id", session.userId)
                .putExtra("project_id", session.projectId).putExtra("occurred_at", RecordingService.now());
        startForegroundService(intent);
    }

    private void sendRecordingAction(String action) { startService(new Intent(this, RecordingService.class).setAction(action)); }

    private void takePhoto() {
        if (PendingCapture.load(this) != null) { toast("A capture is already being saved."); return; }
        session = sessions.load();
        if (session == null || session.projectId == null) { toast(getString(R.string.no_project)); return; }
        pendingPhotoId = StableIds.uuidV7().toString();
        File directory = new File(getFilesDir(), "captures"); if (!directory.exists()) directory.mkdirs();
        pendingPhoto = new File(directory, pendingPhotoId + ".jpg");
        if (!PendingCapture.save(this, new PendingCapture("photo", pendingPhotoId, pendingPhoto.getAbsolutePath(), null,
                session.endpoint, session.userId, session.projectId, RecordingService.now()))) {
            toast(getString(R.string.capture_failed, "capture journal unavailable")); return;
        }
        Uri output = FileProvider.getUriForFile(this, getPackageName() + ".files", pendingPhoto);
        Intent intent = new Intent(MediaStore.ACTION_IMAGE_CAPTURE).putExtra(MediaStore.EXTRA_OUTPUT, output)
                .addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION | Intent.FLAG_GRANT_READ_URI_PERMISSION);
        if (intent.resolveActivity(getPackageManager()) == null) { toast(getString(R.string.capture_failed, "camera unavailable")); return; }
        startActivityForResult(intent, REQUEST_PHOTO);
    }

    private void chooseFile() {
        if (PendingCapture.load(this) != null) { toast("A capture is already being saved."); return; }
        session = sessions.load();
        if (session == null || session.projectId == null) { toast(getString(R.string.no_project)); return; }
        if (!PendingCapture.save(this, new PendingCapture("file", StableIds.uuidV7().toString(), null, null,
                session.endpoint, session.userId, session.projectId, RecordingService.now()))) {
            toast(getString(R.string.capture_failed, "capture journal unavailable")); return;
        }
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*")
                .putExtra(Intent.EXTRA_MIME_TYPES, new String[]{"audio/*", "image/jpeg", "image/png", "text/*"});
        startActivityForResult(intent, REQUEST_FILE);
    }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQUEST_PHOTO) {
            PendingCapture pending = PendingCapture.load(this);
            if (resultCode == RESULT_OK && pending != null && "photo".equals(pending.kind)) {
                pending = pending.withResultReady();
                if (!PendingCapture.save(this, pending)) { toast(getString(R.string.capture_failed, "capture journal unavailable")); return; }
                enqueueOwnedFile(pending, new File(pending.path), "image/jpeg", 0);
            }
            else if (pending != null && pending.path != null) new File(pending.path).delete();
            if (resultCode != RESULT_OK && pending != null) PendingCapture.clear(this, pending.id);
            pendingPhoto = null; pendingPhotoId = null;
        } else if (requestCode == REQUEST_FILE && resultCode == RESULT_OK && data != null && data.getData() != null) {
            PendingCapture pending = PendingCapture.load(this);
            if (pending == null || !"file".equals(pending.kind)) return;
            Uri uri = data.getData();
            try { getContentResolver().takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION); }
            catch (SecurityException ignored) {}
            pending = pending.withUri(uri.toString());
            if (!PendingCapture.save(this, pending)) { toast(getString(R.string.capture_failed, "capture journal unavailable")); return; }
            importUri(uri, pending);
        } else if (requestCode == REQUEST_FILE) {
            PendingCapture pending = PendingCapture.load(this);
            if (pending != null) PendingCapture.clear(this, pending.id);
        }
    }

    private void importUri(Uri uri, PendingCapture pending) {
        io.execute(() -> {
            String id = pending.id;
            String mime = getContentResolver().getType(uri);
            if (mime == null) mime = "application/octet-stream";
            if (!supportedMime(mime)) {
                PendingCapture.clear(this, pending.id);
                runUi(() -> toast(getString(R.string.capture_failed, "unsupported media type"))); return;
            }
            File directory = new File(getFilesDir(), "captures"); if (!directory.exists()) directory.mkdirs();
            String extension = mime.startsWith("audio/") ? ".audio" : mime.startsWith("image/") ? ".image" : ".txt";
            File target = new File(directory, id + extension);
            try (InputStream input = getContentResolver().openInputStream(uri); FileOutputStream output = new FileOutputStream(target)) {
                if (input == null) throw new java.io.IOException("file unavailable");
                byte[] buffer = new byte[64 * 1024]; long total = 0; int read;
                while ((read = input.read(buffer)) != -1) {
                    total += read; if (total > MAX_FILE_BYTES) throw new java.io.IOException("file exceeds 50 MiB");
                    output.write(buffer, 0, read);
                }
            } catch (Exception error) {
                target.delete(); runUi(() -> toast(getString(R.string.capture_failed, message(error)))); return;
            }
            final String frozenMime = mime;
            enqueueOwnedFileInBackground(pending, target, frozenMime, 0);
        });
    }

    private void enqueueOwnedFile(PendingCapture pending, File file, String mime, long duration) {
        io.execute(() -> enqueueOwnedFileInBackground(pending, file, mime, duration));
    }

    private void enqueueOwnedFileInBackground(PendingCapture pending, File file, String mime, long duration) {
        try {
            FileIdentity identity = FileIdentity.read(file);
            if (identity.size > MAX_FILE_BYTES) throw new java.io.IOException("file exceeds 50 MiB");
            CaptureItem item = new CaptureItem(pending.id, pending.endpoint, pending.accountId, pending.projectId,
                    file.getAbsolutePath(), file.getName(), mime, identity.size, identity.sha256, duration,
                    pending.occurredAt, null, CaptureItem.READY, null, System.currentTimeMillis());
            queue.insert(item); PendingCapture.clear(this, pending.id); UploadWorker.enqueue(this, pending.id); runUi(this::showRecord);
        } catch (Exception error) { runUi(() -> toast(getString(R.string.capture_failed, message(error)))); }
    }

    private void restorePendingImport() {
        PendingCapture pending = PendingCapture.load(this);
        if (pending == null) return;
        if ("photo".equals(pending.kind) && pending.path != null) {
            if (pending.resultReady) enqueueOwnedFile(pending, new File(pending.path), "image/jpeg", 0);
            else { pendingPhotoId = pending.id; pendingPhoto = new File(pending.path); }
        } else if ("file".equals(pending.kind) && pending.uri != null) {
            importUri(Uri.parse(pending.uri), pending);
        }
    }

    private static boolean supportedMime(String mime) {
        return mime.startsWith("text/") || "audio/mp4".equals(mime) || "audio/mpeg".equals(mime)
                || "audio/wav".equals(mime) || "image/jpeg".equals(mime) || "image/png".equals(mime);
    }

    private boolean sameView(SessionStore.Session expected) {
        SessionStore.Session current = sessions.load();
        return current != null && expected.endpoint.equals(current.endpoint) && expected.userId.equals(current.userId)
                && java.util.Objects.equals(expected.projectId, current.projectId);
    }

    private String status(CaptureItem item) {
        switch (item.state) {
            case CaptureItem.READY: return getString(R.string.waiting_sync);
            case CaptureItem.UPLOADING: return getString(R.string.uploading);
            case CaptureItem.EVENT_PENDING: return getString(R.string.uploading);
            case CaptureItem.SYNCED: return getString(R.string.synced);
            case CaptureItem.INTERRUPTED: return getString(R.string.interrupted);
            case CaptureItem.AUTH_REQUIRED: return getString(R.string.waiting_sync);
            default: return getString(R.string.failed);
        }
    }

    private LinearLayout page() {
        LinearLayout body = new LinearLayout(this); body.setOrientation(LinearLayout.VERTICAL);
        body.setPadding(dp(20), dp(20), dp(20), dp(28));
        ScrollView scroll = new ScrollView(this); scroll.addView(body);
        body.setTag(scroll); return body;
    }

    private void replace(LinearLayout page) {
        content.removeAllViews();
        Object scroll = page.getTag();
        content.addView(scroll instanceof ScrollView ? (ScrollView) scroll : page,
                new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    private LinearLayout card() {
        LinearLayout result = new LinearLayout(this); result.setOrientation(LinearLayout.VERTICAL);
        result.setPadding(dp(14), dp(12), dp(14), dp(12)); result.setBackgroundColor(0xffffffff);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        params.setMargins(0, dp(6), 0, dp(6)); result.setLayoutParams(params); return result;
    }

    private LinearLayout row() { LinearLayout row = new LinearLayout(this); row.setOrientation(LinearLayout.HORIZONTAL); return row; }
    private TextView title(String value) { TextView v = text(value); v.setTextSize(26); v.setTypeface(Typeface.DEFAULT_BOLD); v.setPadding(0, 0, 0, dp(12)); return v; }
    private TextView strong(String value) { TextView v = text(value); v.setTypeface(Typeface.DEFAULT_BOLD); return v; }
    private TextView text(String value) { TextView v = new TextView(this); v.setText(value); v.setTextSize(16); v.setTextColor(0xff17212b); v.setPadding(0, dp(4), 0, dp(4)); return v; }
    private EditText field(String hint, int inputType) { EditText v = new EditText(this); v.setHint(hint); v.setInputType(inputType); v.setSingleLine(true); return v; }
    private Button button(String value) { Button v = new Button(this); v.setText(value); v.setAllCaps(false); return v; }
    private Button navButton(String value) { Button v = button(value); v.setLayoutParams(new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1)); return v; }
    private int dp(int value) { return Math.round(value * getResources().getDisplayMetrics().density); }
    private void runUi(Runnable action) { runOnUiThread(() -> { if (!isDestroyed()) action.run(); }); }
    private void toast(String message) { Toast.makeText(this, message, Toast.LENGTH_LONG).show(); }
    private static String message(Throwable error) { return error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage(); }
    private static String humanBytes(long size) {
        if (size < 1024) return size + " B";
        if (size < 1024 * 1024) return String.format(Locale.ROOT, "%.1f KiB", size / 1024.0);
        return String.format(Locale.ROOT, "%.1f MiB", size / 1048576.0);
    }

    private static final class PendingCapture {
        private static final String PREFS = "pending-import";
        final String kind, id, path, uri, endpoint, accountId, projectId, occurredAt;
        final boolean resultReady;
        PendingCapture(String kind, String id, String path, String uri, String endpoint,
                       String accountId, String projectId, String occurredAt) {
            this(kind, id, path, uri, endpoint, accountId, projectId, occurredAt, false);
        }
        PendingCapture(String kind, String id, String path, String uri, String endpoint,
                       String accountId, String projectId, String occurredAt, boolean resultReady) {
            this.kind = kind; this.id = id; this.path = path; this.uri = uri; this.endpoint = endpoint;
            this.accountId = accountId; this.projectId = projectId; this.occurredAt = occurredAt; this.resultReady = resultReady;
        }
        PendingCapture withUri(String value) {
            return new PendingCapture(kind, id, path, value, endpoint, accountId, projectId, occurredAt, true);
        }
        PendingCapture withResultReady() { return new PendingCapture(kind, id, path, uri, endpoint, accountId, projectId, occurredAt, true); }
        static boolean save(Context context, PendingCapture value) {
            return context.getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString("kind", value.kind)
                    .putString("id", value.id).putString("path", value.path).putString("uri", value.uri)
                    .putString("endpoint", value.endpoint).putString("account_id", value.accountId)
                    .putString("project_id", value.projectId).putString("occurred_at", value.occurredAt)
                    .putBoolean("result_ready", value.resultReady).commit();
        }
        static PendingCapture load(Context context) {
            SharedPreferences p = context.getSharedPreferences(PREFS, MODE_PRIVATE);
            String kind = p.getString("kind", null), id = p.getString("id", null);
            String endpoint = p.getString("endpoint", null), account = p.getString("account_id", null);
            String project = p.getString("project_id", null), occurred = p.getString("occurred_at", null);
            if (kind == null || id == null || endpoint == null || account == null || project == null || occurred == null) return null;
            return new PendingCapture(kind, id, p.getString("path", null), p.getString("uri", null), endpoint, account, project, occurred,
                    p.getBoolean("result_ready", false));
        }
        static void clear(Context context, String expectedId) {
            SharedPreferences p = context.getSharedPreferences(PREFS, MODE_PRIVATE);
            if (expectedId.equals(p.getString("id", null))) p.edit().clear().commit();
        }
    }
}
