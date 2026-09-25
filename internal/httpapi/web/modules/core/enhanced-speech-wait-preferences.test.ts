// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ENHANCED_SPEECH_WAIT_STORAGE_KEY,
  getEnhancedSpeechWaitMs,
  getEnhancedSpeechWaitPreset,
  normalizeEnhancedSpeechWaitPreset,
  setEnhancedSpeechWaitPreset,
} from './enhanced-speech-wait-preferences.js';

beforeEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('Enhanced speech wait preferences', () => {
  it.each([
    [null, 'normal'],
    [undefined, 'normal'],
    ['fast', 'fast'],
    ['normal', 'normal'],
    ['patient', 'patient'],
    ['unknown', 'normal'],
    [1234, 'normal'],
    [0, 'normal'],
    [500, 'normal'],
    [10000, 'normal'],
  ])('normalizes %j to %s', (value, expected) => {
    expect(normalizeEnhancedSpeechWaitPreset(value)).toBe(expected);
  });

  it('defaults to Normal and maps only canonical preset IDs to durations', () => {
    expect(getEnhancedSpeechWaitPreset()).toBe('normal');
    expect(getEnhancedSpeechWaitMs()).toBe(4_000);
    localStorage.setItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY, 'fast');
    expect(getEnhancedSpeechWaitMs()).toBe(2_000);
    localStorage.setItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY, 'patient');
    expect(getEnhancedSpeechWaitMs()).toBe(7_000);
  });

  it('falls back to Normal when storage is corrupt or unavailable', () => {
    localStorage.setItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY, 'obsolete');
    expect(getEnhancedSpeechWaitPreset()).toBe('normal');

    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('unavailable'); },
      setItem: () => { throw new Error('unavailable'); },
    });
    expect(getEnhancedSpeechWaitPreset()).toBe('normal');
    expect(getEnhancedSpeechWaitMs()).toBe(4_000);
    expect(() => setEnhancedSpeechWaitPreset('patient')).not.toThrow();
  });

  it('stores only the canonical preset ID', () => {
    setEnhancedSpeechWaitPreset('patient');
    expect(localStorage.getItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY)).toBe('patient');

    setEnhancedSpeechWaitPreset('500' as never);
    expect(localStorage.getItem(ENHANCED_SPEECH_WAIT_STORAGE_KEY)).toBe('normal');
  });
});
