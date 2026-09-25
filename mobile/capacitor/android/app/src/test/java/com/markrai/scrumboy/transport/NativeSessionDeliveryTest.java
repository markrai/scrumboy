package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.io.InterruptedIOException;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class NativeSessionDeliveryTest {
    @Test
    public void requestResponseReadyAfterResetCannotResolveThroughPluginDeliveryPath() throws Exception {
        NativeSessionGeneration generation = new NativeSessionGeneration();
        NativeSessionDelivery delivery = new NativeSessionDelivery(generation);
        long requestGeneration = generation.capture();
        CountDownLatch responseReady = new CountDownLatch(1);
        CountDownLatch allowDelivery = new CountDownLatch(1);
        AtomicInteger resolvedResponses = new AtomicInteger();
        ExecutorService worker = Executors.newSingleThreadExecutor();

        try {
            Future<?> completion = worker.submit(() -> {
                responseReady.countDown();
                assertTrue(allowDelivery.await(5, TimeUnit.SECONDS));
                assertThrows(
                    InterruptedIOException.class,
                    () -> delivery.resolveCurrent(requestGeneration, resolvedResponses::incrementAndGet)
                );
                return null;
            });

            assertTrue(responseReady.await(5, TimeUnit.SECONDS));
            generation.invalidate();
            allowDelivery.countDown();
            completion.get(5, TimeUnit.SECONDS);
            assertEquals(0, resolvedResponses.get());
        } finally {
            worker.shutdownNow();
        }
    }

    @Test
    public void sseMessageReadyAfterResetCannotEmitThroughPluginDeliveryPath() {
        NativeSessionGeneration generation = new NativeSessionGeneration();
        NativeSessionDelivery delivery = new NativeSessionDelivery(generation);
        long streamGeneration = generation.capture();
        AtomicInteger bridgeEvents = new AtomicInteger();

        generation.invalidate();

        assertFalse(delivery.emitCurrent(streamGeneration, bridgeEvents::incrementAndGet));
        assertEquals(0, bridgeEvents.get());
    }

    @Test
    public void sseFailureReadyAfterResetCannotEmitThroughPluginDeliveryPath() {
        NativeSessionGeneration generation = new NativeSessionGeneration();
        NativeSessionDelivery delivery = new NativeSessionDelivery(generation);
        long streamGeneration = generation.capture();
        AtomicInteger bridgeEvents = new AtomicInteger();

        generation.invalidate();

        assertFalse(delivery.emitCurrent(streamGeneration, bridgeEvents::incrementAndGet));
        assertEquals(0, bridgeEvents.get());
    }
}
