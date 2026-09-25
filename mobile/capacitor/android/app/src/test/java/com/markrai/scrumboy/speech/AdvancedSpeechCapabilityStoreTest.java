package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

public class AdvancedSpeechCapabilityStoreTest {
    @Test
    public void persistsAndReloadsLocaleScopedRecords() {
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(
            new InMemoryAdvancedSpeechPreferenceStore()
        );
        AdvancedSpeechCacheRecord ready = AdvancedSpeechCacheRecord.ready("en-US", 1000L);
        store.write(ready);

        AdvancedSpeechCacheRecord loaded = store.read("en-US");
        assertEquals(AdvancedSpeechCacheState.SUPPORTED_READY, loaded.state);
        assertEquals("en-US", loaded.localeTag);
        assertNull(store.read("fr-FR"));
    }

    @Test
    public void rejectsMismatchedSchemaOrIntegrationIdentity() {
        InMemoryAdvancedSpeechPreferenceStore prefs = new InMemoryAdvancedSpeechPreferenceStore();
        prefs.put(
            AdvancedSpeechCapabilityStore.cacheKey("en-US"),
            "99|other-integration|en-US|SUPPORTED_READY|1|0"
        );
        AdvancedSpeechCapabilityStore store = new AdvancedSpeechCapabilityStore(prefs);
        assertNull(store.read("en-US"));
    }
}
