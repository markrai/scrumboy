package com.markrai.scrumboy.speech;

import java.util.concurrent.CompletableFuture;

/**
 * Serializes Advanced recognizer sessions: the next start waits for the previous
 * stop/close to settle. Teardown is awaitable and is not fire-and-forget.
 *
 * {@link #acquire()} must run off the Android main thread. It throws
 * {@link InterruptedException} so a cancelled coroutine waiting inside
 * {@code runInterruptible} unwinds as cancellation without taking gate ownership.
 */
final class AdvancedRecognizerSessionGate {
    private final Object lock = new Object();
    private boolean busy;
    private int recognizerStartCount;

    /**
     * Blocks until the previous session has finished native teardown, then records
     * that this session is allowed to create/start a recognizer.
     */
    CompletableFuture<Void> acquire() throws InterruptedException {
        synchronized (lock) {
            while (busy) {
                lock.wait();
            }
            busy = true;
            recognizerStartCount += 1;
            return new CompletableFuture<>();
        }
    }

    int recognizerStartCount() {
        synchronized (lock) {
            return recognizerStartCount;
        }
    }

    boolean isBusy() {
        synchronized (lock) {
            return busy;
        }
    }

    void finish(CompletableFuture<Void> teardown) {
        synchronized (lock) {
            if (teardown != null && !teardown.isDone()) teardown.complete(null);
            busy = false;
            lock.notifyAll();
        }
    }
}
