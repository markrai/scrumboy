package com.markrai.scrumboy.speech;

final class AdvancedSpeechCacheRecord {
    static final int SCHEMA_VERSION = 1;
    static final String INTEGRATION_VERSION = "genai-speech-recognition:1.0.0-alpha1";
    static final long NEGATIVE_REPROBE_INTERVAL_MS = 6L * 60L * 60L * 1000L;

    final int schemaVersion;
    final String integrationVersion;
    final String localeTag;
    final AdvancedSpeechCacheState state;
    final long checkedAtEpochMs;
    final long nextProbeAfterEpochMs;

    AdvancedSpeechCacheRecord(
        int schemaVersion,
        String integrationVersion,
        String localeTag,
        AdvancedSpeechCacheState state,
        long checkedAtEpochMs,
        long nextProbeAfterEpochMs
    ) {
        this.schemaVersion = schemaVersion;
        this.integrationVersion = integrationVersion;
        this.localeTag = localeTag;
        this.state = state;
        this.checkedAtEpochMs = checkedAtEpochMs;
        this.nextProbeAfterEpochMs = nextProbeAfterEpochMs;
    }

    boolean matchesIdentity(String localeTag) {
        return schemaVersion == SCHEMA_VERSION
            && INTEGRATION_VERSION.equals(integrationVersion)
            && this.localeTag.equals(localeTag);
    }

    static AdvancedSpeechCacheRecord ready(String localeTag, long nowMs) {
        return new AdvancedSpeechCacheRecord(
            SCHEMA_VERSION,
            INTEGRATION_VERSION,
            localeTag,
            AdvancedSpeechCacheState.SUPPORTED_READY,
            nowMs,
            0L
        );
    }

    static AdvancedSpeechCacheRecord preparing(String localeTag, long nowMs) {
        return new AdvancedSpeechCacheRecord(
            SCHEMA_VERSION,
            INTEGRATION_VERSION,
            localeTag,
            AdvancedSpeechCacheState.SUPPORTED_PREPARING,
            nowMs,
            0L
        );
    }

    static AdvancedSpeechCacheRecord unavailable(String localeTag, long nowMs) {
        return new AdvancedSpeechCacheRecord(
            SCHEMA_VERSION,
            INTEGRATION_VERSION,
            localeTag,
            AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
            nowMs,
            nowMs + NEGATIVE_REPROBE_INTERVAL_MS
        );
    }
}
