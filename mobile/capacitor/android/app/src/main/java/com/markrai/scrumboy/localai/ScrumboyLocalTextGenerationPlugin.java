package com.markrai.scrumboy.localai;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;

@CapacitorPlugin(name = "ScrumboyLocalTextGeneration")
public class ScrumboyLocalTextGenerationPlugin extends Plugin {
    private final ExecutorService worker;
    private final LocalAiOperationRegistry operations;
    private final Object providerLock = new Object();
    private volatile boolean foreground;
    private volatile boolean destroyed;
    private LocalTextGenerationProvider provider;

    public ScrumboyLocalTextGenerationPlugin() {
        this(Executors.newSingleThreadExecutor(), new LocalAiOperationRegistry(), null);
    }

    ScrumboyLocalTextGenerationPlugin(
        ExecutorService worker,
        LocalAiOperationRegistry operations,
        LocalTextGenerationProvider provider
    ) {
        this.worker = worker;
        this.operations = operations;
        this.provider = provider;
    }

    @Override
    public void load() {
        foreground = getActivity() != null && !getActivity().isFinishing() && getActivity().hasWindowFocus();
    }

    @PluginMethod
    public void status(PluginCall call) {
        final LocalAiOperationRegistry.Operation operation;
        try {
            operation = begin(call, null, LocalAiOperationRegistry.Kind.STATUS);
        } catch (LocalAiException error) {
            reject(call, error);
            return;
        }
        execute(operation, call, () -> {
            LocalAiStatus status = foreground
                ? provider().status(operation)
                : LocalAiStatus.temporarilyUnavailable("foreground", null);
            resolve(operation, call, status.toJSObject());
        });
    }

    @PluginMethod
    public void prepare(PluginCall call) {
        if (!Boolean.TRUE.equals(call.getBoolean("userInitiated"))) {
            reject(call, new LocalAiException("invalid_request", false));
            return;
        }
        final LocalAiOperationRegistry.Operation operation;
        try {
            operation = begin(call, null, LocalAiOperationRegistry.Kind.PREPARE);
        } catch (LocalAiException error) {
            reject(call, error);
            return;
        }
        execute(operation, call, () -> {
            requireForeground();
            provider().prepare(operation);
            resolve(operation, call, null);
        });
    }

    @PluginMethod
    public void generate(PluginCall call) {
        final String requestId;
        final String input;
        final String instructions;
        final int maximumOutputTokens;
        final LocalAiOperationRegistry.Operation operation;
        try {
            requestId = LocalAiRequestValidator.requestId(call.getString("requestId"));
            input = LocalAiRequestValidator.input(call.getString("input"));
            instructions = LocalAiRequestValidator.instructions(call.getString("instructions"));
            maximumOutputTokens = LocalAiRequestValidator.maximumOutputTokens(call.getInt("maximumOutputTokens"));
            operation = begin(call, requestId, LocalAiOperationRegistry.Kind.GENERATE);
        } catch (LocalAiException error) {
            reject(call, error);
            return;
        }
        execute(operation, call, () -> {
            requireForeground();
            String text = provider().generate(operation, input, instructions, maximumOutputTokens);
            JSObject result = new JSObject();
            result.put("requestId", requestId);
            result.put("text", text);
            resolve(operation, call, result);
        });
    }

    @PluginMethod
    public void cancel(PluginCall call) {
        final String operationId;
        try {
            operationId = LocalAiRequestValidator.operationId(call.getString("operationId"));
        } catch (LocalAiException error) {
            reject(call, error);
            return;
        }
        LocalAiOperationRegistry.Operation cancelled = operations.cancel(operationId);
        if (cancelled != null) cancelled.deliverCancellation();
        call.resolve();
    }

    @PluginMethod
    public void invalidate(PluginCall call) {
        cancelAndDeliver(operations.invalidate());
        call.resolve();
    }

    private LocalAiOperationRegistry.Operation begin(
        PluginCall call,
        String requestId,
        LocalAiOperationRegistry.Kind kind
    ) throws LocalAiException {
        if (destroyed) throw new LocalAiException("not_ready", true);
        String operationId = LocalAiRequestValidator.operationId(call.getString("operationId"));
        return operations.begin(operationId, requestId, kind, () -> reject(call, LocalAiException.cancelled()));
    }

    private void execute(
        LocalAiOperationRegistry.Operation operation,
        PluginCall call,
        ThrowingRunnable action
    ) {
        try {
            worker.execute(() -> {
                try {
                    action.run();
                } catch (Throwable error) {
                    rejectCurrent(operation, call, error);
                }
            });
        } catch (RejectedExecutionException error) {
            rejectCurrent(operation, call, error);
        }
    }

    private void resolve(LocalAiOperationRegistry.Operation operation, PluginCall call, JSObject result) {
        if (!operations.claimCompletion(operation)) return;
        if (result == null) call.resolve(); else call.resolve(result);
    }

    private void rejectCurrent(LocalAiOperationRegistry.Operation operation, PluginCall call, Throwable error) {
        if (!operations.claimCompletion(operation)) return;
        reject(call, error instanceof LocalAiException
            ? (LocalAiException) error
            : MlKitErrorMapper.map(error));
    }

    private LocalTextGenerationProvider provider() throws LocalAiException {
        synchronized (providerLock) {
            if (destroyed) throw new LocalAiException("not_ready", true);
            if (provider == null) {
                try {
                    provider = new MlKitPromptProvider();
                } catch (Throwable error) {
                    throw MlKitErrorMapper.map(error);
                }
            }
            return provider;
        }
    }

    private void requireForeground() throws LocalAiException {
        if (!foreground) throw new LocalAiException("foreground_required", true);
    }

    private static void reject(PluginCall call, LocalAiException error) {
        JSObject data = new JSObject();
        data.put("recoverable", error.recoverable());
        if (error.retryAfterMs() != null) data.put("retryAfterMs", error.retryAfterMs());
        call.reject(LocalAiException.publicMessage(error.code()), error.code(), data);
    }

    private static void cancelAndDeliver(List<LocalAiOperationRegistry.Operation> cancelled) {
        for (LocalAiOperationRegistry.Operation operation : cancelled) operation.deliverCancellation();
    }

    @Override
    protected void handleOnResume() {
        foreground = true;
    }

    @Override
    protected void handleOnPause() {
        foreground = false;
        cancelAndDeliver(operations.invalidate());
    }

    @Override
    protected void handleOnDestroy() {
        destroyed = true;
        foreground = false;
        cancelAndDeliver(operations.destroy());
        try {
            synchronized (providerLock) {
                if (provider != null) {
                    provider.close();
                    provider = null;
                }
            }
        } catch (RuntimeException ignored) {
            // Teardown must still terminate the owned executor without exposing provider details.
        } finally {
            worker.shutdownNow();
        }
    }

    @FunctionalInterface
    private interface ThrowingRunnable {
        void run() throws Exception;
    }
}
