package com.markrai.scrumboy.localai;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import com.google.common.util.concurrent.ListenableFuture;
import com.google.mlkit.genai.common.DownloadCallback;
import com.google.mlkit.genai.common.FeatureStatus;
import com.google.mlkit.genai.common.GenAiException;
import com.google.mlkit.genai.common.StreamingCallback;
import com.google.mlkit.genai.prompt.CountTokensResponse;
import com.google.mlkit.genai.prompt.GenerateContentRequest;
import com.google.mlkit.genai.prompt.GenerateContentResponse;
import com.google.mlkit.genai.prompt.GenerativeModel;
import com.google.mlkit.genai.prompt.java.GenerativeModelFutures;
import java.util.ArrayDeque;
import java.util.concurrent.CancellationException;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Executor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.junit.Test;

public class MlKitPromptProviderTest {
    @Test
    public void mapsAllFourFeatureStatusesWithoutADeviceAllowlist() throws Exception {
        FakeModel model = new FakeModel();
        MlKitPromptProvider provider = new MlKitPromptProvider(model);

        model.status = FeatureStatus.AVAILABLE;
        assertEquals("ready", provider.status(operation("available")).state());
        model.status = FeatureStatus.DOWNLOADABLE;
        assertEquals("action-required", provider.status(operation("downloadable")).state());
        model.status = FeatureStatus.DOWNLOADING;
        assertEquals("preparing", provider.status(operation("downloading")).state());
        model.status = FeatureStatus.UNAVAILABLE;
        assertEquals("temporarily-unavailable", provider.status(operation("unavailable")).state());
    }

    @Test
    public void mapsNonPixelStyleStatusFailureWithoutCrashing() throws Exception {
        FakeModel model = new FakeModel();
        model.statusFailure = new GenAiException(new RuntimeException(), GenAiException.ErrorCode.NOT_AVAILABLE);

        LocalAiStatus status = new MlKitPromptProvider(model).status(operation("status-failure"));

        assertEquals("temporarily-unavailable", status.state());
        assertEquals("initializing", status.detail());
    }

    @Test
    public void preparationIsNoOpWhenReadyAndTruthfullyMapsCallbackFailure() throws Exception {
        FakeModel ready = new FakeModel();
        ready.status = FeatureStatus.AVAILABLE;
        new MlKitPromptProvider(ready).prepare(operation("ready"));
        assertEquals(0, ready.downloadCalls);

        FakeModel failing = new FakeModel();
        failing.statuses.add(FeatureStatus.DOWNLOADABLE);
        failing.downloadFailure = new GenAiException(new RuntimeException(), GenAiException.ErrorCode.UNKNOWN);
        LocalAiException error = assertThrows(
            LocalAiException.class,
            () -> new MlKitPromptProvider(failing).prepare(operation("download"))
        );
        assertEquals("download_failed", error.code());
        assertEquals(1, failing.downloadCalls);
    }

    @Test
    public void preparationObservesDownloadLifecycleAndPreservesSpecificFailure() throws Exception {
        FakeModel success = new FakeModel();
        success.statuses.add(FeatureStatus.DOWNLOADABLE);
        success.statuses.add(FeatureStatus.AVAILABLE);

        new MlKitPromptProvider(success).prepare(operation("download-success"));

        assertTrue(success.downloadStarted);
        assertTrue(success.downloadProgressed);
        assertTrue(success.downloadCompleted);

        FakeModel storage = new FakeModel();
        storage.statuses.add(FeatureStatus.DOWNLOADABLE);
        storage.downloadFailure = new GenAiException(
            new RuntimeException(),
            GenAiException.ErrorCode.NOT_ENOUGH_DISK_SPACE
        );
        LocalAiException error = assertThrows(
            LocalAiException.class,
            () -> new MlKitPromptProvider(storage).prepare(operation("download-storage"))
        );
        assertEquals("insufficient_storage", error.code());
    }

    @Test
    public void usesDistinctSystemInstructionsWithoutFastPrefixCacheOrThinking() throws Exception {
        FakeModel model = new FakeModel();
        model.systemPromptAvailable = true;
        MlKitPromptProvider provider = new MlKitPromptProvider(model);

        LocalAiException error = assertThrows(
            LocalAiException.class,
            () -> provider.generate(operation("system"), "user input", "system rule", 32)
        );

        assertEquals("output_rejected", error.code());
        GenerateContentRequest request = model.generatedRequest;
        assertEquals("system rule", request.getSystemInstruction().getTextString());
        assertEquals("user input", request.getText().getTextString());
        assertEquals(32, request.getMaxOutputTokens());
        assertEquals(1, request.getCandidateCount());
        assertFalse(request.getEnableThinking());
        assertNull(request.getPromptPrefix());
        assertNull(request.getCachedContextName());
    }

    @Test
    public void deterministicallyComposesFallbackWhenSystemInstructionsAreUnavailable() throws Exception {
        FakeModel model = new FakeModel();
        model.systemPromptAvailable = false;

        assertThrows(
            LocalAiException.class,
            () -> new MlKitPromptProvider(model).generate(operation("fallback"), "user input", "system rule", 32)
        );

        assertNull(model.generatedRequest.getSystemInstruction());
        assertEquals(
            "System instructions:\nsystem rule\n\nUser input:\nuser input",
            model.generatedRequest.getText().getTextString()
        );
    }

    @Test
    public void tokenChecksRejectBeforeGeneration() throws Exception {
        FakeModel model = new FakeModel();
        model.totalTokens = 990;
        model.tokenLimit = 1_000;

        LocalAiException error = assertThrows(
            LocalAiException.class,
            () -> new MlKitPromptProvider(model).generate(operation("too-large"), "input", "", 32)
        );

        assertEquals("input_too_large", error.code());
        assertEquals(0, model.generateCalls);
    }

