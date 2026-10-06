package com.markrai.scrumboy.transport;

import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicBoolean;
import okhttp3.Call;

final class NativeCallRegistry {
    static final class Operation {
        private final String id;
        private final AtomicBoolean cancelled = new AtomicBoolean(false);
        private volatile Call activeCall;

        private Operation(String id) {
            this.id = id;
        }

        String id() {
            return id;
        }

        boolean attach(Call call) {
            activeCall = call;
            if (cancelled.get()) {
                call.cancel();
                return false;
            }
            return true;
        }

        boolean cancelled() {
            return cancelled.get();
        }

        void cancel() {
            cancelled.set(true);
            Call call = activeCall;
            if (call != null) call.cancel();
        }
    }

    private final ConcurrentHashMap<String, Operation> operations = new ConcurrentHashMap<>();

    Operation begin(String id) throws TransportException {
        if (id == null || id.trim().isEmpty()) throw new TransportException("invalid_url", "A request ID is required");
        Operation operation = new Operation(id);
        if (operations.putIfAbsent(id, operation) != null) {
            throw new TransportException("invalid_url", "Duplicate request ID");
        }
        return operation;
    }

    void cancel(String id) {
        Operation operation = operations.get(id);
        if (operation != null) operation.cancel();
    }

    void complete(Operation operation) {
        operations.remove(operation.id(), operation);
    }

    void cancelAll() {
        for (Operation operation : operations.values()) operation.cancel();
        operations.clear();
    }
}
