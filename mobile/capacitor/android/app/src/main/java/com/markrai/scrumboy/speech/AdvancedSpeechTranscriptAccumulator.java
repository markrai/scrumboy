package com.markrai.scrumboy.speech;

/**
 * Accumulates Advanced recognition text. Only FinalTextResponse values may become product transcripts.
 */
final class AdvancedSpeechTranscriptAccumulator {
    private String finalTranscript;

    synchronized void onPartial(String ignored) {
        // Partials are never promoted to command input.
    }

    /**
     * Stores a FinalTextResponse transcript. Returns true when this value passed
     * validation and is therefore usable as the product terminal for this utterance.
     * A rejected final (null, blank, or over the 260-character safety limit) leaves the
     * stream running so a later valid final can still win; if none arrives, stream
     * completion still reports no_speech.
     */
    synchronized boolean onFinal(String text) {
        if (text == null) return false;
        String trimmed = text.trim();
        if (trimmed.isEmpty() || trimmed.length() > 260) return false;
        finalTranscript = trimmed;
        return true;
    }

    synchronized String finalTranscriptOrNull() {
        return finalTranscript;
    }

    synchronized boolean hasFinal() {
        return finalTranscript != null && !finalTranscript.isEmpty();
    }
}
