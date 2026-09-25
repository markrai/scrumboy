package com.markrai.scrumboy.speech;

import java.util.concurrent.atomic.AtomicBoolean;

/**
 * One Advanced utterance may produce several native terminals (final, error,
 * completed, cancel). Native teardown always runs; JavaScript settles at most once.
 */
final class AdvancedUtteranceTerminal {
    private final AtomicBoolean advancedFinished = new AtomicBoolean(false);

    /**
     * Marks Advanced finished without claiming the speech operation, so same-turn
     * platform fallback can continue on the same PluginCall.
     */
    boolean markAdvancedFinishedKeepOperation() {
        return advancedFinished.compareAndSet(false, true);
    }

    /**
     * Always runs native teardown. Returns true only when this caller owns the
     * JavaScript terminal result.
     */
    boolean claimProductCompletion(
        SpeechInputOperationRegistry operations,
        SpeechInputOperationRegistry.Operation operation,
        Runnable nativeTeardown
    ) {
        if (nativeTeardown != null) nativeTeardown.run();
        if (!advancedFinished.compareAndSet(false, true)) return false;
        return operations.claimCompletion(operation);
    }
}
