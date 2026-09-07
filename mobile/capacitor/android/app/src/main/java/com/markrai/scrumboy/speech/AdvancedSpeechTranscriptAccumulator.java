package com.markrai.scrumboy.speech;

/**
 * Accumulates Advanced recognition text. Only FinalTextResponse values may become product transcripts.
 */
final class AdvancedSpeechTranscriptAccumulator {
    private String finalTranscript;

    synchronized void onPartial(String ignored) {
        // Partials are never promoted to command input.
    }

    synchronized void onFinal(String text) {
        if (text == null) return;
        String trimmed = text.trim();
        if (trimmed.isEmpty() || trimmed.length() > 260) return;
        finalTranscript = trimmed;
    }

    synchronized String finalTranscriptOrNull() {
        return finalTranscript;
    }

    synchronized boolean hasFinal() {
        return finalTranscript != null && !finalTranscript.isEmpty();
    }
}
