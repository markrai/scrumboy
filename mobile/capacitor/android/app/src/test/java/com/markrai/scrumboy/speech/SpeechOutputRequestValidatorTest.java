package com.markrai.scrumboy.speech;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class SpeechOutputRequestValidatorTest {
    @Test
    public void acceptsBoundedTextAndLanguage() throws Exception {
        SpeechOutputRequestValidator.validate("Done.", "en-US");
        SpeechOutputRequestValidator.validate("x".repeat(600), "en-US-x-local");
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
}
