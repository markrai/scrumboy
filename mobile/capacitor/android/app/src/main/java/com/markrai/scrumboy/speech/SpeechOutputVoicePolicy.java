package com.markrai.scrumboy.speech;

import java.util.List;
import java.util.Locale;

final class SpeechOutputVoicePolicy {
    static final class Candidate<T> {
        final T value;
        final String languageTag;
        final boolean networkRequired;

        Candidate(T value, String languageTag, boolean networkRequired) {
            this.value = value;
            this.languageTag = languageTag == null ? "" : languageTag;
            this.networkRequired = networkRequired;
        }
    }

    static <T> T selectLocal(List<Candidate<T>> candidates, String requestedLanguageTag) {
        String requested = normalizedTag(requestedLanguageTag);
        String requestedLanguage = Locale.forLanguageTag(requested).getLanguage();
        T sameLanguage = null;
        for (Candidate<T> candidate : candidates) {
            if (candidate == null || candidate.value == null || candidate.networkRequired) continue;
            String candidateTag = normalizedTag(candidate.languageTag);
            if (candidateTag.equals(requested)) return candidate.value;
            if (
                sameLanguage == null
                && !requestedLanguage.isEmpty()
                && Locale.forLanguageTag(candidateTag).getLanguage().equals(requestedLanguage)
            ) {
                sameLanguage = candidate.value;
            }
        }
        return sameLanguage;
    }

    private static String normalizedTag(String value) {
        if (value == null || value.trim().isEmpty()) return "en-US";
        return Locale.forLanguageTag(value).toLanguageTag();
    }

    private SpeechOutputVoicePolicy() {}
}
