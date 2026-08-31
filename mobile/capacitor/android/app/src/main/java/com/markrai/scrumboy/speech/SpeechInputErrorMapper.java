package com.markrai.scrumboy.speech;

import android.speech.SpeechRecognizer;

final class SpeechInputErrorMapper {
    private SpeechInputErrorMapper() {}

    static SpeechInputException map(int error) {
        return switch (error) {
            case SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS ->
                new SpeechInputException("permission_denied", true);
            case SpeechRecognizer.ERROR_RECOGNIZER_BUSY, SpeechRecognizer.ERROR_TOO_MANY_REQUESTS ->
                new SpeechInputException("busy", true);
            case SpeechRecognizer.ERROR_NO_MATCH, SpeechRecognizer.ERROR_SPEECH_TIMEOUT ->
                new SpeechInputException("no_speech", true);
            case SpeechRecognizer.ERROR_LANGUAGE_NOT_SUPPORTED, SpeechRecognizer.ERROR_LANGUAGE_UNAVAILABLE ->
                new SpeechInputException("unsupported", false);
            default -> new SpeechInputException("recognition_failed", true);
        };
    }
}
