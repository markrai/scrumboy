package com.markrai.scrumboy.speech;

final class AdvancedSpeechCapabilityStore {
    private static final String KEY_PREFIX = "speech_advanced_probe_v1";

    private final AdvancedSpeechPreferenceStore preferences;

    AdvancedSpeechCapabilityStore(AdvancedSpeechPreferenceStore preferences) {
        this.preferences = preferences;
    }

    synchronized AdvancedSpeechCacheRecord read(String localeTag) {
        String raw = preferences.get(cacheKey(localeTag), null);
        if (raw == null || raw.isEmpty()) return null;
        String[] parts = raw.split("\\|", -1);
        if (parts.length != 6) {
            preferences.remove(cacheKey(localeTag));
            return null;
        }
        try {
            AdvancedSpeechCacheRecord record = new AdvancedSpeechCacheRecord(
                Integer.parseInt(parts[0]),
                parts[1],
                parts[2],
                AdvancedSpeechCacheState.fromPersisted(parts[3]),
                Long.parseLong(parts[4]),
                Long.parseLong(parts[5])
            );
            if (!record.matchesIdentity(localeTag) || record.state == AdvancedSpeechCacheState.UNKNOWN) {
                preferences.remove(cacheKey(localeTag));
                return null;
            }
            return record;
        } catch (RuntimeException error) {
            preferences.remove(cacheKey(localeTag));
            return null;
        }
    }

    synchronized void write(AdvancedSpeechCacheRecord record) {
        preferences.put(
            cacheKey(record.localeTag),
            record.schemaVersion
                + "|"
                + record.integrationVersion
                + "|"
                + record.localeTag
                + "|"
                + record.state.name()
                + "|"
                + record.checkedAtEpochMs
                + "|"
                + record.nextProbeAfterEpochMs
        );
    }

    synchronized void clear() {
        preferences.clear();
    }

    static String cacheKey(String localeTag) {
        return KEY_PREFIX
            + "|"
            + AdvancedSpeechCacheRecord.INTEGRATION_VERSION
            + "|"
            + localeTag;
    }
}
