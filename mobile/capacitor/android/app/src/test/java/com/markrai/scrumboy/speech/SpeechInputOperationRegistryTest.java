package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertThrows;

import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class SpeechInputOperationRegistryTest {
    @Test
    public void permitsOnlyOneRecognizerAtATime() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation first = registry.begin("speech-1", () -> {});

        SpeechInputException busy = assertThrows(
            SpeechInputException.class,
            () -> registry.begin("speech-2", () -> {})
        );

        assertEquals("busy", busy.code());
        assertSame(first, registry.active("speech-1"));
        assertEquals(1, registry.activeCount());
    }

    @Test
    public void cancellationStopsNativeWorkAndSuppressesLateCompletion() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        AtomicInteger nativeCancellations = new AtomicInteger();
        AtomicInteger cancellationDeliveries = new AtomicInteger();
        SpeechInputOperationRegistry.Operation operation = registry.begin(
            "speech-1",
            cancellationDeliveries::incrementAndGet
        );
        operation.attachNativeCancellation(nativeCancellations::incrementAndGet);

        SpeechInputOperationRegistry.Operation cancelled = registry.cancel("speech-1");
        assertNotNull(cancelled);
        cancelled.deliverCancellation();

        assertEquals(1, nativeCancellations.get());
        assertEquals(1, cancellationDeliveries.get());
        assertFalse(registry.claimCompletion(operation));
        assertNull(registry.cancel("speech-1"));
    }

    @Test
    public void invalidationCancelsTheActiveRecognizer() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        AtomicInteger nativeCancellations = new AtomicInteger();
        SpeechInputOperationRegistry.Operation operation = registry.begin("speech-1", () -> {});
        operation.attachNativeCancellation(nativeCancellations::incrementAndGet);

        assertSame(operation, registry.invalidate());
        assertEquals(1, nativeCancellations.get());
        assertEquals(0, registry.activeCount());
        assertFalse(registry.claimCompletion(operation));
    }

    @Test
    public void validatesTheOpaqueOperationId() {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();

        assertEquals("invalid_request", assertThrows(
            SpeechInputException.class,
            () -> registry.begin(null, () -> {})
        ).code());
        assertEquals("invalid_request", assertThrows(
            SpeechInputException.class,
            () -> registry.begin("x".repeat(129), () -> {})
        ).code());
    }
}
