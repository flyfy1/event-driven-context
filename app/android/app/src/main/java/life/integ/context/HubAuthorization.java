package life.integ.context;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;

/** Shared fail-closed checks for authorization UI and background reminders. */
final class HubAuthorization {
    static boolean pending(String status, String expiresAt, long now) {
        return "pending".equals(status) && unexpired(expiresAt, now);
    }

    static boolean unexpired(String expiresAt, long now) {
        try { return Instant.parse(expiresAt).toEpochMilli() > now; }
        catch (RuntimeException invalid) { return false; }
    }

    static boolean sameSession(SessionStore.Session expected, SessionStore.Session current) {
        return expected != null && current != null && expected.endpoint.equals(current.endpoint)
                && expected.userId.equals(current.userId) && expected.token.equals(current.token)
                && (current.expiresAt == 0 || current.expiresAt > System.currentTimeMillis());
    }

    static String accountKey(SessionStore.Session session) {
        return digest(session.endpoint + "\n" + session.userId);
    }

    static String digest(String value) {
        try {
            byte[] bytes = MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8));
            StringBuilder result = new StringBuilder();
            for (byte b : bytes) result.append(String.format(java.util.Locale.ROOT, "%02x", b & 255));
            return result.toString();
        } catch (java.security.NoSuchAlgorithmException impossible) { throw new IllegalStateException(impossible); }
    }
}
