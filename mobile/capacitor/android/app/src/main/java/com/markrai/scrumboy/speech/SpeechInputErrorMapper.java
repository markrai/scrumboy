package com.markrai.scrumboy.speech;

import android.speech.SpeechRecognizer;

final class SpeechInputErrorMapper {
    private SpeechInputErrorMapper() {}

    static SpeechInputException map(int error) {
        return switch (error) {
            case SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS ->
                mapped(error, "permission_denied", true, "unknown");
            case SpeechRecognizer.ERROR_RECOGNIZER_BUSY ->
                mapped(error, "busy", true, "recognizer_busy");
            case SpeechRecognizer.ERROR_TOO_MANY_REQUESTS ->
                mapped(error, "busy", true, "too_many_requests");
            case SpeechRecognizer.ERROR_NO_MATCH ->
                mapped(error, "no_speech", true, "no_match");
            case SpeechRecognizer.ERROR_SPEECH_TIMEOUT ->
                mapped(error, "no_speech", true, "speech_timeout");
            case SpeechRecognizer.ERROR_LANGUAGE_NOT_SUPPORTED ->
                mapped(error, "unsupported", false, "language_not_supported");
            case SpeechRecognizer.ERROR_LANGUAGE_UNAVAILABLE ->
                mapped(error, "unsupported", false, "language_unavailable");
            case SpeechRecognizer.ERROR_AUDIO ->
                mapped(error, "recognition_failed", true, "audio");
            case SpeechRecognizer.ERROR_CLIENT ->
                mapped(error, "recognition_failed", true, "client");
            case SpeechRecognizer.ERROR_NETWORK ->
                mapped(error, "recognition_failed", true, "network");
            case SpeechRecognizer.ERROR_NETWORK_TIMEOUT ->
                mapped(error, "recognition_failed", true, "network_timeout");
            case SpeechRecognizer.ERROR_SERVER ->
                mapped(error, "recognition_failed", true, "server");
            case SpeechRecognizer.ERROR_SERVER_DISCONNECTED ->
                mapped(error, "recognition_failed", true, "server_disconnected");
            default -> mapped(error, "recognition_failed", true, "unknown");
        };
    }

    private static SpeechInputException mapped(
        int providerCode,
        String code,
        boolean recoverable,
        String providerReason
    ) {
        return new SpeechInputException(code, recoverable, providerCode, providerReason);
    }
}
