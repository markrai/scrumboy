package com.markrai.scrumboy.localai;

import com.google.mlkit.genai.common.GenAiException;
import java.time.Duration;
import java.util.concurrent.CancellationException;
import java.util.concurrent.ExecutionException;

final class MlKitErrorMapper {
    private static final long MAX_RETRY_AFTER_MS = 86_400_000L;

    private MlKitErrorMapper() {}

    static LocalAiException map(Throwable error) {
        Throwable cause = unwrap(error);
        if (cause instanceof CancellationException || cause instanceof InterruptedException) {
            return LocalAiException.cancelled();
        }
        if (!(cause instanceof GenAiException exception)) {
            return new LocalAiException("internal", true, null, cause);
        }
        int code = exception.getErrorCode();
        Long retryAfterMs = (code == GenAiException.ErrorCode.BUSY || code == GenAiException.ErrorCode.PER_APP_BATTERY_USE_QUOTA_EXCEEDED)
            ? retryAfterMs(exception.getRetryDelay())
            : null;
        if (code == GenAiException.ErrorCode.CANCELLED) return LocalAiException.cancelled();
        if (code == GenAiException.ErrorCode.NOT_SUPPORTED) return new LocalAiException("unsupported", false, null, cause);
        if (code == GenAiException.ErrorCode.NOT_AVAILABLE || code == GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE || code == GenAiException.ErrorCode.AICORE_INCOMPATIBLE) {
            return new LocalAiException("not_ready", true, null, cause);
        }
        if (code == GenAiException.ErrorCode.BUSY) return new LocalAiException("busy", true, retryAfterMs, cause);
        if (code == GenAiException.ErrorCode.PER_APP_BATTERY_USE_QUOTA_EXCEEDED) {
            return new LocalAiException("quota_exceeded", true, retryAfterMs, cause);
        }
        if (code == GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED) {
            return new LocalAiException("foreground_required", true, null, cause);
        }
        if (code == GenAiException.ErrorCode.NOT_ENOUGH_DISK_SPACE) {
            return new LocalAiException("insufficient_storage", true, null, cause);
        }
        if (code == GenAiException.ErrorCode.REQUEST_TOO_LARGE) {
            return new LocalAiException("input_too_large", false, null, cause);
        }
        if (code == GenAiException.ErrorCode.REQUEST_TOO_SMALL || code == GenAiException.ErrorCode.STRUCTURED_OUTPUT_REQUEST_ERROR || code == GenAiException.ErrorCode.INVALID_INPUT_IMAGE) {
            return new LocalAiException("invalid_request", false, null, cause);
        }
        if (
            code == GenAiException.ErrorCode.REQUEST_PROCESSING_ERROR ||
            code == GenAiException.ErrorCode.RESPONSE_GENERATION_ERROR ||
            code == GenAiException.ErrorCode.RESPONSE_PROCESSING_ERROR ||
            code == GenAiException.ErrorCode.STRUCTURED_OUTPUT_RESPONSE_ERROR ||
            code == GenAiException.ErrorCode.STRUCTURED_OUTPUT_MAX_TOKENS_ERROR
        ) {
            return new LocalAiException("output_rejected", false, null, cause);
        }
        return new LocalAiException("internal", true, null, cause);
    }

    static LocalAiStatus status(Throwable error) {
        Throwable cause = unwrap(error);
        if (!(cause instanceof GenAiException exception)) return null;
        int code = exception.getErrorCode();
        if (code == GenAiException.ErrorCode.NOT_SUPPORTED) return LocalAiStatus.unsupported("provider");
        if (code == GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE || code == GenAiException.ErrorCode.AICORE_INCOMPATIBLE) {
            return LocalAiStatus.actionRequired("system-update");
        }
        if (code == GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED) {
            return LocalAiStatus.temporarilyUnavailable("foreground", null);
        }
        if (code == GenAiException.ErrorCode.BUSY) {
            return LocalAiStatus.temporarilyUnavailable("busy", retryAfterMs(exception.getRetryDelay()));
        }
        if (code == GenAiException.ErrorCode.PER_APP_BATTERY_USE_QUOTA_EXCEEDED) {
            return LocalAiStatus.temporarilyUnavailable("quota", retryAfterMs(exception.getRetryDelay()));
        }
        if (code == GenAiException.ErrorCode.NOT_ENOUGH_DISK_SPACE) {
            return LocalAiStatus.temporarilyUnavailable("storage", null);
        }
        if (code == GenAiException.ErrorCode.NOT_AVAILABLE) {
            return LocalAiStatus.temporarilyUnavailable("initializing", null);
        }
        return null;
    }

    private static Throwable unwrap(Throwable error) {
        Throwable current = error;
        while (current instanceof ExecutionException && current.getCause() != null) current = current.getCause();
        return current;
    }

    private static Long retryAfterMs(Duration duration) {
        if (duration == null || duration.isNegative()) return null;
        try {
            return Math.min(duration.toMillis(), MAX_RETRY_AFTER_MS);
        } catch (ArithmeticException ignored) {
            return MAX_RETRY_AFTER_MS;
        }
    }
}
