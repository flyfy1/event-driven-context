package life.integ.context;

import android.net.Uri;
import android.os.Build;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

final class ApiClient {
    static final class ApiException extends IOException {
        final int status;
        final String code;
        ApiException(int status, String code, String message) {
            super(message == null || message.isEmpty() ? code : message);
            this.status = status; this.code = code;
        }
        boolean needsLogin() { return status == 401 || "unauthenticated".equals(code); }
        boolean retryable() { return status == 0 || status == 408 || status == 429 || status >= 500; }
    }

    static final class Login {
        final String userId, username, token;
        final long expiresAt;
        Login(String userId, String username, String token, long expiresAt) {
            this.userId = userId; this.username = username; this.token = token; this.expiresAt = expiresAt;
        }
    }

    static final class Project {
        final String id, name, ownerUserId, timezone;
        Project(String id, String name, String ownerUserId, String timezone) {
            this.id = id; this.name = name; this.ownerUserId = ownerUserId; this.timezone = timezone;
        }
        @Override public String toString() { return name; }
    }

    static final class FileInfo {
        final String id, projectId, sha256;
        final long size;
        FileInfo(String id, String projectId, String sha256, long size) {
            this.id = id; this.projectId = projectId; this.sha256 = sha256; this.size = size;
        }
    }

    static final class EventResult {
        final String status;
        final long sequence;
        EventResult(String status, long sequence) { this.status = status; this.sequence = sequence; }
    }

    static final class RecordSummary {
        final String id, type, recordedAt, fileId, filename, mediaType, sha256;
        final long size;
        RecordSummary(String id, String type, String recordedAt, String fileId, String filename, String mediaType, String sha256, long size) {
            this.id = id; this.type = type; this.recordedAt = recordedAt;
            this.fileId = fileId; this.filename = filename; this.mediaType = mediaType; this.sha256 = sha256; this.size = size;
        }
    }

    static final class ReviewState {
        final String key, text;
        final long version, basedOnSequence, lag;
        final List<String> refs;
        ReviewState(String key, String text, long version, long basedOnSequence, long lag, List<String> refs) {
            this.key = key; this.text = text; this.version = version;
            this.basedOnSequence = basedOnSequence; this.lag = lag; this.refs = refs;
        }
    }

    private final String endpoint;
    private final String token;

    ApiClient(String endpoint, String token) {
        this.endpoint = SessionStore.normalizeEndpoint(endpoint);
        this.token = token;
    }

    static Login login(String endpoint, String username, String password) throws IOException, JSONException {
        ApiClient client = new ApiClient(endpoint, null);
        JSONObject body = new JSONObject().put("username", username).put("password", password);
        JSONObject result = client.json("POST", "/v1/auth/login", body);
        JSONObject user = result.getJSONObject("user");
        long expiry = 0;
        String expiresAt = result.optString("expires_at", "");
        try { expiry = Instant.parse(expiresAt).toEpochMilli(); } catch (RuntimeException ignored) {}
        return new Login(user.getString("id"), user.getString("username"), result.getString("token"), expiry);
    }

    List<Project> projects() throws IOException, JSONException {
        JSONArray values = json("GET", "/v1/projects", null).getJSONArray("projects");
        List<Project> result = new ArrayList<>();
        for (int i = 0; i < values.length(); i++) {
            result.add(project(values.getJSONObject(i)));
        }
        return result;
    }

    Project createProject(String name, String timezone) throws IOException, JSONException {
        return project(json("POST", "/v1/projects", new JSONObject().put("name", name).put("timezone", timezone)));
    }

    Project updateProjectTimezone(String projectId, String timezone) throws IOException, JSONException {
        return project(json("PATCH", projectPath(projectId, ""), new JSONObject().put("timezone", timezone)));
    }

    void logout() {
        try { json("POST", "/v1/auth/logout", null); } catch (Exception ignored) {}
    }

