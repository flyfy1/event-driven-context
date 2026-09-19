package life.integ.context;

import org.junit.Test;
import static org.junit.Assert.*;

public class HubAuthorizationTest {
    @Test public void inboxDefaultsToActionableRequestsAndKeepsHistoryAccessible() {
        long now = java.time.Instant.parse("2026-09-19T00:00:00Z").toEpochMilli();
        String future = "2026-09-20T00:00:00Z";
        assertTrue(HubAuthorization.visible("pending", future, now, false));
        assertFalse(HubAuthorization.visible("pending", "2026-09-18T00:00:00Z", now, false));
        assertFalse(HubAuthorization.visible("approved", future, now, false));
        assertFalse(HubAuthorization.visible("pending", "invalid", now, false));
        assertTrue(HubAuthorization.visible("approved", future, now, true));
        assertTrue(HubAuthorization.visible("denied", future, now, true));
    }

    @Test public void calendarGrantsRequireExactKnownBoundedConstraints() {
        java.util.Map<String, Object> scope = new java.util.HashMap<>();
        scope.put("calendar_id", "primary");
        scope.put("time_min", "2026-09-19T08:00:00+08:00");
        scope.put("time_max", "2026-09-26T08:00:00+08:00");
        assertTrue(HubAuthorization.validConstraints("google-calendar", "events.list", scope));
        assertTrue(HubAuthorization.validConstraints("microsoft-calendar", "events.list", scope));
        assertTrue(HubAuthorization.validConstraints("google-calendar", "freebusy.query", scope));
        assertFalse(HubAuthorization.validConstraints("google-calendar", "events.list", null));
        assertFalse(HubAuthorization.validConstraints("outlook-mail", "messages.list", scope));
        assertTrue(HubAuthorization.validConstraints("outlook-mail", "messages.list", null));
        scope.put("include_attendees", true);
        assertFalse(HubAuthorization.validConstraints("google-calendar", "events.list", scope));
        scope.remove("include_attendees");
        scope.put("time_max", "2026-09-26T08:00:01+08:00");
        assertFalse(HubAuthorization.validConstraints("microsoft-calendar", "events.list", scope));
        scope.put("time_max", "2026-09-18T08:00:00+08:00");
        assertFalse(HubAuthorization.validConstraints("microsoft-calendar", "events.list", scope));
        scope.put("time_max", "2026-09-20");
        assertFalse(HubAuthorization.validConstraints("google-calendar", "events.list", scope));
        scope.put("time_max", 42);
        assertFalse(HubAuthorization.validConstraints("google-calendar", "events.list", scope));
        scope.remove("calendar_id");
        assertFalse(HubAuthorization.validConstraints("google-calendar", "events.list", scope));
    }

    private SessionStore.Session session(String endpoint, String owner, String token, long expiry) {
        return new SessionStore.Session(endpoint, owner, owner, token, null, null, null, expiry);
    }

    @Test public void remindersFailClosedForExpiredOrMalformedRequests() {
        long now = java.time.Instant.parse("2026-09-19T00:00:00Z").toEpochMilli();
        assertTrue(HubAuthorization.pending("pending", "2026-09-19T00:01:00Z", now));
        assertFalse(HubAuthorization.pending("pending", "2026-09-19T00:00:00Z", now));
        assertFalse(HubAuthorization.pending("pending", "", now));
        assertFalse(HubAuthorization.pending("pending", "invalid", now));
        assertFalse(HubAuthorization.pending("approved", "2026-09-20T00:00:00Z", now));
    }

    @Test public void staleResponsesCannotCrossLogoutServerAccountOrTokenChanges() {
        SessionStore.Session original = session("https://a.test", "alice", "token-one", 0);
        assertTrue(HubAuthorization.sameSession(original, session("https://a.test", "alice", "token-one", 0)));
        assertFalse(HubAuthorization.sameSession(original, null));
        assertFalse(HubAuthorization.sameSession(original, session("https://b.test", "alice", "token-one", 0)));
        assertFalse(HubAuthorization.sameSession(original, session("https://a.test", "bob", "token-one", 0)));
        assertFalse(HubAuthorization.sameSession(original, session("https://a.test", "alice", "token-two", 0)));
        assertFalse(HubAuthorization.sameSession(original, session("https://a.test", "alice", "token-one", 1)));
    }

    @Test public void settingsBindToEndpointAndOwnerWithoutStoringTokensInWorkData() {
        SessionStore.Session original = session("https://a.test", "alice", "token-one", 0);
        assertEquals(HubAuthorization.accountKey(original), HubAuthorization.accountKey(session("https://a.test", "alice", "token-two", 0)));
        assertNotEquals(HubAuthorization.accountKey(original), HubAuthorization.accountKey(session("https://b.test", "alice", "token-one", 0)));
        assertNotEquals(HubAuthorization.accountKey(original), HubAuthorization.accountKey(session("https://a.test", "bob", "token-one", 0)));
        assertEquals(64, HubAuthorization.accountKey(original).length());
    }
}
