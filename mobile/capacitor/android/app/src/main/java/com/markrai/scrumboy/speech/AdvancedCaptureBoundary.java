package com.markrai.scrumboy.speech;

/**
 * Alpha1 {@code SpeechRecognizer.startRecognition} starts the session when the
 * returned Flow is collected. The SDK does not expose a trustworthy
 * "microphone acquired" callback.
 *
 * Conservative boundary used by this phase: once {@code startRecognition} has been
 * invoked with {@code AudioSource.fromMic()}, capture may have begun and same-turn
 * platform fallback is forbidden. It is safer to reject and let the next Listen use
 * fallback than to risk overlapping microphone ownership.
 */
final class AdvancedCaptureBoundary {
    private AdvancedCaptureBoundary() {}

    static boolean allowSameTurnPlatformFallback(boolean captureHandedToSdk) {
        return !captureHandedToSdk;
    }
}
