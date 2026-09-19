package life.integ.context;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;

/** Shared fail-closed checks for authorization UI and background reminders. */
final class HubAuthorization {
    static boolean calendarOperation(String provider, String operation) {
        return ("google-calendar".equals(provider) || "microsoft-calendar".equals(provider))
                && ("events.list".equals(operation) || "freebusy.query".equals(operation));
    }

    static boolean validConstraints(String provider, String operation, java.util.Map<String, Object> constraints) {
        if (!calendarOperation(provider, operation)) return constraints == null;
        if (constraints == null || constraints.size() != 3
                || !constraints.keySet().equals(new java.util.HashSet<>(java.util.Arrays.asList("calendar_id", "time_min", "time_max")))) return false;
        for (String key : constraints.keySet()) {
            if (!(constraints.get(key) instanceof String) || ((String) constraints.get(key)).trim().isEmpty()) return false;
        }
        String calendar = (String) constraints.get("calendar_id");
        if (calendar.length() > 1024 || calendar.indexOf('\0') >= 0) return false;
        try {
            String startValue = (String) constraints.get("time_min"), endValue = (String) constraints.get("time_max");
            String timestamp = "\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(?:\\.\\d+)?(?:Z|[+-]\\d{2}:\\d{2})";
            if (!startValue.matches(timestamp) || !endValue.matches(timestamp)) return false;
            Instant start = java.time.OffsetDateTime.parse(startValue).toInstant();
            Instant end = java.time.OffsetDateTime.parse(endValue).toInstant();
            return end.isAfter(start) && java.time.Duration.between(start, end).compareTo(java.time.Duration.ofDays(7)) <= 0;
        } catch (RuntimeException invalid) { return false; }
    }

    static boolean visible(String status, String expiresAt, long now, boolean showHistory) {
        return showHistory || pending(status, expiresAt, now);
    }

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