    FileInfo uploadFile(CaptureItem item) throws IOException, JSONException {
        if (!item.fileStillMatches()) throw new IOException("local file size changed");
        FileIdentity identity = FileIdentity.read(new File(item.localPath));
        if (identity.size != item.sizeBytes || !identity.sha256.equals(item.sha256)) throw new IOException("local file hash changed");
        String boundary = "edc-" + StableIds.uuidV7();
        HttpURLConnection connection = open("POST", projectPath(item.projectId, "/files"));
        connection.setRequestProperty("Content-Type", "multipart/form-data; boundary=" + boundary);
        connection.setChunkedStreamingMode(64 * 1024);
        connection.setDoOutput(true);
        try (BufferedOutputStream output = new BufferedOutputStream(connection.getOutputStream());
             BufferedInputStream input = new BufferedInputStream(new FileInputStream(item.localPath))) {
            write(output, "--" + boundary + "\r\nContent-Disposition: form-data; name=\"sha256\"\r\n\r\n" + item.sha256 + "\r\n");
            String safeName = item.filename.replace('"', '_').replace('\r', '_').replace('\n', '_');
            write(output, "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + safeName
                    + "\"\r\nContent-Type: " + item.mediaType + "\r\n\r\n");
            byte[] buffer = new byte[64 * 1024];
            int read;
            while ((read = input.read(buffer)) != -1) output.write(buffer, 0, read);
            write(output, "\r\n--" + boundary + "--\r\n");
        }
        JSONObject result = responseJson(connection);
        FileInfo info = new FileInfo(result.getString("file_id"), result.getString("project_id"),
                result.getString("sha256"), result.getLong("size_bytes"));
        if (!item.projectId.equals(info.projectId) || !item.sha256.equals(info.sha256) || item.sizeBytes != info.size) {
            throw new IOException("server file identity did not match upload");
        }
        return info;
    }

    EventResult record(CaptureItem item, String fileId) throws IOException, JSONException {
        JSONObject content = new JSONObject().put("kind", "file").put("file_id", fileId)
                .put("duration_ms", item.durationMs);
        JSONObject source = new JSONObject().put("channel", "app").put("client", "android")
                .put("device", Build.MANUFACTURER + " " + Build.MODEL);
        JSONObject event = new JSONObject().put("id", item.id).put("type", "note")
                .put("content", content)
                .put("metadata", new JSONObject().put("kind", "capture"))
                .put("source", source).put("occurred_at", item.occurredAt);
        JSONObject result = json("POST", projectPath(item.projectId, "/events"),
                new JSONObject().put("events", new JSONArray().put(event)));
        JSONArray values = result.getJSONArray("results");
        if (values.length() != 1) throw new IOException("server returned an unexpected event result count");
        JSONObject value = values.getJSONObject(0);
        if (!item.id.equals(value.getString("id"))) throw new IOException("server returned a different event id");
        String status = value.getString("status");
        if (!"created".equals(status) && !"duplicate".equals(status)) {
            JSONObject error = value.optJSONObject("error");
            throw new ApiException(409, status, error == null ? status : error.optString("message", status));
        }
        return new EventResult(status, value.optLong("sequence"));
    }

    List<RecordSummary> appEvents(String projectId) throws IOException, JSONException {
        List<RecordSummary> ascending = new ArrayList<>();
        String cursor = null;
        do {
            JSONObject query = new JSONObject().put("source", new JSONObject().put("channel", "app")).put("limit", 100);
            if (cursor != null) query.put("cursor", cursor);
            JSONObject page = json("POST", projectPath(projectId, "/events/query"), query);
            JSONArray events = page.getJSONArray("events");
            for (int i = 0; i < events.length(); i++) {
                JSONObject event = events.getJSONObject(i);
                JSONObject content = event.getJSONObject("content");
                ascending.add(new RecordSummary(event.getString("id"), event.getString("type"),
                        event.optString("recorded_at"), content.optString("file_id"), content.optString("filename"),
                        content.optString("media_type"), content.optString("sha256"), content.optLong("size_bytes")));
            }
            cursor = page.optString("next_cursor", "");
            if (cursor.isEmpty()) cursor = null;
        } while (cursor != null);
        List<RecordSummary> newestFirst = new ArrayList<>(ascending.size());
        for (int i = ascending.size() - 1; i >= 0; i--) newestFirst.add(ascending.get(i));
        return newestFirst;
    }

