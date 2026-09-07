package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.Test;

public class AdvancedRecognitionOwnershipTest {
    private static final class FakeSession implements AdvancedRecognitionOwnership.Session {
        final AtomicInteger nativeStartCount = new AtomicInteger();
        final AtomicBoolean cancelled = new AtomicBoolean();
        final AtomicBoolean started = new AtomicBoolean();
        final CompletableFuture<Void> teardown = new CompletableFuture<>();

        @Override
        public void start() {
            if (!started.compareAndSet(false, true)) return;
            if (cancelled.get()) {
                teardown.complete(null);
                return;
            }
            nativeStartCount.incrementAndGet();
        }

        @Override
        public void cancel() {
            cancelled.set(true);
            if (!started.get()) teardown.complete(null);
        }

        @Override
        public CompletableFuture<Void> teardownFuture() {
            return teardown;
        }
    }

    private static ExecutorService namedPool(String name) {
        return Executors.newSingleThreadExecutor(r -> {
            Thread thread = new Thread(r, name);
            thread.setDaemon(true);
            return thread;
        });
    }

    @Test
    public void preparedHandleIsPublishedBeforeExactCancellationAndStart() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        FakeSession[] slot = new FakeSession[1];
        FakeSession prepared = new FakeSession();

        AdvancedRecognitionOwnership.publishAttachRegisterThenStartIfOwned(
            slot,
            prepared,
            operations,
            operation,
            barrier
        );

        assertSame(prepared, slot[0]);
        assertEquals(1, prepared.nativeStartCount.get());
        assertEquals(1, barrier.outstandingCount());
        assertFalse(barrier.currentBarrier().isDone());
        prepared.teardown.complete(null);
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void platformWaitsForTheOlderAdvancedSessionAfterANewerOneSettlesEarly() throws Exception {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        FakeSession advancedA = new FakeSession();
        barrier.register(advancedA, advancedA.teardownFuture());

        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operationB = operations.begin("speech-b", () -> {});
        FakeSession[] slotB = new FakeSession[1];
        FakeSession advancedB = new FakeSession();
        // B never owns a recognizer: it is cancelled before its session body can begin,
        // so its teardown settles at once while A is still closing.
        operations.cancel("speech-b");
        AdvancedRecognitionOwnership.publishAttachRegisterThenStartIfOwned(
            slotB,
            advancedB,
            operations,
            operationB,
            barrier
        );
        assertEquals(0, advancedB.nativeStartCount.get());
        assertTrue(advancedB.teardownFuture().isDone());
        assertEquals(1, barrier.outstandingCount());

        List<String> events = new ArrayList<>();
        AtomicInteger platformStartCount = new AtomicInteger();
        CountDownLatch platformStarted = new CountDownLatch(1);
        ExecutorService background = namedPool("barrier-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                barrier.currentBarrier(),
                background,
                () -> true,
                action -> main.execute(action),
                () -> {
                    events.add("platform-start");
                    platformStartCount.incrementAndGet();
                    platformStarted.countDown();
                }
            );

            Thread.sleep(80);
            assertEquals(0, platformStartCount.get());

            events.add("advanced-stop");
            events.add("advanced-close");
            events.add("advanced-gate-release");
            events.add("advanced-teardown-complete");
            advancedA.teardown.complete(null);

            assertTrue(platformStarted.await(5, TimeUnit.SECONDS));
            assertEquals(1, platformStartCount.get());
            assertEquals(
                List.of(
                    "advanced-stop",
                    "advanced-close",
                    "advanced-gate-release",
                    "advanced-teardown-complete",
                    "platform-start"
                ),
                events
            );
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }

    @Test
    public void cancelBeforeStartPreventsNativeAcquisitionAndSettlesTeardown() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        FakeSession[] slot = new FakeSession[1];
        FakeSession prepared = new FakeSession();
        operations.cancel("speech-1");

        AdvancedRecognitionOwnership.publishAttachRegisterThenStartIfOwned(
            slot,
            prepared,
            operations,
            operation,
            barrier
        );

