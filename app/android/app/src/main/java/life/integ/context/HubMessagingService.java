package life.integ.context;

import androidx.annotation.NonNull;
import com.google.firebase.messaging.FirebaseMessagingService;
import com.google.firebase.messaging.RemoteMessage;

public final class HubMessagingService extends FirebaseMessagingService {
    @Override public void onNewToken(@NonNull String token) {
        // Obtain the latest token only when the current account has opted in.
        HubPush.register(this, new SessionStore(this).load());
    }

    @Override public void onMessageReceived(@NonNull RemoteMessage message) {
        if (!"authorization_changed".equals(message.getData().get("type"))) return;
        SessionStore.Session expected = new SessionStore(this).load();
        // Never display remote message content or act on an approval from a notification.
        HubPush.wake(this, expected);
    }
}
