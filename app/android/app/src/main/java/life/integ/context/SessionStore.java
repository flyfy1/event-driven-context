package life.integ.context;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;

import java.net.URI;
import java.security.KeyStore;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

final class SessionStore {
    private static final String PREFS = "active-session";
    private static final String ALIAS = "event-driven-context-session-v1";
    private final SharedPreferences prefs;

    static final class Session {
        final String endpoint;
        final String userId;
        final String username;
        final String token;
        final String projectId;
        final String projectName;
        final String projectTimezone;
        final long expiresAt;

        Session(String endpoint, String userId, String username, String token,
                String projectId, String projectName, String projectTimezone, long expiresAt) {
            this.endpoint = endpoint;
            this.userId = userId;
            this.username = username;
            this.token = token;
            this.projectId = projectId;
            this.projectName = projectName;
            this.projectTimezone = projectTimezone;
            this.expiresAt = expiresAt;
        }
    }

    SessionStore(Context context) {
        prefs = context.getApplicationContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    synchronized void save(String endpoint, String userId, String username, String token, long expiresAt) throws Exception {
        String normalized = normalizeEndpoint(endpoint);
        String priorEndpoint = prefs.getString("endpoint", null);
        String priorUser = prefs.getString("user_id", null);
        String priorProject = prefs.getString("project_id", null);
        String priorProjectName = prefs.getString("project_name", null);
        String priorProjectTimezone = prefs.getString("project_timezone", null);
        byte[][] encrypted = encrypt(token.getBytes(java.nio.charset.StandardCharsets.UTF_8));
        SharedPreferences.Editor edit = prefs.edit()
                .putString("endpoint", normalized)
                .putString("user_id", userId)
                .putString("username", username)
                .putLong("expires_at", expiresAt)
                .putString("token_iv", Base64.encodeToString(encrypted[0], Base64.NO_WRAP))
                .putString("token_cipher", Base64.encodeToString(encrypted[1], Base64.NO_WRAP));
        if (normalized.equals(priorEndpoint) && userId.equals(priorUser) && priorProject != null) {
            edit.putString("project_id", priorProject).putString("project_name", priorProjectName)
                    .putString("project_timezone", priorProjectTimezone);
        } else {
            edit.remove("project_id").remove("project_name").remove("project_timezone");
        }
        boolean saved = edit.commit();
        if (!saved) throw new IllegalStateException("session was not persisted");
    }

    synchronized void selectProject(String projectId, String projectName, String projectTimezone) {
        if (!prefs.edit().putString("project_id", projectId).putString("project_name", projectName)
                .putString("project_timezone", projectTimezone).commit()) {
            throw new IllegalStateException("project selection was not persisted");
        }
    }

    synchronized Session load() {
        String endpoint = prefs.getString("endpoint", null);
        String userId = prefs.getString("user_id", null);
        String iv = prefs.getString("token_iv", null);
        String ciphertext = prefs.getString("token_cipher", null);
        if (endpoint == null || userId == null || iv == null || ciphertext == null) return null;
        try {
            byte[] token = decrypt(Base64.decode(iv, Base64.NO_WRAP), Base64.decode(ciphertext, Base64.NO_WRAP));
            return new Session(endpoint, userId, prefs.getString("username", ""),
                    new String(token, java.nio.charset.StandardCharsets.UTF_8),
                    prefs.getString("project_id", null), prefs.getString("project_name", null),
                    prefs.getString("project_timezone", null),
                    prefs.getLong("expires_at", 0));
        } catch (Exception invalid) {
            try { clear(); } catch (RuntimeException ignored) {}
            return null;
        }
    }

    synchronized void clear() {
        if (!prefs.edit().clear().commit()) throw new IllegalStateException("session was not cleared");
    }

    static String normalizeEndpoint(String raw) {
        try {
            URI uri = URI.create(raw.trim());
            if (!"https".equalsIgnoreCase(uri.getScheme()) || uri.getHost() == null
                    || uri.getUserInfo() != null || uri.getQuery() != null || uri.getFragment() != null
                    || !(uri.getPath() == null || uri.getPath().isEmpty() || "/".equals(uri.getPath()))) {
                throw new IllegalArgumentException("HTTPS origin required");
            }
            int port = uri.getPort();
            return "https://" + uri.getHost() + (port == -1 ? "" : ":" + port);
        } catch (RuntimeException invalid) {
            throw new IllegalArgumentException("HTTPS origin required", invalid);
        }
    }

    private static SecretKey key() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore");
        store.load(null);
        if (store.containsAlias(ALIAS)) return ((KeyStore.SecretKeyEntry) store.getEntry(ALIAS, null)).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS,
                KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build());
        return generator.generateKey();
    }

    private static byte[][] encrypt(byte[] plain) throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.ENCRYPT_MODE, key());
        return new byte[][]{cipher.getIV(), cipher.doFinal(plain)};
    }

    private static byte[] decrypt(byte[] iv, byte[] ciphertext) throws Exception {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, iv));
        return cipher.doFinal(ciphertext);
    }
}
