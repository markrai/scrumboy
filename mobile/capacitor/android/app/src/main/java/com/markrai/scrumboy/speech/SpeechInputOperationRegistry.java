package com.markrai.scrumboy.speech;

import java.util.concurrent.atomic.AtomicBoolean;

final class SpeechInputOperationRegistry {
    static final class Operation {
        private final String operationId;
        private final Runnable cancellationDelivery;
        private final AtomicBoolean terminal = new AtomicBoolean(false);
        private final Object cancellationLock = new Object();
        private Runnable nativeCancellation;
        private boolean nativeCancellationDispatched;

        private Operation(String operationId, Runnable cancellationDelivery) {
            this.operationId = operationId;
            this.cancellationDelivery = cancellationDelivery;
        }

        /**
         * Registers the exact native cancellation for this operation. Attachment and
         * {@link #cancelNative()} can race across threads (cancellation arrives on
         * Capacitor's plugin handler thread while the recognizer is installed on Main),
         * so the callback is dispatched exactly once whichever side observes the other
         * first, and always outside the registry lock.
         */
        void attachNativeCancellation(Runnable cancellation) {
            boolean dispatchNow;
            synchronized (cancellationLock) {
                nativeCancellation = cancellation;
                dispatchNow = terminal.get() && !nativeCancellationDispatched;
                if (dispatchNow) nativeCancellationDispatched = true;
            }
            if (dispatchNow) cancellation.run();
        }

        void deliverCancellation() {
            cancellationDelivery.run();
        }

        private void cancelNative() {
            Runnable cancellation;
            synchronized (cancellationLock) {
                if (nativeCancellationDispatched || nativeCancellation == null) return;
                nativeCancellationDispatched = true;
                cancellation = nativeCancellation;
            }
            cancellation.run();
        }
    }

    private Operation active;

    synchronized Operation begin(String operationId, Runnable cancellationDelivery) throws SpeechInputException {
        validateOperationId(operationId);
        if (active != null) throw new SpeechInputException("busy", true);
        active = new Operation(operationId, cancellationDelivery);
        return active;
    }

    synchronized boolean claimCompletion(Operation operation) {
        if (active != operation || !operation.terminal.compareAndSet(false, true)) return false;
        active = null;
        return true;
    }

    Operation cancel(String operationId) {
        Operation operation;
        synchronized (this) {
            if (active == null || operationId == null || !active.operationId.equals(operationId)) return null;
            operation = active;
            active = null;
            if (!operation.terminal.compareAndSet(false, true)) return null;
        }
        // Outside the lock: native release must never run while the registry is held.
        operation.cancelNative();
        return operation;
    }

    Operation invalidate() {
        Operation operation;
        synchronized (this) {
            if (active == null) return null;
            operation = active;
            active = null;
            if (!operation.terminal.compareAndSet(false, true)) return null;
        }
        operation.cancelNative();
        return operation;
    }

    synchronized int activeCount() {
        return active == null ? 0 : 1;
    }

    synchronized Operation active(String operationId) {
        if (active == null || operationId == null || !active.operationId.equals(operationId)) return null;
        return active;
    }

    synchronized boolean isActive(Operation operation) {
        return active == operation && !operation.terminal.get();
    }

    private static void validateOperationId(String operationId) throws SpeechInputException {
        if (operationId == null || operationId.isEmpty() || operationId.length() > 128) {
            throw new SpeechInputException("invalid_request", false);
        }
    }
}
