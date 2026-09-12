import { getVoiceSpeechRate } from '../core/voice-speech-rate-preferences.js';
export function prepareTextForSpeechSynthesis(text) {
    const value = String(text ?? "");
    const leadingCommand = value.replace(/^(Create|Open|Delete|Move|Assign|Unassign) todo\b/i, "$1 to do");
    return leadingCommand.replace(/^(Add tag .+ to|Remove tag .+ from) todo(?=\s+#\d+\b)/i, "$1 to do");
}
export function speak(text, options = {}) {
    return new Promise((resolve) => {
        const synth = window.speechSynthesis;
        if (!synth || typeof SpeechSynthesisUtterance === "undefined") {
            resolve();
            return;
        }
        let settled = false;
        const utterance = new SpeechSynthesisUtterance(prepareTextForSpeechSynthesis(text));
        utterance.rate = getVoiceSpeechRate();
        const cleanup = () => {
            utterance.onend = null;
            utterance.onerror = null;
            options.signal?.removeEventListener("abort", onAbort);
        };
        const finish = () => {
            if (settled)
                return;
            settled = true;
            cleanup();
            resolve();
        };
        const onAbort = () => {
            try {
                synth.cancel();
            }
            catch {
            }
            finish();
        };
        utterance.onend = finish;
        utterance.onerror = finish;
        if (options.signal?.aborted) {
            onAbort();
            return;
        }
        options.signal?.addEventListener("abort", onAbort, { once: true });
        try {
            synth.cancel();
            synth.speak(utterance);
        }
        catch {
            finish();
        }
    });
}
