package com.markrai.scrumboy.speech;

enum SpeechInputProviderId {
    MLKIT_GENAI_ADVANCED("mlkit_genai_advanced"),
    ANDROID_ON_DEVICE("android_on_device");

    private final String diagnosticName;

    SpeechInputProviderId(String diagnosticName) {
        this.diagnosticName = diagnosticName;
    }

    String diagnosticName() {
        return diagnosticName;
    }
}
