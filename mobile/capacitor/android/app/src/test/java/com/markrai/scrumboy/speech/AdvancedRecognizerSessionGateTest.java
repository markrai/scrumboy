package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class AdvancedRecognizerSessionGateTest {
    @Test
    public void nextRecognizerWaitsUntilPreviousTeardownSettles() throws Exception {
        AdvancedRecognizerSessionGate gate = new AdvancedRecognizerSessionGate();
        CompletableFuture<Void> teardownA = gate.acquire();
        assertEquals(1, gate.recognizerStartCount());

        ExecutorService pool = Executors.newSingleThreadExecutor();
        AtomicInteger recognizerStartCountB = new AtomicInteger();
        CountDownLatch bStartedWaiting = new CountDownLatch(1);
        try {
            Future<CompletableFuture<Void>> sessionB = pool.submit(() -> {
                bStartedWaiting.countDown();
                CompletableFuture<Void> teardownB = gate.acquire();
                recognizerStartCountB.incrementAndGet();
                return teardownB;
            });

            assertTrue(bStartedWaiting.await(5, TimeUnit.SECONDS));
            Thread.sleep(80);
            assertEquals(0, recognizerStartCountB.get());
            assertEquals(1, gate.recognizerStartCount());
            assertFalse(sessionB.isDone());

            gate.finish(teardownA);

            CompletableFuture<Void> teardownB = sessionB.get(5, TimeUnit.SECONDS);
            assertEquals(1, recognizerStartCountB.get());
            assertEquals(2, gate.recognizerStartCount());
            gate.finish(teardownB);
        } finally {
            pool.shutdownNow();
        }
    }
}