    @Test
    public void cancelledFutureMapsToStableCancellation() throws Exception {
        FakeModel model = new FakeModel();
        model.statusFutureCancelled = true;

        LocalAiException error = assertThrows(
            LocalAiException.class,
            () -> new MlKitPromptProvider(model).generate(operation("cancelled"), "input", "", 16)
        );

        assertEquals("cancelled", error.code());
    }

    private static LocalAiOperationRegistry.Operation operation(String suffix) throws Exception {
        return new LocalAiOperationRegistry().begin(
            "operation-" + suffix,
            null,
            LocalAiOperationRegistry.Kind.STATUS,
            () -> {}
        );
    }

    private static final class FakeModel extends GenerativeModelFutures {
        int status = FeatureStatus.AVAILABLE;
        final ArrayDeque<Integer> statuses = new ArrayDeque<>();
        Throwable statusFailure;
        boolean statusFutureCancelled;
        boolean systemPromptAvailable;
        int totalTokens = 10;
        int tokenLimit = 4_096;
        int downloadCalls;
        int generateCalls;
        boolean downloadStarted;
        boolean downloadProgressed;
        boolean downloadCompleted;
        GenAiException downloadFailure;
        GenerateContentRequest generatedRequest;

        @Override
        public ListenableFuture<String> getBaseModelName() {
            return ImmediateFuture.success("nano-v4");
        }

        @Override
        public ListenableFuture<Integer> checkStatus() {
            if (statusFutureCancelled) return ImmediateFuture.cancelled();
            if (statusFailure != null) return ImmediateFuture.failure(statusFailure);
            return ImmediateFuture.success(statuses.isEmpty() ? status : statuses.removeFirst());
        }

        @Override
        public ListenableFuture<Void> download(DownloadCallback callback) {
            downloadCalls += 1;
            if (downloadFailure != null) {
                callback.onDownloadFailed(downloadFailure);
            } else {
                callback.onDownloadStarted(100);
                downloadStarted = true;
                callback.onDownloadProgress(50);
                downloadProgressed = true;
                callback.onDownloadCompleted();
                downloadCompleted = true;
            }
            return ImmediateFuture.success(null);
        }

        @Override
        public ListenableFuture<Void> warmup() {
            throw new AssertionError("C5.1 must never warm up the model");
        }

        @Override
        public ListenableFuture<CountTokensResponse> countTokens(GenerateContentRequest request) {
            return ImmediateFuture.success(new CountTokensResponse(totalTokens));
        }

        @Override
        public ListenableFuture<Integer> getTokenLimit() {
            return ImmediateFuture.success(tokenLimit);
        }

        @Override
        public ListenableFuture<Boolean> isSystemPromptAvailable() {
            return ImmediateFuture.success(systemPromptAvailable);
        }

        @Override
        public ListenableFuture<Boolean> isThinkingModeAvailable() {
            return ImmediateFuture.success(false);
        }

        @Override
        public ListenableFuture<GenerateContentResponse> generateContent(GenerateContentRequest request) {
            generatedRequest = request;
            generateCalls += 1;
            return ImmediateFuture.failure(new GenAiException(
                new RuntimeException(),
                GenAiException.ErrorCode.RESPONSE_GENERATION_ERROR
            ));
        }

        @Override
        public ListenableFuture<GenerateContentResponse> generateContent(
            GenerateContentRequest request,
            StreamingCallback callback
        ) {
            throw new AssertionError("C5.1 must never stream generation");
        }

        @Override
        public ListenableFuture<GenerateContentResponse> generateContent(String input) {
            throw new AssertionError("C5.1 must use a bounded request object");
        }

        @Override
        public ListenableFuture<GenerateContentResponse> generateContent(String input, StreamingCallback callback) {
            throw new AssertionError("C5.1 must never stream generation");
        }

        @Override
        public GenerativeModel getGenerativeModel() {
            return null;
        }

        @Override
        public ListenableFuture<Void> clearImplicitCaches() {
            throw new AssertionError("C5.1 does not use implicit caches");
        }
    }

    private static final class ImmediateFuture<T> implements ListenableFuture<T> {
        private final T value;
        private final Throwable failure;
        private boolean cancelled;

        private ImmediateFuture(T value, Throwable failure, boolean cancelled) {
            this.value = value;
            this.failure = failure;
            this.cancelled = cancelled;
        }

        static <T> ImmediateFuture<T> success(T value) {
            return new ImmediateFuture<>(value, null, false);
        }

        static <T> ImmediateFuture<T> failure(Throwable failure) {
            return new ImmediateFuture<>(null, failure, false);
        }

        static <T> ImmediateFuture<T> cancelled() {
            return new ImmediateFuture<>(null, null, true);
        }

        @Override
        public void addListener(Runnable listener, Executor executor) {
            executor.execute(listener);
        }

        @Override
        public boolean cancel(boolean mayInterruptIfRunning) {
            cancelled = true;
            return true;
        }

        @Override
        public boolean isCancelled() {
            return cancelled;
        }

        @Override
        public boolean isDone() {
            return true;
        }

        @Override
        public T get() throws ExecutionException {
            if (cancelled) throw new CancellationException();
            if (failure != null) throw new ExecutionException(failure);
            return value;
        }

        @Override
        public T get(long timeout, TimeUnit unit) throws ExecutionException, TimeoutException {
            return get();
        }
    }
}
