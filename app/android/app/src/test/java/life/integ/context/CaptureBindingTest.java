package life.integ.context;

import org.junit.Test;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class CaptureBindingTest {
    @Test public void retryIsBoundToEndpointAndAccountButNotCurrentProject() {
        CaptureItem item = new CaptureItem("id", "https://example.test", "alice", "project-a",
                "/tmp/missing", "voice.m4a", "audio/mp4", 1, "hash", 1,
                "2026-09-12T00:00:00Z", null, CaptureItem.READY, null, 1);
        assertTrue(item.belongsToAccount(new SessionStore.Session("https://example.test", "alice", "Alice", "token",
                "project-b", "B", 0)));
        assertFalse(item.belongsToAccount(new SessionStore.Session("https://example.test", "bob", "Bob", "token",
                "project-a", "A", 0)));
        assertFalse(item.belongsToAccount(new SessionStore.Session("https://other.test", "alice", "Alice", "token",
                "project-a", "A", 0)));
    }
}
