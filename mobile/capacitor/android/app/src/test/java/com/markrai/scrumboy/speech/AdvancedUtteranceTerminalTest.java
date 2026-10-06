package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;

public class AdvancedUtteranceTerminalTest {
    @Test
    public void cancelRacingFinalCallbackYieldsOneProductCompletionAndOneTeardown() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        AtomicInteger jsCompletions = new AtomicInteger();
        AtomicInteger nativeTeardowns = new AtomicInteger();
        java.util.concurrent.atomic.AtomicBoolean closed = new java.util.concurrent.atomic.AtomicBoolean(false);
        SpeechInputOperationRegistry.Operation operation = operations.begin(
            "speech-1",
            jsCompletions::incrementAndGet
        );
        AdvancedUtteranceTerminal terminal = new AdvancedUtteranceTerminal();
        Runnable teardown = () -> {
            if (closed.compareAndSet(false, true)) nativeTeardowns.incrementAndGet();
        };

        SpeechInputOperationRegistry.Operation cancelled = operations.cancel("speech-1");
        assertTrue(cancelled != null);
        cancelled.deliverCancellation();
        teardown.run();

        assertFalse(terminal.claimProductCompletion(operations, operation, teardown));
        assertFalse(terminal.claimProductCompletion(operations, operation, teardown));
        assertEquals(1, jsCompletions.get());
        assertEquals(1, nativeTeardowns.get());
    }

    @Test
    public void winningCallbackTearsDownOnceThenSettlesProduct() throws Exception {
        SpeechInputOperationRegistry operations = new SpeechInputOperationRegistry();
        AtomicInteger nativeTeardowns = new AtomicInteger();
        java.util.concurrent.atomic.AtomicBoolean closed = new java.util.concurrent.atomic.AtomicBoolean(false);
        SpeechInputOperationRegistry.Operation operation = operations.begin("speech-1", () -> {});
        AdvancedUtteranceTerminal terminal = new AdvancedUtteranceTerminal();
        Runnable teardown = () -> {
            if (closed.compareAndSet(false, true)) nativeTeardowns.incrementAndGet();
        };

        assertTrue(terminal.claimProductCompletion(operations, operation, teardown));
        assertFalse(terminal.claimProductCompletion(operations, operation, teardown));
        assertEquals(1, nativeTeardowns.get());
        assertFalse(operations.claimCompletion(operation));
    }
}
