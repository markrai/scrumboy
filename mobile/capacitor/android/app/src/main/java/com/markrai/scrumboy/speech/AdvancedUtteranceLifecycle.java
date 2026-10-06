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
 * In single mode the first valid final is terminal. Create v2 uses the same continuous
 * stream in aggregate mode: finals are authoritative segments and a bounded grace window
 * chooses the VoiceFlow turn boundary.
 */
final class AdvancedUtteranceLifecycle {
    /** What the caller owes JavaScript for this response. */
    enum Outcome {
        /** Not a product terminal, or JavaScript has already settled. Do nothing. */
        IGNORE,
        SEGMENT_FINAL,
        /** This caller owns the terminal and must resolve {@link #transcript()}. */
        RESOLVE_TRANSCRIPT,
        /** This caller owns the terminal and must reject no_speech. */
        REJECT_NO_SPEECH
    }

    private final AdvancedSpeechTranscriptAccumulator accumulator = new AdvancedSpeechTranscriptAccumulator();
    private final AdvancedUtteranceTerminal terminal = new AdvancedUtteranceTerminal();
    private final Object aggregationLock = new Object();
    private AdvancedUtteranceScheduler scheduler;
    private Runnable grace;
    private boolean aggregate;
    private long graceMs;
    private SpeechInputOperationRegistry operations;
    private SpeechInputOperationRegistry.Operation operation;
    private Runnable nativeTeardown;
    private Runnable resolve;

    void onPartial(String text) {
        accumulator.onPartial(text);
        synchronized (aggregationLock) {
            if (aggregate && accumulator.hasFinal() && text != null && !text.trim().isEmpty()) scheduleGraceLocked();
        }
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
        Runnable nativeTeardown,
        boolean aggregate,
        long graceMs,
        android.os.Handler handler,
        Runnable resolve
    ) {
        return onFinal(
            text,
            operations,
            operation,
            nativeTeardown,
            aggregate,
            graceMs,
            handler == null ? null : new HandlerAdvancedUtteranceScheduler(handler),
            resolve
        );
    }

    Outcome onFinal(
        String text,
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown,
        boolean aggregate,
        long graceMs,
        AdvancedUtteranceScheduler scheduler,
        Runnable resolve
    ) {
        if (!aggregate && accumulator.hasFinal()) return Outcome.IGNORE;
        if (!accumulator.onFinal(text)) return Outcome.IGNORE;
        if (aggregate) {
            synchronized (aggregationLock) {
                this.aggregate = true; this.graceMs = graceMs; this.scheduler = scheduler;
                this.operations = operations; this.operation = operation; this.nativeTeardown = nativeTeardown; this.resolve = resolve;
                scheduleGraceLocked();
            }
            return Outcome.SEGMENT_FINAL;
        }
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
        synchronized (aggregationLock) { if (scheduler != null && grace != null) scheduler.remove(grace); }
        if (!terminal.claimProductCompletion(operations, operation, nativeTeardown)) return Outcome.IGNORE;
        return accumulator.hasFinal() ? Outcome.RESOLVE_TRANSCRIPT : Outcome.REJECT_NO_SPEECH;
    }

    Outcome onFinal(
        String text,
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown
    ) {
        return onFinal(text, operations, operation, nativeTeardown, false, 0, (AdvancedUtteranceScheduler) null, null);
    }

    private void scheduleGraceLocked() {
        if (scheduler == null) return;
        if (grace != null) scheduler.remove(grace);
        grace = () -> {
            SpeechInputOperationRegistry.Operation op;
            SpeechInputOperationRegistry ops;
            Runnable teardown;
            Runnable complete;
            synchronized (aggregationLock) { op = operation; ops = operations; teardown = nativeTeardown; complete = resolve; grace = null; }
            if (op != null && terminal.claimProductCompletion(ops, op, teardown) && complete != null) complete.run();
        };
        scheduler.post(grace, graceMs);
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

    int segmentCount() { return accumulator.segmentCount(); }
}

/** The two timer operations owned by one aggregate utterance. */
interface AdvancedUtteranceScheduler {
    void post(Runnable runnable, long delayMs);
    void remove(Runnable runnable);
}

/** Keeps Android Handler details at the production edge of the lifecycle. */
final class HandlerAdvancedUtteranceScheduler implements AdvancedUtteranceScheduler {
    private final android.os.Handler handler;

    HandlerAdvancedUtteranceScheduler(android.os.Handler handler) {
        this.handler = handler;
    }

    @Override
    public void post(Runnable runnable, long delayMs) {
        handler.postDelayed(runnable, delayMs);
    }

    @Override
    public void remove(Runnable runnable) {
        handler.removeCallbacks(runnable);
    }
}
