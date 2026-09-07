package com.markrai.scrumboy.speech;

final class AdvancedSpeechUtteranceFreeze {
    private AdvancedSpeechUtteranceFreeze() {}

    static String providerToken(AdvancedSpeechDecision decision) {
        return decision.useAdvanced() ? "advanced" : "platform";
    }

    static AdvancedSpeechDecision thaw(String providerFrozen, String localeTag, String language) {
        boolean useAdvanced = "advanced".equals(providerFrozen);
        String resolvedLocale = localeTag == null || localeTag.isEmpty()
            ? AdvancedSpeechProviderManager.normalizeLocaleTag(language)
            : localeTag;
        return new AdvancedSpeechDecision(
            useAdvanced
                ? SpeechInputProviderId.MLKIT_GENAI_ADVANCED
                : SpeechInputProviderId.ANDROID_ON_DEVICE,
            useAdvanced
                ? AdvancedSpeechCacheState.SUPPORTED_READY
                : AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
            resolvedLocale,
            "hit",
            "cached",
            false
        );
    }
}
