package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CyclicBarrier;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

/**
 * Product lifecycle for one Advanced utterance, driven with the response sequences observed
 * on a physical Pixel 10 rather than the sequence the previous implementation assumed.
 *
 * ML Kit genai-speech-recognition 1.0.0-alpha1 emits CompletedResponse from the SDK's
 * {@code Flow.onCompletion} block and only when the terminating cause is null. On a
 * continuous {@code AudioSource.fromMic} stream that does not happen after a final
 * hypothesis, and a cancelled collection never emits it at all. FinalTextResponse is
 * therefore the only response that can terminate a VoiceFlow utterance in practice.
 */
public class AdvancedUtteranceLifecycleTest {
    /** Records JavaScript settlements and native teardowns for one utterance. */
    private static final class Utterance {
        final SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        final AdvancedUtteranceLifecycle lifecycle = new AdvancedUtteranceLifecycle();
        final List<String> js = new ArrayList<>();
        final AtomicInteger nativeTeardowns = new AtomicInteger();
        final SpeechInputOperationRegistry.Operation operation;

        Utterance() throws SpeechInputException {
            operation = operations.begin("speech-1", () -> js.add("reject:cancelled"));
        }

        /** Mirrors the plugin's exact-handle teardown: idempotent, always runs. */
        Runnable teardown() {
            return nativeTeardowns::incrementAndGet;
        }

        void settle(AdvancedUtteranceLifecycle.Outcome outcome) {
            switch (outcome) {
                case RESOLVE_TRANSCRIPT -> js.add("resolve:" + lifecycle.transcript());
                case REJECT_NO_SPEECH -> js.add("reject:no_speech");
                case IGNORE -> { }
            }
        }
    }

    /**
     * The Pixel 10 regression. Recognition starts, partials stream, an authoritative final
     * arrives, and no CompletedResponse ever follows. The utterance must resolve on the
     * final itself rather than waiting for a stream terminal that never comes.
     */
    @Test
    public void finalWithoutCompletedResolvesTheUtteranceImmediately() throws Exception {
        Utterance utterance = new Utterance();

        utterance.lifecycle.onPartial("move bogus");
        utterance.lifecycle.onPartial("move bogus back to");
        assertTrue("no partial may settle JavaScript", utterance.js.isEmpty());

        AdvancedUtteranceLifecycle.Outcome outcome = utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        );
        utterance.settle(outcome);

