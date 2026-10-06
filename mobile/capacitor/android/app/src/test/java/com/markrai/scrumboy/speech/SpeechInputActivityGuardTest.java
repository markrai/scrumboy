package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertFalse;

import org.junit.Test;

public class SpeechInputActivityGuardTest {
    @Test
    public void destroyedOrMissingActivityCannotDispatch() {
        assertFalse(SpeechInputActivityGuard.canDispatch(true, null));
        assertFalse(SpeechInputActivityGuard.canDispatch(false, null));
    }
}
