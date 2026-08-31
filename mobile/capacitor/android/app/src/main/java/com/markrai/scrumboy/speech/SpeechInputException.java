package com.markrai.scrumboy.speech;

final class SpeechInputException extends Exception {
    private final String code;
    private final boolean recoverable;

    SpeechInputException(String code, boolean recoverable) {
        super(code);
        this.code = code;
        this.recoverable = recoverable;
    }

    String code() {
        return code;
    }

    boolean recoverable() {
        return recoverable;
    }

    static String publicMessage(String code) {
        return switch (code) {
            case "unsupported" -> "On-device speech input is not supported";
            case "not_ready" -> "On-device speech input is not ready";
            case "permission_denied" -> "Microphone permission was denied";
            case "permission_denied_permanently" -> "Microphone permission is blocked";
            case "busy" -> "On-device speech input is busy";
            case "foreground_required" -> "On-device speech input requires the foreground";
            case "no_speech" -> "No speech was recognized";
            case "cancelled" -> "Listening was cancelled";
            case "invalid_request" -> "Invalid speech input request";
            default -> "On-device speech recognition failed";
        };
    }
}
