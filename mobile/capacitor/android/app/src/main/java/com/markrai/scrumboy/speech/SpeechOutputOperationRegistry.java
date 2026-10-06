package com.markrai.scrumboy.speech;

final class SpeechOutputOperationRegistry {
    static final class Operation {
        final String operationId;
        final String deliveryId;
        private boolean terminal;

        Operation(String operationId, String deliveryId) {
            this.operationId = operationId;
            this.deliveryId = deliveryId;
        }
    }

    private Operation active;

    synchronized Operation begin(String operationId, String deliveryId) throws SpeechOutputException {
        validateOperationId(operationId);
        validateDeliveryId(deliveryId);
        if (active != null) throw new SpeechOutputException("busy", true);
        active = new Operation(operationId, deliveryId);
        return active;
    }

    synchronized Operation claim(String operationId) {
        if (active == null || !active.operationId.equals(operationId) || active.terminal) return null;
        Operation operation = active;
        operation.terminal = true;
        active = null;
        return operation;
    }

    synchronized Operation cancel(String operationId) {
        if (operationId == null || active == null || active.terminal) return null;
        if (!active.operationId.equals(operationId)) return null;
        Operation operation = active;
        operation.terminal = true;
        active = null;
        return operation;
    }

    synchronized Operation invalidate() {
        if (active == null || active.terminal) return null;
        Operation operation = active;
        operation.terminal = true;
        active = null;
        return operation;
    }

    synchronized boolean isActive(Operation operation) {
        return active == operation && !operation.terminal;
    }

    synchronized int activeCount() {
        return active == null ? 0 : 1;
    }

    private static void validateOperationId(String operationId) throws SpeechOutputException {
        if (
            operationId == null
            || operationId.isEmpty()
            || operationId.length() > 128
            || !operationId.matches("^[A-Za-z0-9._:-]+$")
        ) {
            throw new SpeechOutputException("invalid_request", false);
        }
    }

    private static void validateDeliveryId(String deliveryId) throws SpeechOutputException {
        if (deliveryId == null || deliveryId.isEmpty() || deliveryId.length() > 256) {
            throw new SpeechOutputException("invalid_request", false);
        }
    }
}
