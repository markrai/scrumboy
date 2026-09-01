package com.markrai.scrumboy.speech;

import android.os.Build;
import android.speech.tts.TextToSpeech;
import android.speech.tts.UtteranceProgressListener;
import android.speech.tts.Voice;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.util.ArrayList;
import java.util.Set;

@CapacitorPlugin(name = "ScrumboySpeechOutput")
public class ScrumboySpeechOutputPlugin extends Plugin {
    private enum InitializationState { INITIALIZING, READY, NO_LOCAL_VOICE, ERROR, DESTROYED }

    private final SpeechOutputOperationRegistry operations = new SpeechOutputOperationRegistry();
    private volatile InitializationState initializationState = InitializationState.INITIALIZING;
    private volatile boolean foreground;
    private TextToSpeech textToSpeech;

    @Override
    public void load() {
        foreground = getActivity() != null && !getActivity().isFinishing();
        getActivity().runOnUiThread(() -> {
            if (initializationState == InitializationState.DESTROYED) return;
            textToSpeech = new TextToSpeech(getContext(), status -> initializeEngine(status));
        });
    }

    private void initializeEngine(int status) {
        TextToSpeech engine = textToSpeech;
        if (initializationState == InitializationState.DESTROYED) {
            if (engine != null) engine.shutdown();
            return;
        }
        if (status != TextToSpeech.SUCCESS || engine == null) {
            initializationState = InitializationState.ERROR;
            return;
        }
        engine.setOnUtteranceProgressListener(new UtteranceProgressListener() {
            @Override public void onStart(String utteranceId) {}

            @Override
            public void onDone(String utteranceId) {
                SpeechOutputOperationRegistry.Operation operation = operations.claim(utteranceId);
                if (operation == null) return;
                PluginCall call = takeDelivery(operation);
                if (call == null) return;
                JSObject result = new JSObject();
                result.put("operationId", utteranceId);
                call.resolve(result);
            }

            @Override
            public void onError(String utteranceId) {
                rejectOwned(utteranceId, new SpeechOutputException("synthesis_failed", true));
            }

            @Override
            public void onError(String utteranceId, int errorCode) {
                rejectOwned(utteranceId, new SpeechOutputException("synthesis_failed", true));
            }

            @Override
            public void onStop(String utteranceId, boolean interrupted) {
                rejectOwned(utteranceId, new SpeechOutputException("cancelled", true));
            }
        });
        Voice local = selectLocalVoice(engine, "en-US");
        if (local == null || engine.setVoice(local) == TextToSpeech.ERROR) {
            initializationState = InitializationState.NO_LOCAL_VOICE;
            return;
        }
        initializationState = InitializationState.READY;
    }

    @PluginMethod
    public void status(PluginCall call) {
        getActivity().runOnUiThread(() -> {
            JSObject result = new JSObject();
            switch (initializationState) {
                case INITIALIZING:
                    result.put("state", "not-ready");
                    result.put("reason", "initializing");
                    break;
                case READY:
                    if (!foreground) {
                        result.put("state", "temporarily-unavailable");
                        result.put("reason", "foreground");
                    } else if (operations.activeCount() > 0) {
                        result.put("state", "temporarily-unavailable");
                        result.put("reason", "busy");
                    } else {
                        result.put("state", "ready");
                    }
                    break;
                case NO_LOCAL_VOICE:
                    result.put("state", "unsupported");
                    result.put("reason", "no-local-voice");
                    break;
                case DESTROYED:
                case ERROR:
                default:
                    result.put("state", "unsupported");
                    result.put("reason", "provider");
                    break;
            }
            call.resolve(result);
        });
    }

