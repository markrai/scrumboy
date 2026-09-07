package com.markrai.scrumboy.speech;

import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;
import android.speech.RecognitionListener;
import android.speech.RecognizerIntent;
import android.speech.SpeechRecognizer;
import java.util.ArrayList;

final class PlatformOnDeviceSpeechInputProvider {
    interface Listener {
        void onListening();

        void onTranscript(String transcript);

        void onError(SpeechInputException error);
    }

    boolean isAvailable(Context context) {
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
            && SpeechRecognizer.isOnDeviceRecognitionAvailable(context);
    }

    /**
     * Phase one: create the on-device recognizer and install its listener on Android Main.
     * Never calls {@code startListening}, so no callback can arrive before the caller has
     * published the handle and attached cancellation to it. Phase two is
     * {@link PlatformRecognitionHandle#start()}.
     */
    PlatformRecognitionHandle prepare(
        Context context,
        String language,
        Listener listener
    ) throws SpeechInputException {
        if (!isAvailable(context)) throw new SpeechInputException("unsupported", false);
        final SpeechRecognizer recognizer;
        try {
            recognizer = SpeechRecognizer.createOnDeviceSpeechRecognizer(context);
        } catch (RuntimeException error) {
            throw new SpeechInputException("unsupported", false);
        }

        recognizer.setRecognitionListener(new RecognitionListener() {
            @Override public void onReadyForSpeech(Bundle params) {
                listener.onListening();
            }

            @Override public void onBeginningOfSpeech() {}
            @Override public void onRmsChanged(float rmsdB) {}
            @Override public void onBufferReceived(byte[] buffer) {}
            @Override public void onEndOfSpeech() {}
            @Override public void onPartialResults(Bundle partialResults) {}
            @Override public void onEvent(int eventType, Bundle params) {}

            @Override
            public void onError(int error) {
                listener.onError(SpeechInputErrorMapper.map(error));
            }

            @Override
            public void onResults(Bundle results) {
                ArrayList<String> matches = results == null
                    ? null
                    : results.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION);
                String transcript = firstTranscript(matches);
                if (transcript == null) {
                    listener.onError(new SpeechInputException("no_speech", true));
                    return;
                }
                listener.onTranscript(transcript);
            }
        });

        final Intent intent = new Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH);
        intent.putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM);
        intent.putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, false);
        intent.putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1);
        if (language != null) intent.putExtra(RecognizerIntent.EXTRA_LANGUAGE, language);

        return new PlatformRecognitionHandle(new PlatformRecognitionHandle.Session() {
            @Override
            public void startListening() throws SpeechInputException {
                try {
                    recognizer.startListening(intent);
                } catch (RuntimeException error) {
                    throw new SpeechInputException("recognition_failed", true);
                }
            }

            @Override
            public void cancelRecognition() {
                try {
                    recognizer.cancel();
                } catch (RuntimeException ignored) {
                    // Cancellation is best effort; destroy still runs.
                }
            }

            @Override
            public void destroyRecognizer() {
                recognizer.destroy();
            }
        });
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
}
