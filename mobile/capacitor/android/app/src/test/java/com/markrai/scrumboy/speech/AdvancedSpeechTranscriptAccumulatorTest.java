package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class AdvancedSpeechTranscriptAccumulatorTest {
    @Test
    public void keepsOnlyAuthoritativeFinalText() {
        AdvancedSpeechTranscriptAccumulator accumulator = new AdvancedSpeechTranscriptAccumulator();
        accumulator.onPartial("Move agenda");
        accumulator.onPartial("Move Agenda lane");
        assertFalse(accumulator.hasFinal());
        assertNull(accumulator.finalTranscriptOrNull());

        accumulator.onFinal("Move Agenda lane finalization to backlog");
        assertTrue(accumulator.hasFinal());
        assertEquals("Move Agenda lane finalization to backlog", accumulator.finalTranscriptOrNull());
    }

    @Test
    public void ignoresEmptyOrOversizedFinalText() {
        AdvancedSpeechTranscriptAccumulator accumulator = new AdvancedSpeechTranscriptAccumulator();
        accumulator.onFinal("   ");
        assertFalse(accumulator.hasFinal());
        accumulator.onFinal("x".repeat(261));
        assertFalse(accumulator.hasFinal());
    }
}
