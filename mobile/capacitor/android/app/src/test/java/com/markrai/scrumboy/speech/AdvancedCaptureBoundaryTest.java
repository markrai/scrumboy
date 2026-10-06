package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class AdvancedCaptureBoundaryTest {
    @Test
    public void preSdkHandoffMayFallbackAndPostHandoffMustNot() {
        assertTrue(AdvancedCaptureBoundary.allowSameTurnPlatformFallback(false));
        assertFalse(AdvancedCaptureBoundary.allowSameTurnPlatformFallback(true));
    }
}
