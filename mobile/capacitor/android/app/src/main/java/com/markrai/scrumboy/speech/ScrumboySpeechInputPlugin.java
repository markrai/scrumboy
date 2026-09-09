package com.markrai.scrumboy.speech;

import android.app.Activity;
import android.content.pm.ApplicationInfo;
import android.Manifest;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;
import com.google.mlkit.genai.common.GenAiException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;

@CapacitorPlugin(
    name = "ScrumboySpeechInput",
    permissions = {
        @Permission(alias = ScrumboySpeechInputPlugin.MICROPHONE_PERMISSION, strings = { Manifest.permission.RECORD_AUDIO })
    }
)
public class ScrumboySpeechInputPlugin extends Plugin {
    static final String MICROPHONE_PERMISSION = "microphone";
    private static final String LISTENING_EVENT = "listening";
    private static final String CAPABILITY_EVENT = "asrCapability";
    // Request validation bound. The shell owns the deadline and exact cancellation;
    // product controllers independently choose 10s (agent) or 45s (Create v2).
    private static final int MAX_DURATION_MS = 45_000;

    private final SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
    /**
     * SpeechRecognizer is Android-Main-only, and Capacitor runs @PluginMethod bodies on its
     * own CapacitorPlugins handler thread. This reaches Main without depending on an
     * Activity, so native release stays correct exactly when the Activity is going away.
     */
    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private final PlatformOnDeviceSpeechInputProvider platformProvider = new PlatformOnDeviceSpeechInputProvider();
    private final ExecutorService capabilityExecutor = Executors.newSingleThreadExecutor(r -> {
        Thread thread = new Thread(r, "scrumboy-advanced-speech");
        thread.setDaemon(true);
        return thread;
    });

    private final AdvancedTeardownBarrier advancedTeardownBarrier = new AdvancedTeardownBarrier();
    private AdvancedSpeechProviderManager advancedManager;
    private MlKitAdvancedSpeechRuntime advancedRuntime;
    private volatile boolean foreground;
    private volatile boolean destroyed;

    @Override
    public void load() {
        SpeechInputDiagnostics.configure(getContext());
        foreground = getActivity() != null && !getActivity().isFinishing();
        advancedRuntime = new MlKitAdvancedSpeechRuntime();
        advancedManager = new AdvancedSpeechProviderManager(
            new AdvancedSpeechCapabilityStore(new SharedPreferencesAdvancedSpeechStore(getContext())),
            advancedRuntime,
            System::currentTimeMillis
        );
    }

