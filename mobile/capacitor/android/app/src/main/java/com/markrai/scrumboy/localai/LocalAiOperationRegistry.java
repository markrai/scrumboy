package com.markrai.scrumboy.localai;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.Future;
import java.util.concurrent.atomic.AtomicBoolean;

final class LocalAiOperationRegistry {
    enum Kind {
        STATUS,
        PREPARE,
        GENERATE
    }

    static final class Operation {
        private final String operationId;
        private final String requestId;
        private final Kind kind;
        private final long generation;
        private final Runnable cancellationDelivery;
        private final AtomicBoolean terminal = new AtomicBoolean(false);
        private volatile Future<?> future;

        private Operation(
            String operationId,
            String requestId,
            Kind kind,
            long generation,
            Runnable cancellationDelivery
        ) {
            this.operationId = operationId;
            this.requestId = requestId;
            this.kind = kind;
            this.generation = generation;
            this.cancellationDelivery = cancellationDelivery;
        }

        String operationId() {
            return operationId;
        }

        String requestId() {
            return requestId;
        }

        Kind kind() {
            return kind;
        }

        boolean cancelled() {
            return terminal.get();
        }

        void attach(Future<?> next) {
            future = next;
            if (terminal.get()) next.cancel(true);
        }

        private void cancelFuture() {
            Future<?> current = future;
            if (current != null) current.cancel(true);
        }

        void deliverCancellation() {
            cancellationDelivery.run();
        }
    }

    private static final int RECENT_LIMIT = 256;
    private final ArrayDeque<String> recentOperationOrder = new ArrayDeque<>();
    private final Set<String> recentOperationIds = new HashSet<>();
    private final ArrayDeque<String> recentRequestOrder = new ArrayDeque<>();
    private final Set<String> recentRequestIds = new HashSet<>();
    private long generation;
    private Operation active;

    synchronized Operation begin(
        String operationId,
        String requestId,
        Kind kind,
        Runnable cancellationDelivery
    ) throws LocalAiException {
        LocalAiRequestValidator.operationId(operationId);
        if (requestId != null) LocalAiRequestValidator.requestId(requestId);
        if (active != null) throw new LocalAiException("busy", true);
        if (recentOperationIds.contains(operationId) || (requestId != null && recentRequestIds.contains(requestId))) {
            throw new LocalAiException("invalid_request", false);
        }
        active = new Operation(operationId, requestId, kind, generation, cancellationDelivery);
        return active;
    }

    synchronized boolean claimCompletion(Operation operation) {
        if (active != operation || operation.generation != generation || !operation.terminal.compareAndSet(false, true)) {
            return false;
        }
        active = null;
        remember(operation);
        return true;
    }

    synchronized Operation cancel(String operationId) {
        if (active == null || operationId == null || !active.operationId.equals(operationId)) return null;
        generation += 1;
        Operation operation = active;
        active = null;
        if (!operation.terminal.compareAndSet(false, true)) return null;
        remember(operation);
        operation.cancelFuture();
        return operation;
    }

    synchronized List<Operation> invalidate() {
        generation += 1;
        List<Operation> cancelled = new ArrayList<>();
        if (active != null) {
            Operation operation = active;
            active = null;
            if (operation.terminal.compareAndSet(false, true)) {
                remember(operation);
                operation.cancelFuture();
                cancelled.add(operation);
            }
        }
        return cancelled;
    }

    synchronized List<Operation> destroy() {
        List<Operation> cancelled = invalidate();
        recentOperationOrder.clear();
        recentOperationIds.clear();
        recentRequestOrder.clear();
        recentRequestIds.clear();
        return cancelled;
    }

    synchronized int activeCount() {
        return active == null ? 0 : 1;
    }

    private void remember(Operation operation) {
        remember(operation.operationId, recentOperationOrder, recentOperationIds);
        if (operation.requestId != null) remember(operation.requestId, recentRequestOrder, recentRequestIds);
    }

    private static void remember(String id, ArrayDeque<String> order, Set<String> ids) {
        ids.add(id);
        order.addLast(id);
        if (order.size() > RECENT_LIMIT) ids.remove(order.removeFirst());
    }
}
