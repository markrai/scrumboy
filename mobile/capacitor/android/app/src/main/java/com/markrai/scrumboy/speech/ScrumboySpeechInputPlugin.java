package com.markrai.scrumboy.speech;

import android.Manifest;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;
import android.speech.RecognitionListener;
import android.speech.RecognizerIntent;
import android.speech.SpeechRecognizer;
import com.getcapacitor.JSObject;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;
import java.util.ArrayList;
import java.util.Locale;

@CapacitorPlugin(
    name = "ScrumboySpeechInput",
    permissions = {
        @Permission(alias = ScrumboySpeechInputPlugin.MICROPHONE_PERMISSION, strings = { Manifest.permission.RECORD_AUDIO })
    }
)
public class ScrumboySpeechInputPlugin extends Plugin {
    static final String MICROPHONE_PERMISSION = "microphone";
    private static final String LISTENING_EVENT = "listening";
    private static final int MAX_DURATION_MS = 10_000;

    private final SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
    private volatile boolean foreground;
    private volatile boolean destroyed;

    @Override
    public void load() {
        foreground = getActivity() != null && !getActivity().isFinishing();
    }

    @PluginMethod
    public void status(PluginCall call) {
        getActivity().runOnUiThread(() -> {
            if (destroyed) {
                reject(call, new SpeechInputException("not_ready", true));
                return;
            }
            JSObject status = new JSObject();
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
                status.put("state", "unsupported");
                status.put("reason", "os");
            } else if (!SpeechRecognizer.isOnDeviceRecognitionAvailable(getContext())) {
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
    }

    @PluginMethod
    public void listen(PluginCall call) {
        final String operationId = call.getString("operationId");
        final Integer maxDurationMs = call.getInt("maxDurationMs");
        final String language = call.getString("language");
        if (
            maxDurationMs == null
            || maxDurationMs < 1
            || maxDurationMs > MAX_DURATION_MS
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

        getActivity().runOnUiThread(() -> {
            try {
                requireAvailable();
                if (getPermissionState(MICROPHONE_PERMISSION) != PermissionState.GRANTED) {
                    requestPermissionForAlias(MICROPHONE_PERMISSION, call, "microphonePermissionCallback");
                    return;
                }
                startRecognition(operation, call, language);
            } catch (SpeechInputException error) {
                rejectCurrent(operation, call, error);
            }
        });
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
        getActivity().runOnUiThread(() -> {
            try {
                requireAvailable();
                // The permission request keeps the original operation active.
                startRecognitionForActive(call);
            } catch (SpeechInputException error) {
                SpeechInputOperationRegistry.Operation cancelled = operations.cancel(operationId);
                if (cancelled != null) reject(call, error);
            }
        });
    }

    private void startRecognitionForActive(PluginCall call) throws SpeechInputException {
        String operationId = call.getString("operationId");
        SpeechInputOperationRegistry.Operation operation = operations.active(operationId);
        if (operation == null) return;
        startRecognition(operation, call, call.getString("language"));
    }

    private void startRecognition(
        SpeechInputOperationRegistry.Operation operation,
        PluginCall call,
        String language
    ) throws SpeechInputException {
        requireAvailable();
        if (!operations.isActive(operation)) return;
        final SpeechRecognizer recognizer;
        try {
            recognizer = SpeechRecognizer.createOnDeviceSpeechRecognizer(getContext());
        } catch (RuntimeException error) {
            throw new SpeechInputException("unsupported", false);
        }

        operation.attachNativeCancellation(() -> getActivity().runOnUiThread(() -> {
            try {
                recognizer.cancel();
            } finally {
                recognizer.destroy();
            }
        }));
        if (!operations.isActive(operation)) return;

        try {
            recognizer.setRecognitionListener(new RecognitionListener() {
                @Override public void onReadyForSpeech(Bundle params) {
                    if (!operations.isActive(operation)) return;
                    JSObject event = new JSObject();
                    event.put("operationId", call.getString("operationId"));
                    notifyListeners(LISTENING_EVENT, event);
                }
                @Override public void onBeginningOfSpeech() {}
                @Override public void onRmsChanged(float rmsdB) {}
                @Override public void onBufferReceived(byte[] buffer) {}
                @Override public void onEndOfSpeech() {}
                @Override public void onPartialResults(Bundle partialResults) {}
                @Override public void onEvent(int eventType, Bundle params) {}

                @Override
                public void onError(int error) {
                    rejectCurrent(operation, call, SpeechInputErrorMapper.map(error), recognizer);
                }

                @Override
                public void onResults(Bundle results) {
                    ArrayList<String> matches = results == null
                        ? null
                        : results.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION);
                    String transcript = firstTranscript(matches);
                    if (transcript == null) {
                        rejectCurrent(operation, call, new SpeechInputException("no_speech", true), recognizer);
                        return;
                    }
                    if (!operations.claimCompletion(operation)) return;
                    recognizer.destroy();
                    JSObject result = new JSObject();
                    result.put("transcript", transcript);
                    call.resolve(result);
                }
            });

            Intent intent = new Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH);
            intent.putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM);
            intent.putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, false);
            intent.putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1);
            if (language != null) intent.putExtra(RecognizerIntent.EXTRA_LANGUAGE, language);
            recognizer.startListening(intent);
        } catch (RuntimeException error) {
            recognizer.destroy();
            throw new SpeechInputException("recognition_failed", true);
        }
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

    private void requireAvailable() throws SpeechInputException {
        if (destroyed) throw new SpeechInputException("not_ready", true);
        if (!foreground) throw new SpeechInputException("foreground_required", true);
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            throw new SpeechInputException("unsupported", false);
        }
        if (!SpeechRecognizer.isOnDeviceRecognitionAvailable(getContext())) {
            throw new SpeechInputException("unsupported", false);
        }
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
        SpeechRecognizer recognizer
    ) {
        if (!operations.claimCompletion(operation)) return;
        recognizer.destroy();
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

    private static String firstTranscript(ArrayList<String> matches) {
        if (matches == null) return null;
        for (String match : matches) {
            if (match == null) continue;
            String transcript = match.trim();
            if (!transcript.isEmpty() && transcript.length() <= 260) return transcript;
        }
        return null;
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
    }
}
