export type VoiceSpeechRate = 1 | 1.25 | 1.5 | 1.75 | 2;

export const VOICE_SPEECH_RATE_STORAGE_KEY = 'scrumboy.voiceSpeechRate';
export const VOICE_SPEECH_RATE_DEFAULT: VoiceSpeechRate = 1;
export const VOICE_SPEECH_RATE_PRESETS: readonly VoiceSpeechRate[] = Object.freeze([
  1,
  1.25,
  1.5,
  1.75,
  2,
]);

export const VOICE_SPEECH_RATE_DISPLAY_LABELS: Readonly<Record<VoiceSpeechRate, string>> = Object.freeze({
  1: '1.0x',
  1.25: '1.25x',
  1.5: '1.50x',
  1.75: '1.75x',
  2: '2.0x',
});

const CANONICAL_STRINGS: Readonly<Record<VoiceSpeechRate, string>> = Object.freeze({
  1: '1',
  1.25: '1.25',
  1.5: '1.5',
  1.75: '1.75',
  2: '2',
});

const ACCEPTED_STRINGS: Readonly<Record<string, VoiceSpeechRate>> = Object.freeze({
  '1': 1,
  '1.0': 1,
  '1.25': 1.25,
  '1.5': 1.5,
  '1.50': 1.5,
  '1.75': 1.75,
  '2': 2,
  '2.0': 2,
});

export function normalizeVoiceSpeechRate(value: unknown): VoiceSpeechRate {
  if (typeof value === 'string') return ACCEPTED_STRINGS[value] ?? VOICE_SPEECH_RATE_DEFAULT;
  if (typeof value !== 'number' || !Number.isFinite(value)) return VOICE_SPEECH_RATE_DEFAULT;
  return VOICE_SPEECH_RATE_PRESETS.includes(value as VoiceSpeechRate)
    ? value as VoiceSpeechRate
    : VOICE_SPEECH_RATE_DEFAULT;
}

export function getVoiceSpeechRate(): VoiceSpeechRate {
  try {
    return normalizeVoiceSpeechRate(globalThis.localStorage?.getItem(VOICE_SPEECH_RATE_STORAGE_KEY));
  } catch {
    return VOICE_SPEECH_RATE_DEFAULT;
  }
}

export function setVoiceSpeechRate(rate: VoiceSpeechRate): void {
  try {
    const normalized = normalizeVoiceSpeechRate(rate);
    globalThis.localStorage?.setItem(VOICE_SPEECH_RATE_STORAGE_KEY, CANONICAL_STRINGS[normalized]);
  } catch {
    // Device-local storage can be unavailable without blocking Settings or speech.
  }
}

export function voiceSpeechRateFromSliderPosition(position: unknown): VoiceSpeechRate {
  if (typeof position !== 'number' || !Number.isInteger(position)) return VOICE_SPEECH_RATE_DEFAULT;
  return VOICE_SPEECH_RATE_PRESETS[position] ?? VOICE_SPEECH_RATE_DEFAULT;
}

export function sliderPositionForVoiceSpeechRate(rate: unknown): number {
  return VOICE_SPEECH_RATE_PRESETS.indexOf(normalizeVoiceSpeechRate(rate));
}

export function voiceSpeechRateDisplayLabel(rate: unknown): string {
  return VOICE_SPEECH_RATE_DISPLAY_LABELS[normalizeVoiceSpeechRate(rate)];
}
