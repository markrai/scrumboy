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
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 45_000 })).not.toThrow();
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 45_000, aggregationMode: 'create_v2', postFinalGraceMs: 4_000, captureContext: 'initial_enhanced_voiceflow_capture' })).not.toThrow();
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 45_000, captureContext: 'binary_clarification_capture' })).not.toThrow();
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 45_001 }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 1_000, captureContext: 'other' as never }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
    expect(() => validateSpeechInputListenOptions({ maxDurationMs: 1_000, language: 'en_US' }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
  });

  it.each([
    { transcript: 'Open story 355' },
    { transcript: 'Create Big Man', segmentCount: 1 },
    { transcript: 'Create Big Man put it in Backlog', segmentCount: 2 },
  ])('accepts the exact speech result contract: %#', result => {
    expect(() => validateSpeechInputResult(result)).not.toThrow();
  });

  it('rejects provider residue and empty transcripts', () => {
    expect(() => validateSpeechInputResult({ transcript: 'Open story 355', audio: 'not-allowed' }))
      .toThrowError(expect.objectContaining({ code: 'recognition_failed' }));
    expect(() => validateSpeechInputResult({ transcript: '   ' }))
      .toThrowError(expect.objectContaining({ code: 'recognition_failed' }));
  });

  it.each([
    { transcript: 'x', segmentCount: 0 },
    { transcript: 'x', segmentCount: -1 },
    { transcript: 'x', segmentCount: 1.5 },
    { transcript: 'x', segmentCount: '2' },
    { transcript: 'x', segmentCount: null },
    { transcript: 'x', segmentCount: undefined },
    { transcript: 'x', segmentCount: 2, arbitrary: 'bad' },
    { segmentCount: 2 },
  ])('fails closed for a malformed or extended speech result: %#', result => {
    expect(() => validateSpeechInputResult(result))
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
