// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  VOICE_SPEECH_RATE_STORAGE_KEY,
  getVoiceSpeechRate,
  normalizeVoiceSpeechRate,
  setVoiceSpeechRate,
  sliderPositionForVoiceSpeechRate,
  voiceSpeechRateDisplayLabel,
  voiceSpeechRateFromSliderPosition,
} from './voice-speech-rate-preferences.js';

beforeEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

afterEach(() => vi.unstubAllGlobals());

describe('Voice speech rate preferences', () => {
  it.each([
    [null, 1],
    [undefined, 1],
    [1, 1],
    ['1', 1],
    ['1.0', 1],
    [1.25, 1.25],
    ['1.25', 1.25],
    [1.5, 1.5],
    ['1.5', 1.5],
    ['1.50', 1.5],
    [1.75, 1.75],
    ['1.75', 1.75],
    [2, 2],
    ['2', 2],
    ['2.0', 2],
    ['', 1],
    ['garbage', 1],
    [0, 1],
    [-1, 1],
    [1.6, 1],
    [3, 1],
    [Number.NaN, 1],
    [Number.POSITIVE_INFINITY, 1],
    [Number.NEGATIVE_INFINITY, 1],
    [true, 1],
  ])('normalizes %j to %s', (value, expected) => {
    expect(normalizeVoiceSpeechRate(value)).toBe(expected);
  });

  it('defaults safely when storage is missing, corrupt, or unavailable', () => {
    expect(getVoiceSpeechRate()).toBe(1);
    localStorage.setItem(VOICE_SPEECH_RATE_STORAGE_KEY, '1.6');
    expect(getVoiceSpeechRate()).toBe(1);
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('unavailable'); },
      setItem: () => { throw new Error('unavailable'); },
    });
    expect(getVoiceSpeechRate()).toBe(1);
    expect(() => setVoiceSpeechRate(1.75)).not.toThrow();
  });

  it.each([
    [1, '1'],
    [1.25, '1.25'],
    [1.5, '1.5'],
    [1.75, '1.75'],
    [2, '2'],
  ] as const)('persists %s as canonical string %s', (rate, stored) => {
    setVoiceSpeechRate(rate);
    expect(localStorage.getItem(VOICE_SPEECH_RATE_STORAGE_KEY)).toBe(stored);
  });

  it('normalizes unsupported values before persistence', () => {
    setVoiceSpeechRate(1.6 as never);
    expect(localStorage.getItem(VOICE_SPEECH_RATE_STORAGE_KEY)).toBe('1');
  });

  it('maps exactly five slider positions to the product presets and display labels', () => {
    expect([0, 1, 2, 3, 4].map(voiceSpeechRateFromSliderPosition))
      .toEqual([1, 1.25, 1.5, 1.75, 2]);
    expect([1, 1.25, 1.5, 1.75, 2].map(sliderPositionForVoiceSpeechRate))
      .toEqual([0, 1, 2, 3, 4]);
    expect([1, 1.25, 1.5, 1.75, 2].map(voiceSpeechRateDisplayLabel))
      .toEqual(['1.0x', '1.25x', '1.50x', '1.75x', '2.0x']);
  });

  it('maps slider position 2 to numeric 1.5 and visible label 1.50x', () => {
    const rate = voiceSpeechRateFromSliderPosition(2);
    expect(rate).toBe(1.5);
    expect(voiceSpeechRateDisplayLabel(rate)).toBe('1.50x');
  });
});
