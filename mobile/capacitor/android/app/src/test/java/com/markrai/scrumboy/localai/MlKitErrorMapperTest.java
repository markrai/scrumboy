package com.markrai.scrumboy.localai;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import com.google.mlkit.genai.common.GenAiException;
import java.time.Duration;
import java.util.concurrent.CancellationException;
import org.junit.Test;

public class MlKitErrorMapperTest {
    @Test
    public void mapsAllRelevantProviderCodesWithoutMessageParsing() {
        assertMapping(GenAiException.ErrorCode.NOT_SUPPORTED, "unsupported", false);
        assertMapping(GenAiException.ErrorCode.NOT_AVAILABLE, "not_ready", true);
        assertMapping(GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE, "not_ready", true);
        assertMapping(GenAiException.ErrorCode.AICORE_INCOMPATIBLE, "not_ready", true);
        assertMapping(GenAiException.ErrorCode.BUSY, "busy", true);
        assertMapping(GenAiException.ErrorCode.PER_APP_BATTERY_USE_QUOTA_EXCEEDED, "quota_exceeded", true);
        assertMapping(GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED, "foreground_required", true);
        assertMapping(GenAiException.ErrorCode.NOT_ENOUGH_DISK_SPACE, "insufficient_storage", true);
        assertMapping(GenAiException.ErrorCode.REQUEST_TOO_LARGE, "input_too_large", false);
        assertMapping(GenAiException.ErrorCode.REQUEST_TOO_SMALL, "invalid_request", false);
        assertMapping(GenAiException.ErrorCode.REQUEST_PROCESSING_ERROR, "output_rejected", false);
        assertMapping(GenAiException.ErrorCode.RESPONSE_GENERATION_ERROR, "output_rejected", false);
        assertMapping(GenAiException.ErrorCode.RESPONSE_PROCESSING_ERROR, "output_rejected", false);
        assertMapping(GenAiException.ErrorCode.CACHE_PROCESSING_ERROR, "internal", true);
        assertMapping(GenAiException.ErrorCode.AUDIO_BUFFER_OVERFLOW, "internal", true);
        assertMapping(GenAiException.ErrorCode.CANCELLED, "cancelled", true);
        assertMapping(GenAiException.ErrorCode.UNKNOWN, "internal", true);
    }

    @Test
    public void preservesOnlyBoundedRetryMetadataForBusyAndQuota() {
        LocalAiException busy = MlKitErrorMapper.map(new GenAiException(
            "provider text must not escape",
            null,
            GenAiException.ErrorCode.BUSY,
            Duration.ofSeconds(2)
        ));
        assertEquals(Long.valueOf(2_000), busy.retryAfterMs());
        assertEquals("Local text generation is busy", busy.getMessage());

        LocalAiException bounded = MlKitErrorMapper.map(new GenAiException(
            null,
            GenAiException.ErrorCode.PER_APP_BATTERY_USE_QUOTA_EXCEEDED,
            Duration.ofDays(10)
        ));
        assertEquals(Long.valueOf(86_400_000), bounded.retryAfterMs());

        LocalAiException storage = MlKitErrorMapper.map(new GenAiException(
            null,
            GenAiException.ErrorCode.NOT_ENOUGH_DISK_SPACE,
            Duration.ofSeconds(2)
        ));
        assertNull(storage.retryAfterMs());
    }

    @Test
    public void mapsCancellationExceptionAndStatusSpecificRecovery() {
        assertEquals("cancelled", MlKitErrorMapper.map(new CancellationException()).code());
        assertEquals(
            "system-update",
            MlKitErrorMapper.status(new GenAiException(new RuntimeException(), GenAiException.ErrorCode.NEEDS_SYSTEM_UPDATE)).detail()
        );
        assertEquals(
            "foreground",
            MlKitErrorMapper.status(new GenAiException(new RuntimeException(), GenAiException.ErrorCode.BACKGROUND_USE_BLOCKED)).detail()
        );
        assertNull(MlKitErrorMapper.status(new RuntimeException()));
    }

    private static void assertMapping(int providerCode, String expectedCode, boolean recoverable) {
        LocalAiException mapped = MlKitErrorMapper.map(new GenAiException(
            "provider text must not escape",
            new RuntimeException(),
            providerCode
        ));
        assertEquals(expectedCode, mapped.code());
        if (recoverable) assertTrue(mapped.recoverable()); else assertFalse(mapped.recoverable());
    }
}
