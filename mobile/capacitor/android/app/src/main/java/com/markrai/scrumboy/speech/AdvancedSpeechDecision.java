package com.markrai.scrumboy.speech;

final class AdvancedSpeechDecision {
    final SpeechInputProviderId providerId;
    final AdvancedSpeechCacheState state;
    final String localeTag;
    final String cacheAccess;
    final String statusSource;
    final boolean probedThisAccess;

    AdvancedSpeechDecision(
        SpeechInputProviderId providerId,
        AdvancedSpeechCacheState state,
        String localeTag,
        String cacheAccess,
        String statusSource,
        boolean probedThisAccess
    ) {
        this.providerId = providerId;
        this.state = state;
        this.localeTag = localeTag;
        this.cacheAccess = cacheAccess;
        this.statusSource = statusSource;
        this.probedThisAccess = probedThisAccess;
    }

    String advancedSupportLabel() {
        return switch (state) {
            case SUPPORTED_READY -> "ready";
            case SUPPORTED_PREPARING -> "preparing";
            case UNAVAILABLE_CACHED -> "unavailable";
            case UNKNOWN -> "unknown";
        };
    }

    boolean useAdvanced() {
        return providerId == SpeechInputProviderId.MLKIT_GENAI_ADVANCED
            && state == AdvancedSpeechCacheState.SUPPORTED_READY;
    }
}
