package life.integ.context;

import org.junit.Test;

import static org.junit.Assert.assertEquals;

public class ActorDisplayTest {
    @Test public void usernameIsPreferredButStableIdIsAlwaysAvailable() {
        ApiClient.Actor user = new ApiClient.Actor("user", "usr_stable", "songyy");
        assertEquals("songyy", user.displayName());
        assertEquals("usr_stable", user.id);
        assertEquals("plugin-brief", new ApiClient.Actor("plugin", "plugin-brief", "").displayName());
    }
}
