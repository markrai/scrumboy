package com.markrai.scrumboy.speech;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executor;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

/**
 * Advanced session install and the one platform-after-barrier pathway.
 * Publication, exact cancellation, and barrier registration happen before start.
 * There is no combined publish-and-start helper.
 */
final class AdvancedRecognitionOwnership {
    interface Session {
        void start();

        void cancel();

        CompletableFuture<Void> teardownFuture();
    }

    private AdvancedRecognitionOwnership() {}

    /**
     * Required order: publish exact handle, attach exact cancellation, register
     * teardown barrier, recheck ownership, then start. If the operation is already
     * terminal, {@code attachNativeCancellation} invokes cancel immediately.
     */
    static <T extends Session> void publishAttachRegisterThenStartIfOwned(
        T[] slot,
        T prepared,
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        AdvancedTeardownBarrier barrier
    ) {
        slot[0] = prepared;
        operation.attachNativeCancellation(prepared::cancel);
        barrier.register(prepared, prepared.teardownFuture());
        if (!operations.isActive(operation)) {
            prepared.cancel();
            return;
        }
        prepared.start();
    }

    /**
     * Awaits the outstanding Advanced native teardown off Main, then starts platform
     * recognition on Main only if the operation and Activity are still valid.
     */
    static void awaitBarrierThenRunOnMain(
        CompletableFuture<Void> barrier,
        Executor background,
        BooleanSupplier stillOwned,
        Consumer<Runnable> runOnMain,
        Runnable startPlatformOnMain
    ) {
        CompletableFuture<Void> settled = barrier == null
            ? CompletableFuture.completedFuture(null)
            : barrier;
        background.execute(() -> {
            try {
                settled.join();
            } catch (Throwable ignored) {
                // Settlement is the handoff point even when close reports an error.
            }
            if (!stillOwned.getAsBoolean()) return;
            runOnMain.accept(() -> {
                if (!stillOwned.getAsBoolean()) return;
                startPlatformOnMain.run();
            });
        });
    }
}
