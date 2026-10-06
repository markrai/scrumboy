export const ENHANCED_SPEECH_WAIT_STORAGE_KEY = 'scrumboy.voiceEnhancedSpeechWait';
export const ENHANCED_SPEECH_WAIT_DEFAULT = 'normal';
export const ENHANCED_SPEECH_WAIT_MS = Object.freeze({
    fast: 2000,
    normal: 4000,
    patient: 7000,
});
export function normalizeEnhancedSpeechWaitPreset(value) {
    return value === 'fast' || value === 'normal' || value === 'patient'
        ? value
        : ENHANCED_SPEECH_WAIT_DEFAULT;
}
export function getEnhancedSpeechWaitPreset() {
    try {
        return normalizeEnhancedSpeechWaitPreset(globalThis.localStorage?.getItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY));
    }
    catch {
        return ENHANCED_SPEECH_WAIT_DEFAULT;
    }
}
export function getEnhancedSpeechWaitMs() {
    return ENHANCED_SPEECH_WAIT_MS[getEnhancedSpeechWaitPreset()];
}
export function setEnhancedSpeechWaitPreset(preset) {
    try {
        globalThis.localStorage?.setItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY, normalizeEnhancedSpeechWaitPreset(preset));
    }
    catch {
        // Device-local storage can be unavailable without blocking Settings or capture.
    }
}