    List<ReviewState> dailyReviews(String projectId) throws IOException, JSONException {
        JSONObject result = json("GET", projectPath(projectId, "/state") + "?prefix=daily-review%2F", null);
        JSONArray states = result.getJSONArray("states");
        List<ReviewState> output = new ArrayList<>();
        for (int i = states.length() - 1; i >= 0; i--) {
            JSONObject state = states.getJSONObject(i);
            JSONObject content = state.optJSONObject("content");
            JSONArray refsJson = state.optJSONArray("refs");
            List<String> refs = new ArrayList<>();
            if (refsJson != null) for (int j = 0; j < refsJson.length(); j++) refs.add(refsJson.getString(j));
            output.add(new ReviewState(state.getString("key"), content == null ? "" : content.optString("text"),
                    state.getLong("version"), state.optLong("based_on_sequence"), state.optLong("lag"), refs));
        }
        return output;
    }

    List<String> derivedTexts(String projectId, String sourceEventId) throws IOException, JSONException {
        JSONObject query = new JSONObject().put("types", new JSONArray().put("derived"))
                .put("refs_to", sourceEventId).put("limit", 100);
        JSONArray events = json("POST", projectPath(projectId, "/events/query"), query).getJSONArray("events");
        List<String> output = new ArrayList<>();
        for (int i = 0; i < events.length(); i++) {
            JSONObject content = events.getJSONObject(i).getJSONObject("content");
            if ("text".equals(content.optString("kind"))) output.add(content.optString("text"));
        }
        return output;
    }

    String eventText(String projectId, String eventId) throws IOException, JSONException {
        JSONObject event = json("GET", projectPath(projectId, "/events/") + Uri.encode(eventId), null);
        if (!projectId.equals(event.getString("project_id")) || !eventId.equals(event.getString("id"))) {
            throw new IOException("server returned a different event");
        }
        JSONObject content = event.getJSONObject("content");
        if ("text".equals(content.optString("kind"))) return content.optString("text");
        return content.optString("filename", eventId) + "\n" + content.optString("media_type")
                + " · " + content.optLong("size_bytes") + " bytes\nSHA-256 " + content.optString("sha256");
    }

    void downloadFile(String projectId, String fileId, String expectedSha256, long expectedSize, File target) throws IOException {
        HttpURLConnection connection = open("GET", projectPath(projectId, "/files/") + Uri.encode(fileId));
        int status;
        try { status = connection.getResponseCode(); }
        catch (IOException transport) { throw new ApiException(0, "network", transport.getMessage()); }
        if (status < 200 || status >= 300) {
            try { responseJson(connection); } catch (JSONException invalid) { throw new IOException(invalid); }
            return;
        }
        String actualId = connection.getHeaderField("X-EDC-File-ID");
        String actualSha = connection.getHeaderField("X-EDC-SHA256");
        long actualSize = connection.getContentLengthLong();
        if (!fileId.equals(actualId) || !expectedSha256.equals(actualSha) || expectedSize != actualSize) {
            throw new IOException("download headers did not match event file identity");
        }
        File partial = new File(target.getAbsolutePath() + ".partial");
        long count = 0;
        try (BufferedInputStream input = new BufferedInputStream(connection.getInputStream());
             FileOutputStream output = new FileOutputStream(partial)) {
            byte[] buffer = new byte[64 * 1024]; int read;
            while ((read = input.read(buffer)) != -1) {
                count += read;
                if (count > expectedSize) throw new IOException("download exceeded expected size");
                output.write(buffer, 0, read);
            }
        } catch (IOException error) { partial.delete(); throw error; }
        FileIdentity identity = FileIdentity.read(partial);
        if (identity.size != expectedSize || !identity.sha256.equals(expectedSha256)) {
            partial.delete(); throw new IOException("downloaded file identity mismatch");
        }
        if (target.exists() && !target.delete()) { partial.delete(); throw new IOException("could not replace media cache"); }
        if (!partial.renameTo(target)) { partial.delete(); throw new IOException("could not publish media cache"); }
    }

