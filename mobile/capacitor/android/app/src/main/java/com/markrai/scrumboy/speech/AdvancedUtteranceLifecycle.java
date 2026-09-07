package com.markrai.scrumboy.speech;

/**
 * Maps ML Kit Advanced stream responses onto one Scrumboy VoiceFlow utterance.
 *
 * Verified against genai-speech-recognition 1.0.0-alpha1: {@code FinalTextResponse} is the
 * authoritative transcript for an utterance, while {@code CompletedResponse} is emitted by
 * the SDK's {@code Flow.onCompletion} block and only when the terminating cause is null.
 * CompletedResponse therefore marks the recognition STREAM ending. On a continuous
 * {@code AudioSource.fromMic} stream it does not follow a final hypothesis on its own, and
 * a cancelled collection never emits it at all.
 *
 * Product invariant: the first valid FinalTextResponse is the terminal for the utterance.
 * Native teardown always runs; JavaScript settles at most once. Late completed, error,
 * cancellation, partial, or second final are inert.
 */
final class AdvancedUtteranceLifecycle {
    /** What the caller owes JavaScript for this response. */
    enum Outcome {
        /** Not a product terminal, or JavaScript has already settled. Do nothing. */
        IGNORE,
        /** This caller owns the terminal and must resolve {@link #transcript()}. */
        RESOLVE_TRANSCRIPT,
        /** This caller owns the terminal and must reject no_speech. */
        REJECT_NO_SPEECH
    }

    private final AdvancedSpeechTranscriptAccumulator accumulator = new AdvancedSpeechTranscriptAccumulator();
    private final AdvancedUtteranceTerminal terminal = new AdvancedUtteranceTerminal();

    void onPartial(String text) {
        accumulator.onPartial(text);
    }

    /**
     * A FinalTextResponse. A valid final claims the product terminal and starts native
     * teardown; the stream is not kept alive waiting for CompletedResponse.
     *
     * A final that fails transcript validation (null, blank, or over the 260-character
     * safety limit) is not a terminal: the stream keeps running so a later valid final can
     * still win, and a stream that ends without one still reports no_speech.
     */
    Outcome onFinal(
        String text,
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown
    ) {
        if (!accumulator.onFinal(text)) return Outcome.IGNORE;
        if (!terminal.claimProductCompletion(operations, operation, nativeTeardown)) return Outcome.IGNORE;
        return Outcome.RESOLVE_TRANSCRIPT;
    }

    /**
     * A CompletedResponse. Harmless and idempotent once a final has claimed the terminal.
     * Only a stream that ended without any valid final settles here, and it settles
     * no_speech exactly as before.
     */
    Outcome onCompleted(
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown
    ) {
        if (!terminal.claimProductCompletion(operations, operation, nativeTeardown)) return Outcome.IGNORE;
        return accumulator.hasFinal() ? Outcome.RESOLVE_TRANSCRIPT : Outcome.REJECT_NO_SPEECH;
    }

    /**
     * An Advanced failure. Same-turn platform fallback stays governed by
     * {@link AdvancedCaptureBoundary} and is additionally impossible once any terminal has
     * been claimed, so a successful final permanently closes the fallback door.
     */
    boolean claimSameTurnPlatformFallback(
        boolean captureHandedToSdk,
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation
    ) {
        return AdvancedCaptureBoundary.allowSameTurnPlatformFallback(captureHandedToSdk)
            && operations.isActive(operation)
            && terminal.markAdvancedFinishedKeepOperation();
    }

    /** Claims the JavaScript terminal for an Advanced failure. */
    boolean claimErrorTerminal(
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown
    ) {
        return terminal.claimProductCompletion(operations, operation, nativeTeardown);
    }

    boolean hasFinal() {
        return accumulator.hasFinal();
    }

    String transcript() {
        return accumulator.finalTranscriptOrNull();
    }
}
