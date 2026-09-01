package com.markrai.scrumboy.speech;

final class SpeechOutputException extends Exception {
    private final String code;
    private final boolean recoverable;

    SpeechOutputException(String code, boolean recoverable) {
        super(publicMessage(code));
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
        switch (code) {
            case "unsupported": return "Local speech output is not supported";
            case "not_ready": return "Local speech output is not ready";
            case "no_local_voice": return "No local speech voice is installed";
            case "busy": return "Local speech output is busy";
            case "foreground_required": return "Local speech output requires the foreground";
            case "cancelled": return "Speech output was cancelled";
            case "invalid_request": return "Invalid speech output request";
            default: return "Local speech synthesis failed";
        }
    }
}
