package com.markrai.scrumboy.speech;

enum AdvancedSpeechCacheState {
    UNKNOWN,
    SUPPORTED_READY,
    SUPPORTED_PREPARING,
    UNAVAILABLE_CACHED;

    static AdvancedSpeechCacheState fromPersisted(String value) {
        if (value == null) return UNKNOWN;
        return switch (value) {
            case "SUPPORTED_READY" -> SUPPORTED_READY;
            case "SUPPORTED_PREPARING" -> SUPPORTED_PREPARING;
            case "UNAVAILABLE_CACHED" -> UNAVAILABLE_CACHED;
            default -> UNKNOWN;
        };
    }
}
