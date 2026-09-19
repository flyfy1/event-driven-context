package life.integ.context;

import org.junit.Test;
import static org.junit.Assert.*;

public class HubAuthorizationTest {
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
