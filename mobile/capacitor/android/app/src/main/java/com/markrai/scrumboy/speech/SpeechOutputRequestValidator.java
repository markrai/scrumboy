package com.markrai.scrumboy.speech;

import java.util.regex.Pattern;

final class SpeechOutputRequestValidator {
    private static final int MAX_TEXT_CODE_UNITS = 600;
    private static final int MAX_LANGUAGE_CODE_UNITS = 64;
    private static final Pattern LANGUAGE = Pattern.compile("^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$");

    static void validate(String text, String language) throws SpeechOutputException {
        if (
            text == null
            || text.trim().isEmpty()
            || text.length() > MAX_TEXT_CODE_UNITS
            || containsDisallowedControl(text)
            || language == null
            || language.isEmpty()
            || language.length() > MAX_LANGUAGE_CODE_UNITS
            || !LANGUAGE.matcher(language).matches()
        ) {
            throw new SpeechOutputException("invalid_request", false);
        }
    }

    private static boolean containsDisallowedControl(String text) {
        for (int index = 0; index < text.length(); index += 1) {
            char value = text.charAt(index);
            if (
                value <= 0x0008
                || value == 0x000b
                || value == 0x000c
                || (value >= 0x000e && value <= 0x001f)
                || (value >= 0x007f && value <= 0x009f)
            ) return true;
        }
        return false;
    }

    private SpeechOutputRequestValidator() {}
}
