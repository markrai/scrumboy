package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import java.util.Arrays;
import java.util.List;
import org.junit.Test;

public class SpeechOutputVoicePolicyTest {
    private static SpeechOutputVoicePolicy.Candidate<String> voice(
        String name,
        String language,
        boolean networkRequired
    ) {
        return new SpeechOutputVoicePolicy.Candidate<>(name, language, networkRequired);
    }

    @Test
    public void prefersAnExactOfflineLocale() {
        List<SpeechOutputVoicePolicy.Candidate<String>> voices = Arrays.asList(
            voice("same-language", "en-GB", false),
            voice("exact", "en-US", false)
        );
        assertEquals("exact", SpeechOutputVoicePolicy.selectLocal(voices, "en-US"));
    }

    @Test
    public void permitsOnlyAnOfflineSameLanguageFallback() {
        List<SpeechOutputVoicePolicy.Candidate<String>> voices = Arrays.asList(
            voice("french", "fr-FR", false),
            voice("english", "en-GB", false)
        );
        assertEquals("english", SpeechOutputVoicePolicy.selectLocal(voices, "en-US"));
    }

    @Test
    public void rejectsNetworkRequiredExactAndSameLanguageVoices() {
        List<SpeechOutputVoicePolicy.Candidate<String>> voices = Arrays.asList(
            voice("exact-network", "en-US", true),
            voice("same-network", "en-GB", true),
            voice("unrelated-offline", "fr-FR", false)
        );
        assertNull(SpeechOutputVoicePolicy.selectLocal(voices, "en-US"));
    }

    @Test
    public void returnsNullWhenNoLocalVoiceMatchesTheLanguage() {
        assertNull(SpeechOutputVoicePolicy.selectLocal(
            Arrays.asList(voice("french", "fr-FR", false)),
            "en-US"
        ));
    }
}
