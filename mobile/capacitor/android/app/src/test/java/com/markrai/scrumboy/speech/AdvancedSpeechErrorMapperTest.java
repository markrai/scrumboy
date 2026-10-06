package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import com.google.mlkit.genai.common.GenAiException;
import org.junit.Test;

public class AdvancedSpeechErrorMapperTest {
    @Test
    public void requestTooSmallMapsToRecognitionFailedNotNoSpeech() {
        SpeechInputException mapped = AdvancedSpeechErrorMapper.map(
            new GenAiException(new RuntimeException("too small"), GenAiException.ErrorCode.REQUEST_TOO_SMALL)
        );
        assertEquals("recognition_failed", mapped.code());
        assertTrue(mapped.recoverable());
        assertEquals(Integer.valueOf(GenAiException.ErrorCode.REQUEST_TOO_SMALL), mapped.providerCode());
    }

    @Test
    public void capabilityLevelCodesDemoteAndOrdinaryCodesDoNot() {
        assertTrue(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.NOT_AVAILABLE)
        ));
        assertTrue(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE)
        ));
        assertTrue(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.AICORE_INCOMPATIBLE)
        ));
        assertTrue(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.NOT_SUPPORTED)
        ));
        assertFalse(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.BUSY)
        ));
        assertFalse(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.CANCELLED)
        ));
        assertFalse(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED)
        ));
        assertFalse(AdvancedSpeechErrorMapper.isCapabilityLevel(
            new GenAiException(new RuntimeException(), GenAiException.ErrorCode.REQUEST_TOO_SMALL)
        ));
        assertFalse(AdvancedSpeechErrorMapper.isCapabilityLevel(new IllegalStateException("boom")));
    }
}
