package com.markrai.scrumboy.speech;

/**
 * Exact ownership for one platform (on-device SpeechRecognizer) utterance.
 *
 * The recognizer is created and its listener installed during prepare; microphone
 * acquisition happens only in {@link #start()}, which the plugin calls after the handle
 * has been published and cancellation attached to this exact handle.
 *
 * Cancellation is split in two because the plugin's threads differ. Capacitor dispatches
 * {@code @PluginMethod} bodies on its own {@code CapacitorPlugins} handler thread, while
 * every SpeechRecognizer call must be made on Android Main:
 *
 * <ul>
 *   <li>{@link #requestCancel()} is safe from any thread. It resolves the start/cancel
 *       race at a single linearization point, so a cancellation that arrives before
 *       {@link #start()} takes the transition guarantees {@code startListening} never runs.</li>
 *   <li>{@link #cancel()} and {@link #destroy()} perform the native release and must run
 *       on Android Main. The caller posts them there.</li>
 * </ul>
 *
 * Because the release is posted to Main and {@link #start()} also runs on Main, native
 * calls can never interleave: a release queued while start is in flight is delivered
 * behind {@code startListening} rather than in front of it.
 *
 * All methods are safe to call more than once; native cleanup runs once no matter which
 * of result, error, or cancellation arrives first.
 */
final class PlatformRecognitionHandle {
    /** Native recognizer operations, isolated so ownership is unit-testable. */
    interface Session {
        void startListening() throws SpeechInputException;

        void cancelRecognition();

        void destroyRecognizer();
    }

    /** One linearizable transition: PREPARED loses to whichever of start or cancel arrives first. */
    private enum State {
        PREPARED,
        STARTED,
        CANCELLED
    }

    private final Session session;
    private final Object lock = new Object();
    private State state = State.PREPARED;
    private boolean released;

    PlatformRecognitionHandle(Session session) {
        this.session = session;
    }

    /** Acquires the microphone exactly once, on Android Main, and never after cancellation. */
    void start() throws SpeechInputException {
        synchronized (lock) {
            if (state != State.PREPARED) return;
            state = State.STARTED;
        }
        try {
            session.startListening();
        } catch (SpeechInputException error) {
            destroy();
            throw error;
        } catch (RuntimeException error) {
            destroy();
            throw new SpeechInputException("recognition_failed", true);
        }
    }

    boolean started() {
        synchronized (lock) {
            return state == State.STARTED && !released;
        }
    }

    /**
     * Records cancellation from any thread, so a later {@link #start()} is a no-op.
     * Returns true when the native release is still outstanding, meaning the caller owns
     * dispatching {@link #cancel()} to Android Main.
     */
    boolean requestCancel() {
        synchronized (lock) {
            state = State.CANCELLED;
            return !released;
        }
    }

    /** Cancels and destroys the recognizer once. Android Main only. */
    void cancel() {
        synchronized (lock) {
            state = State.CANCELLED;
            if (released) return;
            released = true;
        }
        try {
            session.cancelRecognition();
        } finally {
            session.destroyRecognizer();
        }
    }

    /** Releases the recognizer once, for terminal result/error paths. Android Main only. */
    void destroy() {
        synchronized (lock) {
            if (released) return;
            released = true;
        }
        session.destroyRecognizer();
    }
}
