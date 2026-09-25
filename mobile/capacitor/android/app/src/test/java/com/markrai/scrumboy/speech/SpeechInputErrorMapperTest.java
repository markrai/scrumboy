package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import android.speech.SpeechRecognizer;
import org.junit.Test;

public class SpeechInputErrorMapperTest {
    @Test
    public void mapsNormalizedCodesWithBoundedProviderDiagnostics() {
        assertMapping(SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS, "permission_denied", true, "unknown");
        assertMapping(SpeechRecognizer.ERROR_RECOGNIZER_BUSY, "busy", true, "recognizer_busy");
        assertMapping(SpeechRecognizer.ERROR_TOO_MANY_REQUESTS, "busy", true, "too_many_requests");
        assertMapping(SpeechRecognizer.ERROR_NO_MATCH, "no_speech", true, "no_match");
        assertMapping(SpeechRecognizer.ERROR_SPEECH_TIMEOUT, "no_speech", true, "speech_timeout");
        assertMapping(SpeechRecognizer.ERROR_LANGUAGE_NOT_SUPPORTED, "unsupported", false, "language_not_supported");
        assertMapping(SpeechRecognizer.ERROR_LANGUAGE_UNAVAILABLE, "unsupported", false, "language_unavailable");
        assertMapping(SpeechRecognizer.ERROR_AUDIO, "recognition_failed", true, "audio");
        assertMapping(SpeechRecognizer.ERROR_CLIENT, "recognition_failed", true, "client");
        assertMapping(SpeechRecognizer.ERROR_NETWORK, "recognition_failed", true, "network");
        assertMapping(SpeechRecognizer.ERROR_NETWORK_TIMEOUT, "recognition_failed", true, "network_timeout");
        assertMapping(SpeechRecognizer.ERROR_SERVER, "recognition_failed", true, "server");
        assertMapping(SpeechRecognizer.ERROR_SERVER_DISCONNECTED, "recognition_failed", true, "server_disconnected");
    }

    private static void assertMapping(
        int providerCode,
        String expectedCode,
        boolean recoverable,
        String providerReason
    ) {
        SpeechInputException mapped = SpeechInputErrorMapper.map(providerCode);
        assertEquals(expectedCode, mapped.code());
        assertEquals(Integer.valueOf(providerCode), mapped.providerCode());
        assertEquals(providerReason, mapped.providerReason());
        if (recoverable) assertTrue(mapped.recoverable()); else assertFalse(mapped.recoverable());
    }
}
