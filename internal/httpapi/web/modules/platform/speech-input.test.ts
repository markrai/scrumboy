import { describe, expect, it } from 'vitest';
import {
  SPEECH_INPUT_MAX_DURATION_MS,
  SpeechInputError,
  validateSpeechInputListenOptions,
  validateSpeechInputResult,
} from './speech-input.js';

describe('speech-input contract', () => {
  it('accepts only bounded provider-neutral listen requests', () => {
    expect(() => validateSpeechInputListenOptions({
      maxDurationMs: SPEECH_INPUT_MAX_DURATION_MS,
      language: 'en-US',
    })).not.toThrow();
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 10_001 }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 1_000, language: 'en_US' }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
  });

  it('accepts a single bounded transcript and rejects provider residue', () => {
    expect(() => validateSpeechInputResult({ transcript: 'Open story 355' })).not.toThrow();
    expect(() => validateSpeechInputResult({ transcript: 'Open story 355', audio: 'not-allowed' }))
      .toThrowError(expect.objectContaining({ code: 'recognition_failed' }));
    expect(() => validateSpeechInputResult({ transcript: '   ' }))
      .toThrowError(expect.objectContaining({ code: 'recognition_failed' }));
  });

  it('keeps permission denial distinct from capability support', () => {
    expect(new SpeechInputError('permission_denied')).toMatchObject({
      code: 'permission_denied',
      recoverable: true,
    });
    expect(new SpeechInputError('permission_denied_permanently')).toMatchObject({
      code: 'permission_denied_permanently',
      recoverable: false,
    });
  });

  it('accepts only bounded provider diagnostics and never requires them', () => {
    expect(new SpeechInputError('recognition_failed', {
      providerCode: 3,
      providerReason: 'audio',
    })).toMatchObject({
      code: 'recognition_failed',
      providerCode: 3,
      providerReason: 'audio',
    });
    expect(new SpeechInputError('recognition_failed')).not.toHaveProperty('providerCode');
    expect(new SpeechInputError('recognition_failed', {
      providerCode: 100_000,
      providerReason: 'audio',
    })).not.toHaveProperty('providerCode');
  });
});
