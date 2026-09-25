package com.markrai.scrumboy.speech;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.util.Log;

/**
 * Narrow VoiceFlow speech lifecycle diagnostics, mirroring the shell's
 * {@code voiceFlowDiagnostic(event, details)} shape so logcat and the web console read
 * the same way.
 *
 * Debuggable builds only, so production logcat stays quiet. Transcript text is never
 * logged; only lengths and lifecycle transitions, matching the shell policy of not
 * dumping recognized speech.
 */
final class SpeechInputDiagnostics {
    static final String TAG = "ScrumboyVoiceFlow";

    private static volatile Boolean enabled;

    private SpeechInputDiagnostics() {}

    static void configure(Context context) {
        if (context == null) return;
        enabled = (context.getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
    }

    static boolean isEnabled() {
        Boolean current = enabled;
        return current != null && current;
    }

    /** Emits {@code ASR <event> key=value ...}; pairs are flattened key, value, key, value. */
    static void emit(String event, Object... pairs) {
        if (!isEnabled()) return;
        StringBuilder line = new StringBuilder("ASR ").append(event);
        for (int index = 0; index + 1 < pairs.length; index += 2) {
            line.append(' ').append(pairs[index]).append('=').append(pairs[index + 1]);
        }
        Log.d(TAG, line.toString());
    }
}
