package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import android.speech.SpeechRecognizer;
import org.junit.Test;

public class SpeechInputErrorMapperTest {
    @Test
    public void mapsPermissionBusyNoSpeechAndLanguageErrorsWithoutProviderText() {
        assertMapping(SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS, "permission_denied", true);
        assertMapping(SpeechRecognizer.ERROR_RECOGNIZER_BUSY, "busy", true);
        assertMapping(SpeechRecognizer.ERROR_TOO_MANY_REQUESTS, "busy", true);
        assertMapping(SpeechRecognizer.ERROR_NO_MATCH, "no_speech", true);
        assertMapping(SpeechRecognizer.ERROR_SPEECH_TIMEOUT, "no_speech", true);
        assertMapping(SpeechRecognizer.ERROR_LANGUAGE_NOT_SUPPORTED, "unsupported", false);
        assertMapping(SpeechRecognizer.ERROR_LANGUAGE_UNAVAILABLE, "unsupported", false);
        assertMapping(SpeechRecognizer.ERROR_NETWORK, "recognition_failed", true);
    }

    private static void assertMapping(int providerCode, String expectedCode, boolean recoverable) {
        SpeechInputException mapped = SpeechInputErrorMapper.map(providerCode);
        assertEquals(expectedCode, mapped.code());
        if (recoverable) assertTrue(mapped.recoverable()); else assertFalse(mapped.recoverable());
    }
}
