package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class SpeechOutputRequestValidatorTest {
    @Test
    public void acceptsBoundedTextAndLanguage() throws Exception {
        assertEquals(1.0f, SpeechOutputRequestValidator.validate("Done.", "en-US"), 0.0f);
        assertEquals(1.0f, SpeechOutputRequestValidator.validate("x".repeat(600), "en-US-x-local"), 0.0f);
    }

    @Test
    public void resolvesEveryProductPresetExactly() throws Exception {
        assertEquals(1.0f, validateRate(1.0d), 0.0f);
        assertEquals(1.25f, validateRate(1.25d), 0.0f);
        assertEquals(1.5f, validateRate(1.5d), 0.0f);
        assertEquals(1.75f, validateRate(1.75d), 0.0f);
        assertEquals(2.0f, validateRate(2.0d), 0.0f);
    }

    @Test
    public void acceptsGenericProtocolBoundaries() throws Exception {
        assertEquals(0.5f, validateRate(0.5d), 0.0f);
        assertEquals(3.0f, validateRate(3.0d), 0.0f);
    }

    @Test
    public void omittedRateAlwaysResolvesBackToOne() throws Exception {
        assertEquals(2.0f, validateRate(2.0d), 0.0f);
        assertEquals(1.0f, validateRate(null), 0.0f);
    }

    @Test
    public void rejectsInvalidRates() {
        assertInvalidRate("1.5");
        assertInvalidRate(0.0d);
        assertInvalidRate(-1.0d);
        assertInvalidRate(0.49d);
        assertInvalidRate(3.01d);
        assertInvalidRate(Double.NaN);
        assertInvalidRate(Double.POSITIVE_INFINITY);
        assertInvalidRate(Double.NEGATIVE_INFINITY);
    }

    @Test
    public void rejectsInvalidNativeRequests() {
        assertInvalid("");
        assertInvalid("   ");
        assertInvalid("x".repeat(601));
        assertInvalid("hello\u0000world");
        assertInvalid("hello\u0085world");
        assertInvalidLanguage("");
        assertInvalidLanguage("en--US");
        assertInvalidLanguage("en_US");
        assertInvalidLanguage("123");
    }

    private static void assertInvalid(String text) {
        SpeechOutputException error = assertThrows(
            SpeechOutputException.class,
            () -> SpeechOutputRequestValidator.validate(text, "en-US")
        );
        assertEquals("invalid_request", error.code());
    }

    private static void assertInvalidLanguage(String language) {
        SpeechOutputException error = assertThrows(
            SpeechOutputException.class,
            () -> SpeechOutputRequestValidator.validate("Done.", language)
        );
        assertEquals("invalid_request", error.code());
    }

    private static float validateRate(Object rate) throws SpeechOutputException {
        return SpeechOutputRequestValidator.validate("Done.", "en-US", rate);
    }

    private static void assertInvalidRate(Object rate) {
        SpeechOutputException error = assertThrows(
            SpeechOutputException.class,
            () -> validateRate(rate)
        );
        assertEquals("invalid_request", error.code());
    }
}
