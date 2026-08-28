package com.markrai.scrumboy.transport;

final class NativeSessionGeneration {
    private long generation;

    synchronized long capture() {
        return generation;
    }

    synchronized long invalidate() {
        generation += 1;
        return generation;
    }

    synchronized boolean runIfCurrent(long expected, Runnable action) {
        if (generation != expected) return false;
        action.run();
        return true;
    }
}
