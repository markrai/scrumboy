package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.concurrent.CyclicBarrier;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

/**
 * Models the plugin's platform install order:
 * prepare -> publish -> attach exact cancellation -> recheck ownership -> startListening.
 */
public class PlatformRecognitionHandleTest {
    private static final class FakeSession implements PlatformRecognitionHandle.Session {
        final List<String> events = new ArrayList<>();
        final AtomicInteger startListeningCount = new AtomicInteger();
        final AtomicInteger cancelCount = new AtomicInteger();
        final AtomicInteger destroyCount = new AtomicInteger();
        boolean startThrows;
        Runnable duringStartListening;

        FakeSession() {
            events.add("create");
            events.add("listener");
        }

        @Override
        public void startListening() throws SpeechInputException {
            if (startThrows) throw new SpeechInputException("recognition_failed", true);
            startListeningCount.incrementAndGet();
            events.add("start");
            if (duringStartListening != null) duringStartListening.run();
        }

        @Override
        public void cancelRecognition() {
            cancelCount.incrementAndGet();
            events.add("cancel");
        }

        @Override
        public void destroyRecognizer() {
            destroyCount.incrementAndGet();
            events.add("destroy");
        }
    }

    @Test
    public void prepareDoesNotStartListening() {
        FakeSession session = new FakeSession();
        new PlatformRecognitionHandle(session);
        assertEquals(0, session.startListeningCount.get());
        assertEquals(List.of("create", "listener"), session.events);
    }

