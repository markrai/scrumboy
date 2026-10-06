package com.markrai.scrumboy.localai;

final class LocalAiException extends Exception {
    private final String code;
    private final boolean recoverable;
    private final Long retryAfterMs;

    LocalAiException(String code, boolean recoverable) {
        this(code, recoverable, null, null);
    }

    LocalAiException(String code, boolean recoverable, Long retryAfterMs) {
        this(code, recoverable, retryAfterMs, null);
    }

    LocalAiException(String code, boolean recoverable, Long retryAfterMs, Throwable cause) {
        super(publicMessage(code), cause);
        this.code = code;
        this.recoverable = recoverable;
        this.retryAfterMs = retryAfterMs;
    }

    String code() {
        return code;
    }

    boolean recoverable() {
        return recoverable;
    }

    Long retryAfterMs() {
        return retryAfterMs;
    }

    static LocalAiException cancelled() {
        return new LocalAiException("cancelled", true);
    }

    static String publicMessage(String code) {
        return switch (code) {
            case "unsupported" -> "Local text generation is not supported";
            case "disabled" -> "Local text generation is disabled";
            case "not_ready" -> "Local text generation is not ready";
            case "download_failed" -> "Local text generation preparation failed";
            case "foreground_required" -> "Local text generation requires the foreground";
            case "busy" -> "Local text generation is busy";
            case "quota_exceeded" -> "Local text generation quota is exhausted";
            case "insufficient_storage" -> "Local text generation needs more storage";
            case "input_too_large" -> "Local text generation input is too large";
            case "invalid_request" -> "Invalid local text generation request";
            case "output_rejected" -> "Local text generation output was rejected";
            case "cancelled" -> "Local text generation was cancelled";
            default -> "Local text generation failed";
        };
    }
}