    @PluginMethod
    public void status(PluginCall call) {
        final String language = call.getString("language");
        boolean submitted = submitCapabilityWork(() -> {
            AdvancedSpeechDecision decision;
            try {
                decision = advancedManager.resolve(language);
            } catch (RuntimeException error) {
                decision = null;
            }
            final AdvancedSpeechDecision resolved = decision;
            dispatchToActivity(call, null, () -> {
                if (resolved != null) emitCapability(resolved);
                JSObject status = new JSObject();
                if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
                    status.put("state", "unsupported");
                    status.put("reason", "os");
                } else if (!speechReady(resolved)) {
                    status.put("state", "unsupported");
                    status.put("reason", "provider");
                } else if (!foreground) {
                    status.put("state", "temporarily-unavailable");
                    status.put("reason", "foreground");
                } else if (operations.activeCount() > 0) {
                    status.put("state", "temporarily-unavailable");
                    status.put("reason", "busy");
                } else {
                    status.put("state", "ready");
                }
                call.resolve(status);
            });
        });
        if (!submitted) reject(call, new SpeechInputException("not_ready", true));
    }

    @PluginMethod
    public void listen(PluginCall call) {
        final String operationId = call.getString("operationId");
        final Integer maxDurationMs = call.getInt("maxDurationMs");
        final boolean aggregateSegments = "create_v2".equals(call.getString("aggregationMode"));
        final Integer requestedGraceMs = call.getInt("postFinalGraceMs");
        final int postFinalGraceMs = requestedGraceMs == null ? 4_000 : requestedGraceMs;
        final String language = call.getString("language");
        if (
            maxDurationMs == null
            || maxDurationMs < 1
            || maxDurationMs > MAX_DURATION_MS
            || postFinalGraceMs < 1
            || postFinalGraceMs > 10_000
            || (language != null && !validLanguage(language))
        ) {
            reject(call, new SpeechInputException("invalid_request", false));
            return;
        }

        final SpeechInputOperationRegistry.Operation operation;
        try {
            operation = operations.begin(operationId, () -> reject(call, new SpeechInputException("cancelled", true)));
        } catch (SpeechInputException error) {
            reject(call, error);
            return;
        }

        boolean submitted = submitCapabilityWork(() -> {
            AdvancedSpeechDecision decision;
            try {
                decision = advancedManager.resolve(language);
            } catch (RuntimeException error) {
                decision = new AdvancedSpeechDecision(
                    SpeechInputProviderId.ANDROID_ON_DEVICE,
                    AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
                    AdvancedSpeechProviderManager.normalizeLocaleTag(language),
                    "miss",
                    "probed",
                    true
                );
            }
            final AdvancedSpeechDecision resolved = decision;
            dispatchToActivity(call, operation, () -> {
                try {
                    if (resolved != null) emitCapability(resolved);
                    requireAvailable(resolved);
                    if (getPermissionState(MICROPHONE_PERMISSION) != PermissionState.GRANTED) {
                        // Capacitor already preserves this PluginCall for the permission callback.
                        // Do not setKeepAlive(true): that would leak the call after resolve/reject.
                        call.getData().put("providerFrozen", AdvancedSpeechUtteranceFreeze.providerToken(resolved));
                        call.getData().put("localeTag", resolved.localeTag);
                        requestPermissionForAlias(MICROPHONE_PERMISSION, call, "microphonePermissionCallback");
                        return;
                    }
                    startRecognition(operation, call, language, resolved);
                } catch (SpeechInputException error) {
                    rejectCurrent(operation, call, error);
                }
            });
        });
        if (!submitted) rejectCurrent(operation, call, new SpeechInputException("not_ready", true));
    }

    @PermissionCallback
    private void microphonePermissionCallback(PluginCall call) {
        String operationId = call.getString("operationId");
        PermissionState permission = getPermissionState(MICROPHONE_PERMISSION);
        if (permission != PermissionState.GRANTED) {
            SpeechInputOperationRegistry.Operation cancelled = operations.cancel(operationId);
            if (cancelled == null) return;
            reject(call, new SpeechInputException(
                permission == PermissionState.DENIED
                    ? "permission_denied_permanently"
                    : "permission_denied",
                permission != PermissionState.DENIED
            ));
            return;
        }
        dispatchToActivity(call, operations.active(operationId), () -> {
            try {
                String language = call.getString("language");
                AdvancedSpeechDecision decision = AdvancedSpeechUtteranceFreeze.thaw(
                    call.getData().optString("providerFrozen", "platform"),
                    call.getData().optString("localeTag", ""),
                    language
                );
                requireAvailable(decision);
                SpeechInputOperationRegistry.Operation operation = operations.active(operationId);
                if (operation == null) return;
                startRecognition(operation, call, language, decision);
            } catch (SpeechInputException error) {
                SpeechInputOperationRegistry.Operation cancelled = operations.cancel(operationId);
                if (cancelled != null) reject(call, error);
            }
        });
    }

    private void startRecognition(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        String language,
        AdvancedSpeechDecision decision
    ) throws SpeechInputException {
        requireAvailable(decision);
        if (!operations.isActive(operation)) return;

        // Provider choice is frozen for this utterance.
        if (decision.useAdvanced()) {
            boolean aggregateSegments = "create_v2".equals(call.getString("aggregationMode"));
            Integer requestedGraceMs = call.getInt("postFinalGraceMs");
            int postFinalGraceMs = requestedGraceMs == null ? 4_000 : requestedGraceMs;
            startAdvancedRecognition(
                operation,
                call,
                language,
                decision,
                aggregateSegments,
                postFinalGraceMs
            );
        } else {
            startPlatformAfterAdvancedTeardown(operation, call, language, decision);
        }
    }

    /**
     * Creates the on-device SpeechRecognizer. Must run on Android Main, and only
     * after {@link #startPlatformAfterAdvancedTeardown} has awaited the Advanced barrier.
     *
     * Ownership order: prepare (create + listener, no capture), publish the exact handle,
     * attach cancellation to that handle, recheck the operation, then startListening.
     */
    private void startPlatformRecognition(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        String language,
        AdvancedSpeechDecision decision
    ) throws SpeechInputException {
        final PlatformRecognitionHandle[] handleSlot = new PlatformRecognitionHandle[1];
        PlatformRecognitionHandle prepared = platformProvider.prepare(
            getContext(),
            language,
            new PlatformOnDeviceSpeechInputProvider.Listener() {
                @Override
                public void onListening() {
                    if (!operations.isActive(operation)) return;
                    notifyListening(call.getString("operationId"), decision.providerId);
                }

                @Override
                public void onTranscript(String transcript) {
                    if (!operations.claimCompletion(operation)) return;
                    PlatformRecognitionHandle handle = handleSlot[0];
                    if (handle != null) handle.destroy();
                    JSObject result = new JSObject();
                    result.put("transcript", transcript);
                    call.resolve(result);
                }

                @Override
                public void onError(SpeechInputException error) {
                    rejectCurrent(operation, call, error, handleSlot[0]);
                }
            }
        );

        handleSlot[0] = prepared;
        operation.attachNativeCancellation(() -> cancelPlatformHandle(prepared));
        if (!operations.isActive(operation)) {
            prepared.cancel();
            return;
        }
        prepared.start();
    }

    /**
     * Cancellation can arrive on Capacitor's plugin handler thread. Marking the handle
     * cancelled is what wins the race against {@link PlatformRecognitionHandle#start()};
     * the native cancel/destroy is always posted to Android Main, never run inline, so it
     * cannot land in front of a startListening that already won.
     */
    private void cancelPlatformHandle(PlatformRecognitionHandle handle) {
        if (!handle.requestCancel()) return;
        mainHandler.post(handle::cancel);
    }

    private void startAdvancedRecognition(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        String language,
        AdvancedSpeechDecision decision,
        boolean aggregateSegments,
        int postFinalGraceMs
    ) {
        AdvancedUtteranceLifecycle lifecycle = new AdvancedUtteranceLifecycle();

        final MlKitAdvancedSpeechRuntime.RecognitionHandle[] handleSlot =
            new MlKitAdvancedSpeechRuntime.RecognitionHandle[1];

        final MlKitAdvancedSpeechRuntime.RecognitionHandle prepared;
        try {
            prepared = advancedRuntime.prepareRecognition(
                decision.localeTag,
                new MlKitAdvancedSpeechRuntime.RecognitionCallbacks() {
                    @Override
                    public void onListening() {
                        if (!operations.isActive(operation)) return;
                        notifyListening(call.getString("operationId"), decision.providerId);
                    }

                    @Override
                    public void onPartial(String text) {
                        if (!operations.isActive(operation)) return;
                        SpeechInputDiagnostics.emit(
                            "response",
                            "provider", decision.providerId.diagnosticName(),
                            "response", "partial"
                        );
                        lifecycle.onPartial(text);
                    }

                    /**
                     * FinalTextResponse is the authoritative transcript for this utterance and
                     * is the product terminal. CompletedResponse marks the recognition stream
                     * ending and, on a continuous mic source, never arrives on its own, so
                     * waiting for it would hang until the shell's timeout cancelled the session.
                     */
                    @Override
                    public void onFinal(String text) {
                        AdvancedUtteranceLifecycle.Outcome outcome = lifecycle.onFinal(
                            text,
                            operations,
                            operation,
                            () -> stopAdvancedHandle(handleSlot),
                            aggregateSegments,
                            postFinalGraceMs,
                            mainHandler,
                            () -> {
                                SpeechInputDiagnostics.emit(
                                    "turn-final",
                                    "provider", decision.providerId.diagnosticName(),
                                    "segments", lifecycle.segmentCount(),
                                    "length", lifecycle.transcript().length(),
                                    "completion", "post_final_grace"
                                );
                                JSObject result = new JSObject();
                                result.put("transcript", lifecycle.transcript());
                                result.put("segmentCount", lifecycle.segmentCount());
                                call.resolve(result);
                            }
                        );
                        SpeechInputDiagnostics.emit(
                            "response",
                            "provider", decision.providerId.diagnosticName(),
                            "response", "final",
                            "productTerminal", outcome == AdvancedUtteranceLifecycle.Outcome.RESOLVE_TRANSCRIPT,
                            "segmentFinal", aggregateSegments && outcome == AdvancedUtteranceLifecycle.Outcome.SEGMENT_FINAL
                        );
                        if (aggregateSegments && outcome == AdvancedUtteranceLifecycle.Outcome.SEGMENT_FINAL) {
                            SpeechInputDiagnostics.emit(
                                "segment-final",
                                "provider", decision.providerId.diagnosticName(),
                                "segment", lifecycle.segmentCount(),
                                "length", text == null ? 0 : text.trim().length()
                            );
                            return;
                        }
                        if (outcome != AdvancedUtteranceLifecycle.Outcome.RESOLVE_TRANSCRIPT) return;
                        SpeechInputDiagnostics.emit(
                            "product-terminal",
                            "provider", decision.providerId.diagnosticName(),
                            "product-terminal", "final",
                            "length", lifecycle.transcript().length()
                        );
                        JSObject result = new JSObject();
                        result.put("transcript", lifecycle.transcript());
                        if (aggregateSegments) result.put("segmentCount", lifecycle.segmentCount());
                        call.resolve(result);
                    }

                    /**
                     * Stream completion. Harmless and idempotent after a final already claimed
                     * the product terminal; only a stream that ended without any valid final
                     * settles here, and it settles as no_speech exactly as before.
                     */
                    @Override
                    public void onCompleted() {
                        AdvancedUtteranceLifecycle.Outcome outcome =
                            lifecycle.onCompleted(operations, operation, () -> {});
                        SpeechInputDiagnostics.emit(
                            "response",
                            "provider", decision.providerId.diagnosticName(),
                            "response", "completed",
                            "productTerminal", outcome != AdvancedUtteranceLifecycle.Outcome.IGNORE
                        );
                        if (outcome == AdvancedUtteranceLifecycle.Outcome.IGNORE) return;
                        if (outcome == AdvancedUtteranceLifecycle.Outcome.REJECT_NO_SPEECH) {
                            reject(call, new SpeechInputException("no_speech", true));
                            return;
                        }
                        JSObject result = new JSObject();
                        result.put("transcript", lifecycle.transcript());
                        if (aggregateSegments) {
                            result.put("segmentCount", lifecycle.segmentCount());
                            SpeechInputDiagnostics.emit("turn-final", "provider", decision.providerId.diagnosticName(), "segments", lifecycle.segmentCount(), "length", lifecycle.transcript().length(), "completion", "sdk_completed");
                        }
                        call.resolve(result);
                    }

                    @Override
                    public void onFailure(GenAiException genAi, Throwable error, boolean captureHandedToSdk) {
                        if (AdvancedSpeechErrorMapper.isCapabilityLevel(genAi != null ? genAi : error)) {
                            advancedManager.demoteForCapabilityFailure(decision.localeTag);
                        }
                        SpeechInputDiagnostics.emit(
                            "response",
                            "provider", decision.providerId.diagnosticName(),
                            "response", "error",
                            "captureHandedToSdk", captureHandedToSdk
                        );
                        if (lifecycle.claimSameTurnPlatformFallback(captureHandedToSdk, operations, operation)) {
                            stopAdvancedHandle(handleSlot);
                            startPlatformAfterAdvancedTeardown(
                                operation,
                                call,
                                language,
                                platformFallbackDecision(decision)
                            );
                            return;
                        }
                        if (!lifecycle.claimErrorTerminal(
                            operations,
                            operation,
                            () -> stopAdvancedHandle(handleSlot)
                        )) {
                            return;
                        }
                        reject(call, AdvancedSpeechErrorMapper.map(genAi != null ? genAi : error));
                    }
                }
            );
        } catch (RuntimeException error) {
            if (AdvancedSpeechErrorMapper.isCapabilityLevel(error)) {
                advancedManager.demoteForCapabilityFailure(decision.localeTag);
            }
            // No recognizer was created, so capture was never handed to the SDK.
            if (!lifecycle.claimSameTurnPlatformFallback(false, operations, operation)) return;
            startPlatformAfterAdvancedTeardown(
                operation,
                call,
                language != null ? language : decision.localeTag,
                platformFallbackDecision(decision)
            );
            return;
        }
        AdvancedRecognitionOwnership.publishAttachRegisterThenStartIfOwned(
            handleSlot,
            prepared,
            operations,
            operation,
            advancedTeardownBarrier
        );
    }

    /** Advanced native teardown for the exact published handle; idempotent. */
    private static void stopAdvancedHandle(MlKitAdvancedSpeechRuntime.RecognitionHandle[] handleSlot) {
        MlKitAdvancedSpeechRuntime.RecognitionHandle handle = handleSlot[0];
        if (handle != null) handle.cancel();
    }

    /**
     * The only platform SpeechRecognizer acquisition entry. Awaits outstanding
     * Advanced native teardown off Main, then creates the on-device recognizer on Main.
     */
    private void startPlatformAfterAdvancedTeardown(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        String language,
        AdvancedSpeechDecision decision
    ) {
        try {
            AdvancedRecognitionOwnership.awaitBarrierThenRunOnMain(
                advancedTeardownBarrier.currentBarrier(),
                capabilityExecutor,
                () -> operations.isActive(operation) && !destroyed,
                action -> dispatchToActivity(call, operation, action),
                () -> {
                    try {
                        startPlatformRecognition(
                            operation,
                            call,
                            language != null ? language : decision.localeTag,
                            decision
                        );
                    } catch (SpeechInputException fallbackError) {
                        rejectCurrent(operation, call, fallbackError);
                    }
                }
            );
        } catch (RejectedExecutionException shuttingDown) {
            rejectCurrent(operation, call, new SpeechInputException("not_ready", true));
        }
    }

    /**
     * The capability executor is shut down in handleOnDestroy, and Capacitor rethrows
     * anything a plugin method leaks. Submission must report rejection rather than crash
     * the plugin handler thread or Main.
     */
    private boolean submitCapabilityWork(Runnable work) {
        try {
            capabilityExecutor.execute(work);
            return true;
        } catch (RejectedExecutionException shuttingDown) {
            return false;
        }
    }

    private static AdvancedSpeechDecision platformFallbackDecision(AdvancedSpeechDecision decision) {
        return new AdvancedSpeechDecision(
            SpeechInputProviderId.ANDROID_ON_DEVICE,
            AdvancedSpeechCacheState.UNAVAILABLE_CACHED,
            decision.localeTag,
            "hit",
            "cached",
            false
        );
    }

    @PluginMethod
    public void cancel(PluginCall call) {
        SpeechInputOperationRegistry.Operation cancelled = operations.cancel(call.getString("operationId"));
        if (cancelled != null) cancelled.deliverCancellation();
        call.resolve();
    }

    @PluginMethod
    public void invalidate(PluginCall call) {
        cancelAndDeliver();
        call.resolve();
    }

    @PluginMethod
    public void clearAdvancedCapabilityCache(PluginCall call) {
        boolean debuggable = (getContext().getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
        if (!debuggable) {
            reject(call, new SpeechInputException("unsupported", false));
            return;
        }
        if (operations.activeCount() > 0) {
            reject(call, new SpeechInputException("busy", true));
            return;
        }
        advancedManager.clearCache();
        call.resolve();
    }

    private boolean speechReady(AdvancedSpeechDecision decision) {
        if (decision != null && decision.state == AdvancedSpeechCacheState.SUPPORTED_READY) return true;
        return platformProvider.isAvailable(getContext());
    }

    private void requireAvailable(AdvancedSpeechDecision decision) throws SpeechInputException {
        if (destroyed) throw new SpeechInputException("not_ready", true);
        if (!foreground) throw new SpeechInputException("foreground_required", true);
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            throw new SpeechInputException("unsupported", false);
        }
        if (!speechReady(decision)) {
            throw new SpeechInputException("unsupported", false);
        }
    }

    private void dispatchToActivity(
        PluginCall call,
        SpeechInputOperationRegistry.Operation operation,
        Runnable action
    ) {
        Activity activity = getActivity();
        if (!SpeechInputActivityGuard.canDispatch(destroyed, activity)) {
            rejectDestroyed(call, operation);
            return;
        }
        activity.runOnUiThread(() -> {
            if (!SpeechInputActivityGuard.canDispatch(destroyed, getActivity())) {
                rejectDestroyed(call, operation);
                return;
            }
            action.run();
        });
    }

    private void rejectDestroyed(PluginCall call, SpeechInputOperationRegistry.Operation operation) {
        SpeechInputException error = new SpeechInputException("not_ready", true);
        if (operation != null) {
            rejectCurrent(operation, call, error);
            return;
        }
        reject(call, error);
    }

    private void emitCapability(AdvancedSpeechDecision decision) {
        JSObject event = new JSObject();
        event.put("cache", decision.cacheAccess);
        event.put("advancedSupport", decision.advancedSupportLabel());
        event.put("statusSource", decision.statusSource);
        event.put("locale", decision.localeTag);
        event.put("provider", decision.providerId.diagnosticName());
        notifyListeners(CAPABILITY_EVENT, event);
    }

    private void notifyListening(String operationId, SpeechInputProviderId providerId) {
        SpeechInputDiagnostics.emit("listening", "provider", providerId.diagnosticName());
        JSObject event = new JSObject();
        event.put("operationId", operationId);
        event.put("provider", providerId.diagnosticName());
        notifyListeners(LISTENING_EVENT, event);
    }

    private void rejectCurrent(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        SpeechInputException error
    ) {
        if (!operations.claimCompletion(operation)) return;
        reject(call, error);
    }

    private void rejectCurrent(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        SpeechInputException error,
        PlatformRecognitionHandle handle
    ) {
        if (!operations.claimCompletion(operation)) return;
        if (handle != null) handle.destroy();
        reject(call, error);
    }

    private static void reject(PluginCall call, SpeechInputException error) {
        JSObject data = new JSObject();
        data.put("recoverable", error.recoverable());
        if (error.providerCode() != null) data.put("providerCode", error.providerCode());
        if (error.providerReason() != null) data.put("providerReason", error.providerReason());
        call.reject(SpeechInputException.publicMessage(error.code()), error.code(), data);
    }

    private void cancelAndDeliver() {
        SpeechInputOperationRegistry.Operation cancelled = operations.invalidate();
        if (cancelled != null) cancelled.deliverCancellation();
    }

    private static boolean validLanguage(String language) {
        return language.length() > 0
            && language.length() <= 64
            && language.matches("^[A-Za-z0-9-]+$");
    }

    @Override
    protected void handleOnResume() {
        foreground = true;
    }

    @Override
    protected void handleOnStop() {
        foreground = false;
        cancelAndDeliver();
    }

    @Override
    protected void handleOnDestroy() {
        destroyed = true;
        foreground = false;
        cancelAndDeliver();
        if (advancedManager != null) advancedManager.close();
        capabilityExecutor.shutdownNow();
    }
}
