package com.markrai.scrumboy.localai;

import com.google.common.util.concurrent.ListenableFuture;
import com.google.mlkit.genai.common.DownloadCallback;
import com.google.mlkit.genai.common.FeatureStatus;
import com.google.mlkit.genai.common.GenAiException;
import com.google.mlkit.genai.prompt.Candidate;
import com.google.mlkit.genai.prompt.CountTokensResponse;
import com.google.mlkit.genai.prompt.GenerateContentRequest;
import com.google.mlkit.genai.prompt.GenerateContentResponse;
import com.google.mlkit.genai.prompt.Generation;
import com.google.mlkit.genai.prompt.SystemInstruction;
import com.google.mlkit.genai.prompt.TextPart;
import com.google.mlkit.genai.prompt.java.GenerativeModelFutures;
import java.util.List;
import java.util.concurrent.CancellationException;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.atomic.AtomicReference;

final class MlKitPromptProvider implements LocalTextGenerationProvider {
    private final GenerativeModelFutures model;
    private volatile Long downloadedBytes;
    private volatile Long totalBytes;

    MlKitPromptProvider() {
        // Intentionally omit GenerationConfig/ModelConfig. This uses the stable default,
        // does not select FAST, and never enables preview or prefix caching.
        this(GenerativeModelFutures.from(Generation.INSTANCE.getClient()));
    }

    MlKitPromptProvider(GenerativeModelFutures model) {
        this.model = model;
    }

    @Override
    public LocalAiStatus status(LocalAiOperationRegistry.Operation operation) throws LocalAiException {
        final int featureStatus;
        try {
            featureStatus = awaitRaw(operation, model.checkStatus());
        } catch (Throwable error) {
            LocalAiStatus status = MlKitErrorMapper.status(error);
            if (status != null) return status;
            throw MlKitErrorMapper.map(error);
        }
        if (featureStatus == FeatureStatus.AVAILABLE) {
            Integer tokenLimit = diagnostic(operation, model.getTokenLimit());
            String modelName = diagnostic(operation, model.getBaseModelName());
            return LocalAiStatus.ready(tokenLimit, modelName);
        }
        if (featureStatus == FeatureStatus.DOWNLOADABLE) return LocalAiStatus.actionRequired("download");
        if (featureStatus == FeatureStatus.DOWNLOADING) {
            return LocalAiStatus.preparing(downloadedBytes, totalBytes);
        }
        return LocalAiStatus.temporarilyUnavailable("provider", null);
    }

    @Override
    public void prepare(LocalAiOperationRegistry.Operation operation) throws LocalAiException {
        final int featureStatus;
        try {
            featureStatus = awaitRaw(operation, model.checkStatus());
        } catch (Throwable error) {
            throw MlKitErrorMapper.map(error);
        }
        if (featureStatus == FeatureStatus.AVAILABLE) return;
        if (featureStatus != FeatureStatus.DOWNLOADABLE) {
            throw new LocalAiException("not_ready", true);
        }

        downloadedBytes = null;
        totalBytes = null;
        AtomicReference<GenAiException> callbackFailure = new AtomicReference<>();
        try {
            ListenableFuture<Void> download = model.download(new DownloadCallback() {
                @Override
                public void onDownloadStarted(long bytesToDownload) {
                    downloadedBytes = 0L;
                    totalBytes = nonNegative(bytesToDownload);
                }

                @Override
                public void onDownloadProgress(long bytesDownloaded) {
                    downloadedBytes = nonNegative(bytesDownloaded);
                }

                @Override
                public void onDownloadCompleted() {
                    if (totalBytes != null) downloadedBytes = totalBytes;
                }

                @Override
                public void onDownloadFailed(GenAiException error) {
                    callbackFailure.compareAndSet(null, error);
                }
            });
            awaitRaw(operation, download);
            if (callbackFailure.get() != null) {
                throw preparationFailure(callbackFailure.get());
            }
            if (awaitRaw(operation, model.checkStatus()) != FeatureStatus.AVAILABLE) {
                throw new LocalAiException("not_ready", true);
            }
        } catch (Throwable error) {
            if (error instanceof LocalAiException localError) throw localError;
            throw preparationFailure(error);
        }
    }

    @Override
    public String generate(
        LocalAiOperationRegistry.Operation operation,
        String input,
        String instructions,
        int maximumOutputTokens
    ) throws LocalAiException {
        try {
            if (awaitRaw(operation, model.checkStatus()) != FeatureStatus.AVAILABLE) {
                throw new LocalAiException("not_ready", true);
            }

            boolean systemPromptAvailable = false;
            if (!instructions.isEmpty()) {
                try {
                    systemPromptAvailable = Boolean.TRUE.equals(awaitRaw(operation, model.isSystemPromptAvailable()));
                } catch (Throwable error) {
                    LocalAiException mapped = MlKitErrorMapper.map(error);
                    if (mapped.code().equals("cancelled")) throw mapped;
                    systemPromptAvailable = false;
                }
            }

            String providerInput = systemPromptAvailable || instructions.isEmpty()
                ? input
                : composeFallback(instructions, input);
            GenerateContentRequest.Builder builder = new GenerateContentRequest.Builder(new TextPart(providerInput));
            if (systemPromptAvailable) builder.setSystemInstruction(new SystemInstruction(instructions));
            builder.setCandidateCount(1);
            builder.setMaxOutputTokens(maximumOutputTokens);
            builder.setEnableThinking(false);
            GenerateContentRequest request = builder.build();

            CountTokensResponse tokens = awaitRaw(operation, model.countTokens(request));
            int tokenLimit = awaitRaw(operation, model.getTokenLimit());
            if ((long) tokens.getTotalTokens() + maximumOutputTokens > tokenLimit) {
                throw new LocalAiException("input_too_large", false);
            }

            GenerateContentResponse response = awaitRaw(operation, model.generateContent(request));
            List<Candidate> candidates = response.getCandidates();
            if (candidates == null || candidates.isEmpty()) {
                throw new LocalAiException("output_rejected", false);
            }
            return LocalAiRequestValidator.output(candidates.get(0).getText());
        } catch (LocalAiException error) {
            throw error;
        } catch (Throwable error) {
            throw MlKitErrorMapper.map(error);
        }
    }

    private static String composeFallback(String instructions, String input) {
        return "System instructions:\n" + instructions + "\n\nUser input:\n" + input;
    }

    private static Long nonNegative(long value) {
        return value >= 0 ? value : null;
    }

    private static LocalAiException preparationFailure(Throwable error) {
        LocalAiException mapped = MlKitErrorMapper.map(error);
        return switch (mapped.code()) {
            case "cancelled", "unsupported", "not_ready", "foreground_required", "busy",
                "quota_exceeded", "insufficient_storage" -> mapped;
            default -> new LocalAiException("download_failed", true, mapped.retryAfterMs(), error);
        };
    }

    private <T> T diagnostic(LocalAiOperationRegistry.Operation operation, ListenableFuture<T> future)
        throws LocalAiException {
        try {
            return awaitRaw(operation, future);
        } catch (Throwable error) {
            LocalAiException mapped = MlKitErrorMapper.map(error);
            if (mapped.code().equals("cancelled")) throw mapped;
            return null;
        }
    }

    private static <T> T awaitRaw(LocalAiOperationRegistry.Operation operation, ListenableFuture<T> future)
        throws InterruptedException, ExecutionException, CancellationException {
        operation.attach(future);
        try {
            return future.get();
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw error;
        }
    }

    @Override
    public void close() {
        model.getGenerativeModel().close();
    }
}
