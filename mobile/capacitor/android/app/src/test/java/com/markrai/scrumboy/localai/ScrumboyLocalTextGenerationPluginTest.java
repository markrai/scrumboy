package com.markrai.scrumboy.localai;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.FutureTask;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.Test;

public class ScrumboyLocalTextGenerationPluginTest {
    @Test
    public void pauseCancelsActiveWorkAndDestroyClosesAllOwnedResources() throws Exception {
        ExecutorService worker = Executors.newSingleThreadExecutor();
        LocalAiOperationRegistry operations = new LocalAiOperationRegistry();
        FakeProvider provider = new FakeProvider();
        ScrumboyLocalTextGenerationPlugin plugin = new ScrumboyLocalTextGenerationPlugin(
            worker,
            operations,
            provider
        );
        AtomicBoolean cancellationDelivered = new AtomicBoolean();
        LocalAiOperationRegistry.Operation operation = operations.begin(
            "operation-1",
            "request-1",
            LocalAiOperationRegistry.Kind.GENERATE,
            () -> cancellationDelivered.set(true)
        );
        FutureTask<Void> future = new FutureTask<>(() -> null);
        operation.attach(future);

        plugin.handleOnPause();

        assertTrue(cancellationDelivered.get());
        assertTrue(future.isCancelled());
        assertFalse(provider.closed);

        plugin.handleOnDestroy();

        assertTrue(provider.closed);
        assertTrue(worker.isShutdown());
    }

    private static final class FakeProvider implements LocalTextGenerationProvider {
        boolean closed;

        @Override
        public LocalAiStatus status(LocalAiOperationRegistry.Operation operation) {
            return LocalAiStatus.temporarilyUnavailable("provider", null);
        }

        @Override
        public void prepare(LocalAiOperationRegistry.Operation operation) {}

        @Override
        public String generate(
            LocalAiOperationRegistry.Operation operation,
            String input,
            String instructions,
            int maximumOutputTokens
        ) {
            return "generated";
        }

        @Override
        public void close() {
            closed = true;
        }
    }
}
