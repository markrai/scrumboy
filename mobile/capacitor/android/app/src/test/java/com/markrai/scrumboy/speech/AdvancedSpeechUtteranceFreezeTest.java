package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class AdvancedSpeechUtteranceFreezeTest {
    @Test
    public void permissionRoundTripKeepsProviderAndLocaleFrozen() {
        AdvancedSpeechDecision selected = new AdvancedSpeechDecision(
            SpeechInputProviderId.MLKIT_GENAI_ADVANCED,
            AdvancedSpeechCacheState.SUPPORTED_READY,
            "fr-FR",
            "hit",
            "cached",
            false
        );
        String providerFrozen = AdvancedSpeechUtteranceFreeze.providerToken(selected);
        AdvancedSpeechDecision thawed = AdvancedSpeechUtteranceFreeze.thaw(
            providerFrozen,
            selected.localeTag,
            "en-US"
        );
        assertEquals("advanced", providerFrozen);
        assertTrue(thawed.useAdvanced());
        assertEquals("fr-FR", thawed.localeTag);
        assertFalse("en-US".equals(thawed.localeTag));
    }
}
