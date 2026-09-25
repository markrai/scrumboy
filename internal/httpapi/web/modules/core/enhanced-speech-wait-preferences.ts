export type EnhancedSpeechWaitPreset = 'fast' | 'normal' | 'patient';

export const ENHANCED_SPEECH_WAIT_STORAGE_KEY = 'scrumboy.voiceEnhancedSpeechWait';
export const ENHANCED_SPEECH_WAIT_DEFAULT: EnhancedSpeechWaitPreset = 'normal';

export const ENHANCED_SPEECH_WAIT_MS: Readonly<Record<EnhancedSpeechWaitPreset, number>> = Object.freeze({
  fast: 2_000,
  normal: 4_000,
  patient: 7_000,
});

export function normalizeEnhancedSpeechWaitPreset(value: unknown): EnhancedSpeechWaitPreset {
  return value === 'fast' || value === 'normal' || value === 'patient'
    ? value
    : ENHANCED_SPEECH_WAIT_DEFAULT;
}

export function getEnhancedSpeechWaitPreset(): EnhancedSpeechWaitPreset {
  try {
    return normalizeEnhancedSpeechWaitPreset(globalThis.localStorage?.getItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY));
  } catch {
    return ENHANCED_SPEECH_WAIT_DEFAULT;
  }
}

export function getEnhancedSpeechWaitMs(): number {
  return ENHANCED_SPEECH_WAIT_MS[getEnhancedSpeechWaitPreset()];
}

export function setEnhancedSpeechWaitPreset(preset: EnhancedSpeechWaitPreset): void {
  try {
    globalThis.localStorage?.setItem(
      ENHANCED_SPEECH_WAIT_STORAGE_KEY,
      normalizeEnhancedSpeechWaitPreset(preset),
    );
  } catch {
    // Device-local storage can be unavailable without blocking Settings or capture.
  }
}
