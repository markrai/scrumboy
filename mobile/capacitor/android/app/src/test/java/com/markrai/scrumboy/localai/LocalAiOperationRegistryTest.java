package com.markrai.scrumboy.localai;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.util.List;
import java.util.concurrent.FutureTask;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class LocalAiOperationRegistryTest {
    @Test
    public void permitsOneOperationAndRejectsPreparationGenerationOverlap() throws Exception {
        LocalAiOperationRegistry registry = new LocalAiOperationRegistry();
        LocalAiOperationRegistry.Operation prepare = registry.begin(
            "operation-1",
            null,
            LocalAiOperationRegistry.Kind.PREPARE,
            () -> {}
        );

        LocalAiException busy = assertThrows(LocalAiException.class, () -> registry.begin(
            "operation-2",
            "request-2",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        ));
        assertEquals("busy", busy.code());
        assertTrue(registry.claimCompletion(prepare));
        assertEquals(0, registry.activeCount());
    }

    @Test
    public void cancellationIsIdempotentAndSuppressesLateCompletion() throws Exception {
        LocalAiOperationRegistry registry = new LocalAiOperationRegistry();
        AtomicInteger cancellationDeliveries = new AtomicInteger();
        LocalAiOperationRegistry.Operation operation = registry.begin(
            "operation-1",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            cancellationDeliveries::incrementAndGet
        );
        FutureTask<Void> future = new FutureTask<>(() -> null);
        operation.attach(future);

        LocalAiOperationRegistry.Operation cancelled = registry.cancel("operation-1");
        assertNotNull(cancelled);
        cancelled.deliverCancellation();
        assertTrue(future.isCancelled());
        assertEquals(1, cancellationDeliveries.get());
        assertNull(registry.cancel("operation-1"));
        assertFalse(registry.claimCompletion(operation));
    }

    @Test
    public void invalidationCancelsAndMakesBothIdsRecent() throws Exception {
        LocalAiOperationRegistry registry = new LocalAiOperationRegistry();
        LocalAiOperationRegistry.Operation operation = registry.begin(
            "operation-1",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        );

        List<LocalAiOperationRegistry.Operation> cancelled = registry.invalidate();
        assertEquals(List.of(operation), cancelled);
        assertFalse(registry.claimCompletion(operation));
        assertEquals("invalid_request", assertThrows(LocalAiException.class, () -> registry.begin(
            "operation-1",
            "request-new",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        )).code());
        assertEquals("invalid_request", assertThrows(LocalAiException.class, () -> registry.begin(
            "operation-new",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        )).code());
    }

    @Test
    public void destroyCancelsActiveWorkAndDiscardsRecentIds() throws Exception {
        LocalAiOperationRegistry registry = new LocalAiOperationRegistry();
        LocalAiOperationRegistry.Operation first = registry.begin(
            "operation-1",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        );
        assertTrue(registry.claimCompletion(first));
        LocalAiOperationRegistry.Operation second = registry.begin(
            "operation-2",
            "request-2",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        );

        assertEquals(List.of(second), registry.destroy());
        assertNotNull(registry.begin(
            "operation-1",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> {}
        ));
    }
}
