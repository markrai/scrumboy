package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertThrows;

import static org.junit.Assert.assertTrue;

import java.util.concurrent.CyclicBarrier;
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
    public void attachingCancellationAfterTerminalInvokesTheExactCallbackImmediately() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        AtomicInteger nativeCancellations = new AtomicInteger();
        SpeechInputOperationRegistry.Operation operation = registry.begin("speech-1", () -> {});
        assertNotNull(registry.cancel("speech-1"));
        operation.attachNativeCancellation(nativeCancellations::incrementAndGet);
        assertEquals(1, nativeCancellations.get());
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

    /**
     * Native cancellation is dispatched at most once per operation. Both attachment sites
     * (platform and Advanced) recheck ownership and release their own handle immediately
     * after attaching, so a suppressed second dispatch can never leak a recognizer.
     */
    @Test
    public void nativeCancellationDispatchesAtMostOncePerOperation() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        AtomicInteger dispatches = new AtomicInteger();
        SpeechInputOperationRegistry.Operation operation = registry.begin("speech-1", () -> {});

        operation.attachNativeCancellation(dispatches::incrementAndGet);
        assertNotNull(registry.cancel("speech-1"));
        assertEquals(1, dispatches.get());

        // A re-attach after the operation went terminal must not dispatch a second time.
        operation.attachNativeCancellation(dispatches::incrementAndGet);
        assertEquals(1, dispatches.get());
    }

    /**
     * Attachment happens on Android Main while cancellation arrives on Capacitor's plugin
     * handler thread. Whichever side observes the other first, the exact native
     * cancellation must be dispatched once and only once.
     */
    @Test
    public void attachAndCancelRacingAcrossThreadsDispatchTheCallbackExactlyOnce() throws Exception {
        for (int attempt = 0; attempt < 2_000; attempt += 1) {
            SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
            SpeechInputOperationRegistry.Operation operation = registry.begin("speech-1", () -> {});
            AtomicInteger nativeCancellations = new AtomicInteger();
            AtomicInteger failures = new AtomicInteger();
            CyclicBarrier gate = new CyclicBarrier(2);

            Thread capacitor = new Thread(() -> {
                try {
                    gate.await();
                    registry.cancel("speech-1");
                } catch (Exception error) {
                    failures.incrementAndGet();
                }
            }, "CapacitorPlugins");
            capacitor.start();

            gate.await();
            operation.attachNativeCancellation(nativeCancellations::incrementAndGet);
            capacitor.join();

            assertEquals(0, failures.get());
            assertEquals(1, nativeCancellations.get());
        }
    }

    /** Cancellation must not hold the registry lock while native release runs. */
    @Test
    public void nativeCancellationRunsWithoutHoldingTheRegistryLock() throws Exception {
        SpeechInputOperationRegistry registry = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = registry.begin("speech-1", () -> {});
        AtomicInteger reentrantBegins = new AtomicInteger();
        operation.attachNativeCancellation(() -> {
            // A concurrent listen() would contend on the registry monitor here.
            Thread other = new Thread(() -> {
                try {
                    registry.begin("speech-2", () -> {});
                    reentrantBegins.incrementAndGet();
                } catch (SpeechInputException ignored) {
                    // begin may legitimately report busy; only blocking forever is a defect.
                }
            });
            other.start();
            try {
                other.join(2_000);
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
            }
            assertFalse("registry monitor must not be held during native cancellation", other.isAlive());
        });

        assertNotNull(registry.cancel("speech-1"));
        assertTrue(reentrantBegins.get() >= 0);
    }
}