    @PluginMethod
    public void speak(PluginCall call) {
        String operationId = call.getString("operationId");
        String text = call.getString("text");
        String language = call.getString("language", "en-US");
        try {
            SpeechOutputRequestValidator.validate(text, language);
        } catch (SpeechOutputException error) {
            reject(call, error);
            return;
        }

        final SpeechOutputOperationRegistry.Operation operation;
        try {
            operation = operations.begin(operationId, call.getCallbackId());
        } catch (SpeechOutputException error) {
            reject(call, error);
            return;
        }
        bridge.saveCall(call);
        getActivity().runOnUiThread(() -> {
            try {
                requireReady();
                TextToSpeech engine = textToSpeech;
                Voice local = selectLocalVoice(engine, language);
                if (local == null || engine.setVoice(local) == TextToSpeech.ERROR) {
                    throw new SpeechOutputException("no_local_voice", false);
                }
                if (!operations.isActive(operation)) return;
                int result = engine.speak(text.trim(), TextToSpeech.QUEUE_FLUSH, null, operationId);
                if (result != TextToSpeech.SUCCESS) {
                    throw new SpeechOutputException("synthesis_failed", true);
                }
            } catch (SpeechOutputException error) {
                rejectOwned(operationId, error);
            } catch (RuntimeException error) {
                rejectOwned(operationId, new SpeechOutputException("synthesis_failed", true));
            }
        });
    }

    @PluginMethod
    public void stop(PluginCall call) {
        String operationId = call.getString("operationId");
        getActivity().runOnUiThread(() -> {
            SpeechOutputOperationRegistry.Operation cancelled = operations.cancel(operationId);
            if (cancelled != null) {
                if (textToSpeech != null) textToSpeech.stop();
                PluginCall pending = takeDelivery(cancelled);
                if (pending != null) reject(pending, new SpeechOutputException("cancelled", true));
            }
            call.resolve();
        });
    }

    @PluginMethod
    public void invalidate(PluginCall call) {
        getActivity().runOnUiThread(() -> {
            cancelActive();
            call.resolve();
        });
    }

    private void requireReady() throws SpeechOutputException {
        if (initializationState == InitializationState.NO_LOCAL_VOICE) {
            throw new SpeechOutputException("no_local_voice", false);
        }
        if (initializationState != InitializationState.READY || textToSpeech == null) {
            throw new SpeechOutputException("not_ready", true);
        }
        if (!foreground) throw new SpeechOutputException("foreground_required", true);
    }

    private Voice selectLocalVoice(TextToSpeech engine, String language) {
        if (engine == null || Build.VERSION.SDK_INT < Build.VERSION_CODES.LOLLIPOP) return null;
        Set<Voice> voices = engine.getVoices();
        if (voices == null) return null;
        ArrayList<SpeechOutputVoicePolicy.Candidate<Voice>> candidates = new ArrayList<>();
        for (Voice voice : voices) {
            if (voice == null) continue;
            candidates.add(new SpeechOutputVoicePolicy.Candidate<>(
                voice,
                voice.getLocale() == null ? "" : voice.getLocale().toLanguageTag(),
                voice.isNetworkConnectionRequired()
            ));
        }
        return SpeechOutputVoicePolicy.selectLocal(candidates, language);
    }

    private void rejectOwned(String operationId, SpeechOutputException error) {
        SpeechOutputOperationRegistry.Operation operation = operations.claim(operationId);
        if (operation == null) return;
        PluginCall call = takeDelivery(operation);
        if (call != null) reject(call, error);
    }

    private void cancelActive() {
        SpeechOutputOperationRegistry.Operation cancelled = operations.invalidate();
        if (textToSpeech != null) textToSpeech.stop();
        if (cancelled == null) return;
        PluginCall call = takeDelivery(cancelled);
        if (call != null) reject(call, new SpeechOutputException("cancelled", true));
    }

    private PluginCall takeDelivery(SpeechOutputOperationRegistry.Operation operation) {
        PluginCall call = bridge.getSavedCall(operation.deliveryId);
        if (call != null) bridge.releaseCall(call);
        return call;
    }

    private static void reject(PluginCall call, SpeechOutputException error) {
        JSObject data = new JSObject();
        data.put("recoverable", error.recoverable());
        call.reject(SpeechOutputException.publicMessage(error.code()), error.code(), data);
    }

    @Override
    protected void handleOnResume() {
        foreground = true;
    }

    @Override
    protected void handleOnStop() {
        foreground = false;
        cancelActive();
    }

    @Override
    protected void handleOnDestroy() {
        foreground = false;
        initializationState = InitializationState.DESTROYED;
        cancelActive();
        TextToSpeech engine = textToSpeech;
        textToSpeech = null;
        if (engine != null) engine.shutdown();
    }
}