        // No CompletedResponse is delivered, exactly as observed on the device.
        assertEquals(AdvancedUtteranceLifecycle.Outcome.RESOLVE_TRANSCRIPT, outcome);
        assertEquals(List.of("resolve:move bogus back to backlog"), utterance.js);
        assertEquals("Advanced teardown must begin on the final", 1, utterance.nativeTeardowns.get());
        assertEquals(0, utterance.operations.activeCount());
    }

    /** A CompletedResponse that does arrive later is inert. */
    @Test
    public void finalThenCompletedSettlesJavaScriptOnce() throws Exception {
        Utterance utterance = new Utterance();

        utterance.settle(utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));
        AdvancedUtteranceLifecycle.Outcome late = utterance.lifecycle.onCompleted(
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        );
        utterance.settle(late);

        assertEquals(AdvancedUtteranceLifecycle.Outcome.IGNORE, late);
        assertEquals(List.of("resolve:move bogus back to backlog"), utterance.js);
        assertEquals(0, utterance.operations.activeCount());
    }

    /** A late error cannot reject after a final, and cannot open the fallback door. */
    @Test
    public void finalThenErrorKeepsTheFinalAuthoritative() throws Exception {
        Utterance utterance = new Utterance();

        utterance.settle(utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));

        assertFalse(
            "a successful final permanently closes same-turn fallback",
            utterance.lifecycle.claimSameTurnPlatformFallback(false, utterance.operations, utterance.operation)
        );
        assertFalse(
            "a late error cannot claim the JavaScript terminal",
            utterance.lifecycle.claimErrorTerminal(utterance.operations, utterance.operation, utterance.teardown())
        );

        assertEquals(List.of("resolve:move bogus back to backlog"), utterance.js);
    }

    /** Final wins the race: cancellation cannot convert a delivered transcript into a failure. */
    @Test
    public void finalWinningCancellationResolvesExactlyOnce() throws Exception {
        Utterance utterance = new Utterance();

        utterance.settle(utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));

        SpeechInputOperationRegistry.Operation cancelled = utterance.operations.cancel("speech-1");
        assertNull("the operation is already terminal", cancelled);

        assertEquals(List.of("resolve:move bogus back to backlog"), utterance.js);
    }

    /** Cancellation wins the race: a final arriving afterwards cannot resurrect the operation. */
    @Test
    public void cancellationWinningFinalRemainsAuthoritative() throws Exception {
        Utterance utterance = new Utterance();

        SpeechInputOperationRegistry.Operation cancelled = utterance.operations.cancel("speech-1");
        assertNotNull(cancelled);
        cancelled.deliverCancellation();

        AdvancedUtteranceLifecycle.Outcome late = utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        );
        utterance.settle(late);

        assertEquals(AdvancedUtteranceLifecycle.Outcome.IGNORE, late);
        assertEquals(List.of("reject:cancelled"), utterance.js);
        assertEquals("native teardown still runs for the late final", 1, utterance.nativeTeardowns.get());
    }

    /** Two threads race a final against cancellation; exactly one JavaScript terminal results. */
    @Test
    public void finalRacingCancellationAcrossThreadsYieldsOneTerminal() throws Exception {
        for (int attempt = 0; attempt < 2_000; attempt += 1) {
            Utterance utterance = new Utterance();
            List<String> js = java.util.Collections.synchronizedList(utterance.js);
            CyclicBarrier gate = new CyclicBarrier(2);
            AtomicInteger failures = new AtomicInteger();

            Thread canceller = new Thread(() -> {
                try {
                    gate.await();
                    SpeechInputOperationRegistry.Operation cancelled = utterance.operations.cancel("speech-1");
                    if (cancelled != null) cancelled.deliverCancellation();
                } catch (Exception error) {
                    failures.incrementAndGet();
                }
            }, "CapacitorPlugins");
            canceller.start();

            gate.await();
            utterance.settle(utterance.lifecycle.onFinal(
                "move bogus back to backlog",
                utterance.operations,
                utterance.operation,
                utterance.teardown()
            ));
            canceller.join();

            assertEquals(0, failures.get());
            assertEquals("exactly one JavaScript terminal", 1, js.size());
            assertEquals(1, utterance.nativeTeardowns.get());
        }
    }

    /**
     * Transcript validation is unchanged, including the 260-character safety constraint.
     *
     * Decision recorded by this test: a final that fails validation is NOT a product
     * terminal. The recognition stream keeps running so a later valid final can still win,
     * which preserves the previous behaviour where only validated text could ever become a
     * command. A stream that ends without any valid final still reports no_speech.
     */
    @Test
    public void invalidFinalIsNotAProductTerminalAndLetsTheStreamKeepListening() throws Exception {
        Utterance utterance = new Utterance();

        for (String rejected : new String[] { null, "", "   ", "x".repeat(261) }) {
            AdvancedUtteranceLifecycle.Outcome outcome = utterance.lifecycle.onFinal(
                rejected,
                utterance.operations,
                utterance.operation,
                utterance.teardown()
            );
            utterance.settle(outcome);
            assertEquals(AdvancedUtteranceLifecycle.Outcome.IGNORE, outcome);
        }

        assertTrue("a rejected final must not settle JavaScript", utterance.js.isEmpty());
        assertEquals("a rejected final must not tear down the recognizer", 0, utterance.nativeTeardowns.get());
        assertTrue("the operation is still listening", utterance.operations.activeCount() > 0);

        // The stream is still alive, so a later valid final still wins.
        utterance.settle(utterance.lifecycle.onFinal(
            "x".repeat(260),
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));
        assertEquals(List.of("resolve:" + "x".repeat(260)), utterance.js);
    }

    /** A stream that completes without any valid final still reports no_speech. */
    @Test
    public void completedWithoutAnyValidFinalRejectsNoSpeech() throws Exception {
        Utterance utterance = new Utterance();

        utterance.settle(utterance.lifecycle.onFinal(
            "   ",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));
        AdvancedUtteranceLifecycle.Outcome outcome = utterance.lifecycle.onCompleted(
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        );
        utterance.settle(outcome);

        assertEquals(AdvancedUtteranceLifecycle.Outcome.REJECT_NO_SPEECH, outcome);
        assertEquals(List.of("reject:no_speech"), utterance.js);
    }

    /** An Advanced failure before SDK capture keeps same-turn platform fallback available. */
    @Test
    public void advancedErrorBeforeCaptureStillAllowsSameTurnPlatformFallback() throws Exception {
        Utterance utterance = new Utterance();

        assertTrue(utterance.lifecycle.claimSameTurnPlatformFallback(
            false,
            utterance.operations,
            utterance.operation
        ));
        assertTrue("fallback keeps the same operation alive", utterance.operations.activeCount() > 0);
        assertTrue("fallback must not settle JavaScript", utterance.js.isEmpty());

        assertFalse(
            "fallback is claimed once",
            utterance.lifecycle.claimSameTurnPlatformFallback(false, utterance.operations, utterance.operation)
        );
    }

    /** The capture boundary is unchanged: after SDK handoff, same-turn fallback stays forbidden. */
    @Test
    public void captureHandedToTheSdkStillForbidsSameTurnPlatformFallback() throws Exception {
        Utterance utterance = new Utterance();

        assertFalse(utterance.lifecycle.claimSameTurnPlatformFallback(
            true,
            utterance.operations,
            utterance.operation
        ));
        assertTrue(utterance.operations.activeCount() > 0);
    }

    /** A successful final can never initiate fallback, whatever the capture boundary says. */
    @Test
    public void successfulFinalCanNeverInitiatePlatformFallback() throws Exception {
        Utterance utterance = new Utterance();

        utterance.settle(utterance.lifecycle.onFinal(
            "move bogus back to backlog",
            utterance.operations,
            utterance.operation,
            utterance.teardown()
        ));

        assertFalse(utterance.lifecycle.claimSameTurnPlatformFallback(
            false,
            utterance.operations,
            utterance.operation
        ));
        assertFalse(utterance.lifecycle.claimSameTurnPlatformFallback(
            true,
            utterance.operations,
            utterance.operation
        ));
        assertEquals(List.of("resolve:move bogus back to backlog"), utterance.js);
    }
}
