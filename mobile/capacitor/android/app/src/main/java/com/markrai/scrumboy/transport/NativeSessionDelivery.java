package com.markrai.scrumboy.transport;

import java.io.InterruptedIOException;

final class NativeSessionDelivery {
    private final NativeSessionGeneration generation;

    NativeSessionDelivery(NativeSessionGeneration generation) {
        this.generation = generation;
    }

    void resolveCurrent(long expected, Runnable delivery) throws InterruptedIOException {
        if (!generation.runIfCurrent(expected, delivery)) {
            throw new InterruptedIOException("session invalidated");
        }
    }

    boolean emitCurrent(long expected, Runnable delivery) {
        return generation.runIfCurrent(expected, delivery);
    }
}
