package com.markrai.scrumboy.localai;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class LocalAiRequestValidatorTest {
    @Test
    public void acceptsExactSharedContractBoundsWithoutNormalizingText() throws Exception {
        String requestId = "r".repeat(128);
        String input = " i".repeat(16_384);
        String instructions = "s".repeat(8_192);

        assertEquals(requestId, LocalAiRequestValidator.requestId(requestId));
        assertEquals(input, LocalAiRequestValidator.input(input));
        assertEquals(instructions, LocalAiRequestValidator.instructions(instructions));
        assertEquals(256, LocalAiRequestValidator.maximumOutputTokens(256));
    }

    @Test
    public void rejectsMalformedAndOversizedRequestsWithStableCodes() {
        assertCode("invalid_request", () -> LocalAiRequestValidator.operationId(""));
        assertCode("invalid_request", () -> LocalAiRequestValidator.requestId("r".repeat(129)));
        assertCode("invalid_request", () -> LocalAiRequestValidator.input(" \n "));
        assertCode("input_too_large", () -> LocalAiRequestValidator.input("i".repeat(32_769)));
        assertCode("invalid_request", () -> LocalAiRequestValidator.instructions("s".repeat(8_193)));
        assertCode("invalid_request", () -> LocalAiRequestValidator.maximumOutputTokens(257));
        assertCode("output_rejected", () -> LocalAiRequestValidator.output("o".repeat(65_537)));
    }

    @Test
    public void boundsProviderModelDiagnostics() {
        assertEquals(128, LocalAiRequestValidator.providerModel("m".repeat(256)).length());
    }

    private static void assertCode(String code, CheckedRunnable action) {
        LocalAiException error = assertThrows(LocalAiException.class, action::run);
        assertEquals(code, error.code());
    }

    @FunctionalInterface
    private interface CheckedRunnable {
        void run() throws Exception;
    }
}
