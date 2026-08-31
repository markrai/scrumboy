package com.markrai.scrumboy.speech;

import java.util.concurrent.atomic.AtomicBoolean;

final class SpeechInputOperationRegistry {
    static final class Operation {
        private final String operationId;
        private final Runnable cancellationDelivery;
        private final AtomicBoolean terminal = new AtomicBoolean(false);
        private volatile Runnable nativeCancellation;

        private Operation(String operationId, Runnable cancellationDelivery) {
            this.operationId = operationId;
            this.cancellationDelivery = cancellationDelivery;
        }

        void attachNativeCancellation(Runnable cancellation) {
            nativeCancellation = cancellation;
            if (terminal.get()) cancellation.run();
        }

        void deliverCancellation() {
            cancellationDelivery.run();
        }

        private void cancelNative() {
            Runnable cancellation = nativeCancellation;
            if (cancellation != null) cancellation.run();
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

    synchronized Operation cancel(String operationId) {
        if (active == null || operationId == null || !active.operationId.equals(operationId)) return null;
        Operation operation = active;
        active = null;
        if (!operation.terminal.compareAndSet(false, true)) return null;
        operation.cancelNative();
        return operation;
    }

    synchronized Operation invalidate() {
        if (active == null) return null;
        Operation operation = active;
        active = null;
        if (!operation.terminal.compareAndSet(false, true)) return null;
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