    List<String> plugins(String projectId) throws IOException, JSONException {
        JSONObject result = json("GET", projectPath(projectId, "/plugins"), null);
        JSONArray plugins = result.optJSONArray("plugins");
        if (plugins == null) plugins = result.optJSONArray("installations");
        List<String> output = new ArrayList<>();
        if (plugins == null) return output;
        for (int i = 0; i < plugins.length(); i++) {
            JSONObject item = plugins.getJSONObject(i);
            String id = item.optString("plugin_id", item.optString("id", "plugin"));
            output.add(id + " · " + item.optString("status", "unknown"));
        }
        return output;
    }

    private JSONObject json(String method, String path, JSONObject body) throws IOException, JSONException {
        HttpURLConnection connection = open(method, path);
        if (body != null) {
            connection.setDoOutput(true);
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
            try (BufferedOutputStream output = new BufferedOutputStream(connection.getOutputStream())) {
                output.write(body.toString().getBytes(StandardCharsets.UTF_8));
            }
        }
        return responseJson(connection);
    }

    private HttpURLConnection open(String method, String path) throws IOException {
        HttpURLConnection connection = (HttpURLConnection) new URL(endpoint + path).openConnection();
        connection.setRequestMethod(method);
        connection.setConnectTimeout(20_000);
        connection.setReadTimeout(60_000);
        connection.setRequestProperty("Accept", "application/json");
        if (token != null) connection.setRequestProperty("Authorization", "Bearer " + token);
        return connection;
    }

    private static JSONObject responseJson(HttpURLConnection connection) throws IOException, JSONException {
        int status;
        try { status = connection.getResponseCode(); }
        catch (IOException transport) { throw new ApiException(0, "network", transport.getMessage()); }
        java.io.InputStream stream = status >= 200 && status < 300 ? connection.getInputStream() : connection.getErrorStream();
        byte[] data = stream == null ? new byte[0] : readLimited(stream, 2 * 1024 * 1024);
        String raw = new String(data, StandardCharsets.UTF_8);
        if (status < 200 || status >= 300) {
            String code = "http_" + status;
            String message = raw;
            try {
                JSONObject error = new JSONObject(raw).optJSONObject("error");
                if (error != null) { code = error.optString("code", code); message = error.optString("message", message); }
            } catch (JSONException ignored) {}
            throw new ApiException(status, code, message);
        }
        return raw.isEmpty() ? new JSONObject() : new JSONObject(raw);
    }

    private static byte[] readLimited(java.io.InputStream stream, int limit) throws IOException {
        try (java.io.InputStream input = stream; ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[16 * 1024];
            int total = 0, read;
            while ((read = input.read(buffer)) != -1) {
                total += read;
                if (total > limit) throw new IOException("response exceeded limit");
                output.write(buffer, 0, read);
            }
            return output.toByteArray();
        }
    }

    private static String projectPath(String projectId, String suffix) {
        return "/v1/projects/" + Uri.encode(projectId) + suffix;
    }

    private static Project project(JSONObject value) throws JSONException {
        return new Project(value.getString("id"), value.getString("name"), value.optString("owner_user_id"),
                value.getString("timezone"));
    }

    private static void write(BufferedOutputStream output, String value) throws IOException {
        output.write(value.getBytes(StandardCharsets.UTF_8));
    }
}
