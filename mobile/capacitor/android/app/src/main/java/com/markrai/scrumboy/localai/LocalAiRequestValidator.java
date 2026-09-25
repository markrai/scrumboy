package com.markrai.scrumboy.localai;

final class LocalAiRequestValidator {
    static final int MAX_OPERATION_ID_CODE_UNITS = 128;
    static final int MAX_REQUEST_ID_CODE_UNITS = 128;
    static final int MAX_INPUT_CODE_UNITS = 32_768;
    static final int MAX_INSTRUCTION_CODE_UNITS = 8_192;
    static final int MAX_OUTPUT_TOKENS = 256;
    static final int MAX_OUTPUT_CODE_UNITS = 65_536;
    static final int MAX_PROVIDER_MODEL_CODE_UNITS = 128;

    private LocalAiRequestValidator() {}

    static String operationId(String value) throws LocalAiException {
        return identifier(value, MAX_OPERATION_ID_CODE_UNITS);
    }

    static String requestId(String value) throws LocalAiException {
        return identifier(value, MAX_REQUEST_ID_CODE_UNITS);
    }

    private static String identifier(String value, int maximum) throws LocalAiException {
        if (value == null || value.isEmpty() || value.length() > maximum) {
            throw new LocalAiException("invalid_request", false);
        }
        return value;
    }

    static String input(String value) throws LocalAiException {
        if (value == null || value.trim().isEmpty()) {
            throw new LocalAiException("invalid_request", false);
        }
        if (value.length() > MAX_INPUT_CODE_UNITS) {
            throw new LocalAiException("input_too_large", false);
        }
        return value;
    }

    static String instructions(String value) throws LocalAiException {
        if (value == null || value.length() > MAX_INSTRUCTION_CODE_UNITS) {
            throw new LocalAiException("invalid_request", false);
        }
        return value;
    }

    static int maximumOutputTokens(Integer value) throws LocalAiException {
        if (value == null || value < 1 || value > MAX_OUTPUT_TOKENS) {
            throw new LocalAiException("invalid_request", false);
        }
        return value;
    }

    static String output(String value) throws LocalAiException {
        if (value == null || value.length() > MAX_OUTPUT_CODE_UNITS) {
            throw new LocalAiException("output_rejected", false);
        }
        return value;
    }

    static String providerModel(String value) {
        if (value == null) return null;
        return value.length() <= MAX_PROVIDER_MODEL_CODE_UNITS
            ? value
            : value.substring(0, MAX_PROVIDER_MODEL_CODE_UNITS);
    }
}
