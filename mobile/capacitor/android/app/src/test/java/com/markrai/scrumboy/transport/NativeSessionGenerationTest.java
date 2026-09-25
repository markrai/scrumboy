package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class NativeSessionGenerationTest {
    @Test
    public void responseWaitingToResolveCannotWinAfterServerResetInvalidatesSession() throws Exception {
        NativeSessionGeneration generation = new NativeSessionGeneration();
        long requestGeneration = generation.capture();
        CountDownLatch responseReady = new CountDownLatch(1);
        CountDownLatch allowCompletion = new CountDownLatch(1);
        AtomicInteger resolutions = new AtomicInteger();
        ExecutorService worker = Executors.newSingleThreadExecutor();

        Future<Boolean> completion = worker.submit(() -> {
            responseReady.countDown();
            assertTrue(allowCompletion.await(5, TimeUnit.SECONDS));
            return generation.runIfCurrent(requestGeneration, resolutions::incrementAndGet);
        });

        assertTrue(responseReady.await(5, TimeUnit.SECONDS));
        generation.invalidate(); // resetForServerChange invalidates before cancelling native calls.
        allowCompletion.countDown();

        assertFalse(completion.get(5, TimeUnit.SECONDS));
        assertEquals(0, resolutions.get());
        worker.shutdownNow();
    }

    @Test
    public void sseMessageAndErrorCallbacksCannotEmitAfterServerReset() {
        NativeSessionGeneration generation = new NativeSessionGeneration();
        long streamGeneration = generation.capture();
        AtomicInteger emitted = new AtomicInteger();

        generation.invalidate();

        assertFalse(generation.runIfCurrent(streamGeneration, emitted::incrementAndGet));
        assertFalse(generation.runIfCurrent(streamGeneration, emitted::incrementAndGet));
        assertEquals(0, emitted.get());
    }
}
