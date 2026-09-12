package life.integ.context;

import org.junit.Test;

import java.util.HashSet;
import java.util.Set;
import java.util.UUID;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

public class StableIdsTest {
    @Test public void uuidV7HasRequiredVersionVariantAndIsUnique() {
        Set<UUID> values = new HashSet<>();
        for (int i = 0; i < 1000; i++) {
            UUID value = StableIds.uuidV7();
            assertEquals(7, value.version());
            assertEquals(2, value.variant());
            assertTrue(values.add(value));
        }
    }
}
