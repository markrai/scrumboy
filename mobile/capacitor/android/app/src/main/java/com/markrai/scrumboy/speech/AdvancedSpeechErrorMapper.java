package com.markrai.scrumboy.speech;

import com.google.mlkit.genai.common.GenAiException;

final class AdvancedSpeechErrorMapper {
    private AdvancedSpeechErrorMapper() {}

    static SpeechInputException map(GenAiException exception) {
        if (exception == null) return new SpeechInputException("recognition_failed", true);
        if (isCapabilityLevel(exception)) {
            return new SpeechInputException("not_ready", true, exception.getErrorCode(), "unknown");
        }
        int code = exception.getErrorCode();
        if (code == GenAiException.ErrorCode.CANCELLED) {
            return new SpeechInputException("cancelled", true);
        }
        if (code == GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED) {
            return new SpeechInputException("foreground_required", true, code, "unknown");
        }
        if (code == GenAiException.ErrorCode.BUSY) {
            return new SpeechInputException("busy", true, code, "recognizer_busy");
        }
        if (code == GenAiException.ErrorCode.REQUEST_TOO_SMALL) {
            // Shared GenAI docs: "Request is too small to be processed. Use a longer input."
            // That is not a documented speech/no-utterance condition. Do not map to no_speech.
            return new SpeechInputException("recognition_failed", true, code, "unknown");
        }
        return new SpeechInputException("recognition_failed", true, code, "unknown");
    }

    static SpeechInputException map(Throwable error) {
        if (error instanceof GenAiException genAiException) return map(genAiException);
        if (error instanceof SpeechInputException speechInputException) return speechInputException;
        return new SpeechInputException("recognition_failed", true);
    }

    /**
     * Capability-level demotion codes from alpha1 {@link GenAiException.ErrorCode}:
     * feature unavailable, unsupported, system too old, or AICore incompatible.
     * BUSY, CANCELLED, BACKGROUND_USE_BLOCKED, REQUEST_TOO_SMALL, and ordinary
     * recognition failures are not capability demotions.
     */
    static boolean isCapabilityLevel(GenAiException exception) {
        if (exception == null) return false;
        int code = exception.getErrorCode();
        return code == GenAiException.ErrorCode.NOT_AVAILABLE
            || code == GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE
            || code == GenAiException.ErrorCode.AICORE_INCOMPATIBLE
            || code == GenAiException.ErrorCode.NOT_SUPPORTED;
    }

    static boolean isCapabilityLevel(Throwable error) {
        return error instanceof GenAiException genAiException && isCapabilityLevel(genAiException);
    }
}
