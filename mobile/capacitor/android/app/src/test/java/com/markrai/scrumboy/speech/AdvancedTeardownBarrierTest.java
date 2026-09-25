package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.io.IOException;
import java.util.concurrent.CompletableFuture;
import org.junit.Test;

public class AdvancedTeardownBarrierTest {
    @Test
    public void barrierWaitsForEveryOutstandingTeardownWhenNewerCompletesFirst() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        CompletableFuture<Void> teardownA = new CompletableFuture<>();
        CompletableFuture<Void> teardownB = new CompletableFuture<>();
        barrier.register(new Object(), teardownA);
        barrier.register(new Object(), teardownB);

        CompletableFuture<Void> snapshot = barrier.currentBarrier();
        teardownB.complete(null);
        assertFalse(snapshot.isDone());
        assertFalse(barrier.currentBarrier().isDone());

        teardownA.complete(null);
        assertTrue(snapshot.isDone());
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void barrierWaitsForEveryOutstandingTeardownWhenOlderCompletesFirst() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        CompletableFuture<Void> teardownA = new CompletableFuture<>();
        CompletableFuture<Void> teardownB = new CompletableFuture<>();
        barrier.register(new Object(), teardownA);
        barrier.register(new Object(), teardownB);

        CompletableFuture<Void> snapshot = barrier.currentBarrier();
        teardownA.complete(null);
        assertFalse(snapshot.isDone());
        assertFalse(barrier.currentBarrier().isDone());

        teardownB.complete(null);
        assertTrue(snapshot.isDone());
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void exceptionalTeardownStillWaitsForTheOtherSessionThenReleases() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        CompletableFuture<Void> teardownA = new CompletableFuture<>();
        CompletableFuture<Void> teardownB = new CompletableFuture<>();
        barrier.register(new Object(), teardownA);
        barrier.register(new Object(), teardownB);

        CompletableFuture<Void> snapshot = barrier.currentBarrier();
        teardownA.completeExceptionally(new IOException("close failed"));
        assertFalse(snapshot.isDone());

        teardownB.complete(null);
        assertTrue(snapshot.isDone());
        assertFalse(snapshot.isCompletedExceptionally());
    }

    @Test
    public void registeringANewerSessionCannotHideAnUnresolvedOlderSession() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        CompletableFuture<Void> teardownA = new CompletableFuture<>();
        barrier.register(new Object(), teardownA);
        barrier.register(new Object(), CompletableFuture.completedFuture(null));

        assertFalse(barrier.currentBarrier().isDone());
        teardownA.complete(null);
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void olderOwnerCannotClearAnotherOutstandingOwner() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        Object first = new Object();
        Object second = new Object();
        CompletableFuture<Void> firstTeardown = new CompletableFuture<>();
        CompletableFuture<Void> secondTeardown = new CompletableFuture<>();
        barrier.register(first, firstTeardown);
        barrier.register(second, secondTeardown);

        barrier.clearExact(first);
        assertEquals(1, barrier.outstandingCount());
        assertFalse(barrier.currentBarrier().isDone());

        barrier.clearExact(second);
        assertEquals(0, barrier.outstandingCount());
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void clearingTheNewerOwnerLeavesTheOlderOneRepresented() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        Object first = new Object();
        Object second = new Object();
        CompletableFuture<Void> firstTeardown = new CompletableFuture<>();
        barrier.register(first, firstTeardown);
        barrier.register(second, new CompletableFuture<>());

        barrier.clearExact(second);
        assertEquals(1, barrier.outstandingCount());
        assertFalse(barrier.currentBarrier().isDone());

        firstTeardown.complete(null);
        assertTrue(barrier.currentBarrier().isDone());
    }

    @Test
    public void settledSessionsAreRemovedSoTheOutstandingSetStaysBounded() {
        AdvancedTeardownBarrier barrier = new AdvancedTeardownBarrier();
        for (int index = 0; index < 20; index += 1) {
            CompletableFuture<Void> teardown = new CompletableFuture<>();
            barrier.register(new Object(), teardown);
            teardown.complete(null);
        }
        assertEquals(0, barrier.outstandingCount());
        assertTrue(barrier.currentBarrier().isDone());
    }
}