        assertSame(prepared, slot[0]);
        assertEquals(0, prepared.nativeStartCount.get());
        assertTrue(prepared.cancelled.get());
        assertTrue(prepared.teardownFuture().isDone());
        prepared.start();
        assertEquals(0, prepared.nativeStartCount.get());
    }

    @Test
    public void startIsIdempotent() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        FakeSession[] slot = new FakeSession[1];
        FakeSession prepared = new FakeSession();
        AdvancedRecognitionOwnership.publishAttachRegisterThenStartIfOwned(
            slot,
            prepared,
            operations,
            operation,
            new AdvancedTeardownBarrier()
        );
        prepared.start();
        prepared.start();
        assertEquals(1, prepared.nativeStartCount.get());
    }

    @Test
    public void newPlatformUtteranceWaitsForPriorAdvancedNativeTeardownOnMain() throws Exception {
        List<String> events = new ArrayList<>();
        CompletableFuture<Void> advancedTeardown = new CompletableFuture<>();
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        Object advancedA = new Object();
        barrier.register(advancedA, advancedTeardown);

        AtomicInteger platformStartCount = new AtomicInteger();
        AtomicInteger createCount = new AtomicInteger();
        AtomicInteger listenerCount = new AtomicInteger();
        AtomicInteger startListeningCount = new AtomicInteger();
        CountDownLatch mainReady = new CountDownLatch(1);
        CountDownLatch platformStarted = new CountDownLatch(1);
        AtomicReference<Thread> mainThread = new AtomicReference<>();
        AtomicBoolean stillOwned = new AtomicBoolean(true);
        ExecutorService background = namedPool("advanced-teardown-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            main.execute(() -> {
                mainThread.set(Thread.currentThread());
                mainReady.countDown();
            });
            assertTrue(mainReady.await(5, TimeUnit.SECONDS));

            events.add("advanced-active");
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                barrier.currentBarrier(),
                background,
                stillOwned::get,
                action -> main.execute(action),
                () -> {
                    assertSame(mainThread.get(), Thread.currentThread());
                    events.add("platform-main");
                    createCount.incrementAndGet();
                    events.add("platform-create");
                    listenerCount.incrementAndGet();
                    events.add("platform-listener");
                    startListeningCount.incrementAndGet();
                    events.add("platform-start");
                    platformStartCount.incrementAndGet();
                    platformStarted.countDown();
                }
            );

            Thread.sleep(80);
            assertEquals(0, platformStartCount.get());
            assertEquals(0, createCount.get());
            assertEquals(0, listenerCount.get());
            assertEquals(0, startListeningCount.get());

            events.add("advanced-stop");
            events.add("advanced-close");
            events.add("advanced-gate-release");
            events.add("advanced-teardown-complete");
            advancedTeardown.complete(null);

            assertTrue(platformStarted.await(5, TimeUnit.SECONDS));
            assertEquals(1, platformStartCount.get());
            assertEquals(1, createCount.get());
            assertEquals(1, listenerCount.get());
            assertEquals(1, startListeningCount.get());
            assertEquals(
                List.of(
                    "advanced-active",
                    "advanced-stop",
                    "advanced-close",
                    "advanced-gate-release",
                    "advanced-teardown-complete",
                    "platform-main",
                    "platform-create",
                    "platform-listener",
                    "platform-start"
                ),
                events
            );
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }

    @Test
    public void sameTurnAndPostHandoffPlatformRetryUseTheSameBarrier() throws Exception {
        CompletableFuture<Void> advancedTeardown = new CompletableFuture<>();
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        barrier.register(new Object(), advancedTeardown);
        AtomicInteger platformStartCount = new AtomicInteger();
        CountDownLatch started = new CountDownLatch(1);
        ExecutorService background = namedPool("barrier-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                barrier.currentBarrier(),
                background,
                () -> true,
                action -> main.execute(action),
                () -> {
                    platformStartCount.incrementAndGet();
                    started.countDown();
                }
            );
            Thread.sleep(50);
            assertEquals(0, platformStartCount.get());
            advancedTeardown.complete(null);
            assertTrue(started.await(5, TimeUnit.SECONDS));
            assertEquals(1, platformStartCount.get());
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }

    @Test
    public void cancelledWaiterNeverStartsAfterTeardown() throws Exception {
        CompletableFuture<Void> advancedTeardown = new CompletableFuture<>();
        AtomicBoolean stillOwned = new AtomicBoolean(true);
        AtomicInteger platformStartCount = new AtomicInteger();
        ExecutorService background = namedPool("barrier-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                advancedTeardown,
                background,
                stillOwned::get,
                action -> main.execute(action),
                platformStartCount::incrementAndGet
            );
            stillOwned.set(false);
            advancedTeardown.complete(null);
            Thread.sleep(80);
            assertEquals(0, platformStartCount.get());
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }

    @Test
    public void destroyedActivityNeverStartsAfterTeardown() throws Exception {
        CompletableFuture<Void> advancedTeardown = new CompletableFuture<>();
        AtomicBoolean stillOwned = new AtomicBoolean(true);
        AtomicInteger platformStartCount = new AtomicInteger();
        ExecutorService background = namedPool("barrier-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                advancedTeardown,
                background,
                () -> stillOwned.get() && SpeechInputActivityGuard.canDispatch(true, null),
                action -> main.execute(action),
                platformStartCount::incrementAndGet
            );
            advancedTeardown.complete(null);
            Thread.sleep(80);
            assertEquals(0, platformStartCount.get());
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }

    @Test
    public void synchronousPrepareFailureStillWaitsForPreviousAdvancedTeardown() throws Exception {
        CompletableFuture<Void> previous = new CompletableFuture<>();
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        barrier.register(new Object(), previous);
        AtomicInteger platformStartCount = new AtomicInteger();
        CountDownLatch started = new CountDownLatch(1);
        ExecutorService background = namedPool("barrier-wait");
        ExecutorService main = namedPool("speech-main");
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                barrier.currentBarrier(),
                background,
                () -> true,
                action -> main.execute(action),
                () -> {
                    platformStartCount.incrementAndGet();
                    started.countDown();
                }
            );
            Thread.sleep(50);
            assertEquals(0, platformStartCount.get());
            previous.complete(null);
            assertTrue(started.await(5, TimeUnit.SECONDS));
            assertEquals(1, platformStartCount.get());
        } finally {
            background.shutdownNow();
            main.shutdownNow();
        }
    }
}
