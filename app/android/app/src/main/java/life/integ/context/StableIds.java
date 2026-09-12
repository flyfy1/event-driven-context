package life.integ.context;

import java.security.SecureRandom;
import java.util.UUID;

final class StableIds {
    private static final SecureRandom RANDOM = new SecureRandom();

    private StableIds() {}

    static UUID uuidV7() {
        byte[] bytes = new byte[16];
        RANDOM.nextBytes(bytes);
        long millis = System.currentTimeMillis();
        bytes[0] = (byte) (millis >>> 40);
        bytes[1] = (byte) (millis >>> 32);
        bytes[2] = (byte) (millis >>> 24);
        bytes[3] = (byte) (millis >>> 16);
        bytes[4] = (byte) (millis >>> 8);
        bytes[5] = (byte) millis;
        bytes[6] = (byte) ((bytes[6] & 0x0f) | 0x70);
        bytes[8] = (byte) ((bytes[8] & 0x3f) | 0x80);
        long high = 0;
        long low = 0;
        for (int i = 0; i < 8; i++) high = (high << 8) | (bytes[i] & 0xffL);
        for (int i = 8; i < 16; i++) low = (low << 8) | (bytes[i] & 0xffL);
        return new UUID(high, low);
    }
}