    @Test
    public void handleIsPublishedBeforeCancellationIsAttached() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle[] slot = new PlatformRecognitionHandle[1];
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);

        assertNull(slot[0]);
        slot[0] = prepared;
        final PlatformRecognitionHandle[] cancellationTarget = new PlatformRecognitionHandle[1];
        operation.attachNativeCancellation(() -> {
            cancellationTarget[0] = prepared;
            prepared.cancel();
        });
        assertSame(prepared, slot[0]);

        assertTrue(operations.isActive(operation));
        prepared.start();

        assertEquals(1, session.startListeningCount.get());
        operations.cancel("speech-1");
        assertSame(prepared, cancellationTarget[0]);
    }

    @Test
    public void cancelBeforeStartNeverAcquiresTheMicrophone() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle[] slot = new PlatformRecognitionHandle[1];
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);

        slot[0] = prepared;
        operations.cancel("speech-1");
        operation.attachNativeCancellation(prepared::cancel);

        if (operations.isActive(operation)) fail("operation should already be terminal");
        prepared.start();

        assertEquals(0, session.startListeningCount.get());
        assertEquals(1, session.cancelCount.get());
        assertEquals(1, session.destroyCount.get());
        assertFalse(prepared.started());
        assertEquals(List.of("create", "listener", "cancel", "destroy"), session.events);
    }

    @Test
    public void startIsIdempotent() throws Exception {
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
        prepared.start();
        prepared.start();
        prepared.start();
        assertEquals(1, session.startListeningCount.get());
        assertTrue(prepared.started());
    }

    @Test
    public void cancelAndDestroyAreIdempotent() {
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
        prepared.cancel();
        prepared.cancel();
        prepared.destroy();
        assertEquals(1, session.cancelCount.get());
        assertEquals(1, session.destroyCount.get());
    }

    @Test
    public void successRacingCancellationYieldsOneTerminalAndOneNativeRelease() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
        operation.attachNativeCancellation(prepared::cancel);
        prepared.start();

        AtomicInteger terminals = new AtomicInteger();
        if (operations.claimCompletion(operation)) {
            prepared.destroy();
            terminals.incrementAndGet();
        }
        // Late cancellation loses the claim and must not release the recognizer twice.
        assertNull(operations.cancel("speech-1"));
        prepared.cancel();

        assertEquals(1, terminals.get());
        assertEquals(1, session.destroyCount.get());
        assertEquals(0, session.cancelCount.get());
    }

    @Test
    public void errorRacingCancellationYieldsOneTerminalAndOneNativeRelease() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
        operation.attachNativeCancellation(prepared::cancel);
        prepared.start();

        AtomicInteger terminals = new AtomicInteger();
        // Cancellation wins: it releases the recognizer through the exact handle.
        assertTrue(operations.cancel("speech-1") != null);
        if (operations.claimCompletion(operation)) {
            prepared.destroy();
            terminals.incrementAndGet();
        }

        assertEquals(0, terminals.get());
        assertEquals(1, session.cancelCount.get());
        assertEquals(1, session.destroyCount.get());
    }

    @Test
    public void startFailureReleasesTheRecognizerAndSurfacesTheMappedError() {
        FakeSession session = new FakeSession();
        session.startThrows = true;
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
        try {
            prepared.start();
            fail("expected recognition_failed");
        } catch (SpeechInputException error) {
            assertEquals("recognition_failed", error.code());
        }
        assertEquals(0, session.startListeningCount.get());
        assertEquals(1, session.destroyCount.get());
        prepared.cancel();
        assertEquals(1, session.destroyCount.get());
    }

    /**
     * Deterministic stand-in for the Android Main looper. The plugin posts native release
     * here rather than running it on the cancelling thread, so queued work can only be
     * delivered once the "Main" thread returns to the loop.
     */
    private static final class FakeMainLooper {
        private final Deque<Runnable> queue = new ArrayDeque<>();

        synchronized void post(Runnable work) {
            queue.add(work);
        }

        void drain() {
            while (true) {
                Runnable next;
                synchronized (this) {
                    next = queue.poll();
                }
                if (next == null) return;
                next.run();
            }
        }

        synchronized int pending() {
            return queue.size();
        }
    }

    /** Mirrors ScrumboySpeechInputPlugin#cancelPlatformHandle. */
    private static void cancelPlatformHandle(PlatformRecognitionHandle handle, FakeMainLooper main) {
        if (!handle.requestCancel()) return;
        main.post(handle::cancel);
    }

    /**
     * Capacitor dispatches @PluginMethod bodies on its CapacitorPlugins handler thread, so
     * cancel() can land between the plugin's ownership recheck and start() on Main. The
     * microphone must not be acquired.
     */
    @Test
    public void cancelOnTheCapacitorThreadAfterTheOwnershipRecheckNeverAcquiresTheMicrophone() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession session = new FakeSession();
        FakeMainLooper main = new FakeMainLooper();
        PlatformRecognitionHandle[] slot = new PlatformRecognitionHandle[1];
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);

        slot[0] = prepared;
        operation.attachNativeCancellation(() -> cancelPlatformHandle(prepared, main));
        assertTrue(operations.isActive(operation));

        // The plugin's Main thread has passed its recheck; cancellation arrives now.
        Thread capacitor = new Thread(() -> operations.cancel("speech-1"), "CapacitorPlugins");
        capacitor.start();
        capacitor.join();

        // No SpeechRecognizer call may happen on the cancelling thread.
        assertEquals(List.of("create", "listener"), session.events);
        assertEquals(1, main.pending());

        prepared.start();
        assertEquals(0, session.startListeningCount.get());

        main.drain();
        assertEquals(List.of("create", "listener", "cancel", "destroy"), session.events);
        assertEquals(1, session.cancelCount.get());
        assertEquals(1, session.destroyCount.get());
    }

    /** The cancelling thread only marks state; native release is the caller's to dispatch. */
    @Test
    public void requestCancelTouchesNoNativeSessionAndHandsTheReleaseToTheCaller() {
        FakeSession session = new FakeSession();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);

        assertTrue(prepared.requestCancel());
        assertEquals(List.of("create", "listener"), session.events);

        prepared.cancel();
        assertEquals(List.of("create", "listener", "cancel", "destroy"), session.events);
        // The release is already done, so a second requester must not dispatch again.
        assertFalse(prepared.requestCancel());
    }

    /** A release requested while start is in flight is delivered behind startListening. */
    @Test
    public void cancelRequestedDuringStartIsDeliveredAfterStartListening() throws Exception {
        FakeSession session = new FakeSession();
        FakeMainLooper main = new FakeMainLooper();
        PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);

        session.duringStartListening = () -> cancelPlatformHandle(prepared, main);
        prepared.start();

        assertEquals(1, session.startListeningCount.get());
        main.drain();
        assertEquals(List.of("create", "listener", "start", "cancel", "destroy"), session.events);
    }

    /**
     * Real two-thread race: start on Main against cancellation on the Capacitor thread.
     * Whichever wins the transition, the recognizer is released exactly once and
     * startListening is never issued against an already-destroyed recognizer.
     */
    @Test
    public void startAndCancelRacingAcrossThreadsNeverInterleaveNativeCalls() throws Exception {
        for (int attempt = 0; attempt < 2_000; attempt += 1) {
            FakeSession session = new FakeSession();
            FakeMainLooper main = new FakeMainLooper();
            PlatformRecognitionHandle prepared = new PlatformRecognitionHandle(session);
            CyclicBarrier gate = new CyclicBarrier(2);
            AtomicInteger failures = new AtomicInteger();

            Thread capacitor = new Thread(() -> {
                try {
                    gate.await();
                    cancelPlatformHandle(prepared, main);
                } catch (Exception error) {
                    failures.incrementAndGet();
                }
            }, "CapacitorPlugins");
            capacitor.start();

            gate.await();
            prepared.start();
            capacitor.join();
            main.drain();

            assertEquals(0, failures.get());
            assertEquals(1, session.destroyCount.get());
            assertEquals(1, session.cancelCount.get());
            int start = session.events.indexOf("start");
            if (start >= 0) {
                assertTrue(
                    "startListening must never follow the native release",
                    start < session.events.indexOf("cancel")
                );
                assertEquals(1, session.startListeningCount.get());
            } else {
                assertEquals(0, session.startListeningCount.get());
            }
        }
    }
}
