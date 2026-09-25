package com.markrai.scrumboy.localai;

interface LocalTextGenerationProvider extends AutoCloseable {
    LocalAiStatus status(LocalAiOperationRegistry.Operation operation) throws LocalAiException;

    void prepare(LocalAiOperationRegistry.Operation operation) throws LocalAiException;

    String generate(
        LocalAiOperationRegistry.Operation operation,
        String input,
        String instructions,
        int maximumOutputTokens
    ) throws LocalAiException;

    @Override
    void close();
}
