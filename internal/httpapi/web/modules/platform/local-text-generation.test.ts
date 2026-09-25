import { describe, expect, expectTypeOf, it } from 'vitest';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  LOCAL_TEXT_GENERATION_LIMITS,
  LocalTextGenerationError,
  isLocalTextGenerationErrorCode,
  validateLocalTextGenerationOutput,
  validateLocalTextGenerationRequest,
  type LocalTextGenerationErrorCode,
  type LocalTextGenerationStatus,
} from './local-text-generation.js';

const ERROR_CODES: LocalTextGenerationErrorCode[] = [
  'unsupported',
  'disabled',
  'not_ready',
  'download_failed',
  'foreground_required',
  'busy',
  'quota_exceeded',
  'insufficient_storage',
  'input_too_large',
  'invalid_request',
  'output_rejected',
  'cancelled',
  'internal',
];

function validRequest() {
  return {
    requestId: 'request-1',
    input: 'Write one short sentence.',
    instructions: 'Be concise.',
    maximumOutputTokens: 32,
  };
}

describe('local text generation contract', () => {
  it('publishes one operation-oriented capability ID and every status shape', () => {
    expect(LOCAL_TEXT_GENERATION_CAPABILITY).toBe('local-text-generation');
    const statuses = [
      { state: 'unsupported', reason: 'device' },
      { state: 'action-required', action: 'download' },
      { state: 'preparing', downloadedBytes: 1, totalBytes: 2 },
      { state: 'ready', maximumOutputTokens: 256, contextTokenLimit: 4_096, providerModel: 'nano-v4' },
      { state: 'temporarily-unavailable', reason: 'busy', retryAfterMs: 500 },
    ] satisfies LocalTextGenerationStatus[];

    expect(statuses.map(({ state }) => state)).toEqual([
      'unsupported',
      'action-required',
      'preparing',
      'ready',
      'temporarily-unavailable',
    ]);
    expectTypeOf(statuses).toMatchTypeOf<LocalTextGenerationStatus[]>();
  });

  it('recognizes every stable error code and preserves bounded retry metadata', () => {
    for (const code of ERROR_CODES) {
      expect(isLocalTextGenerationErrorCode(code)).toBe(true);
      const error = new LocalTextGenerationError(code, { retryAfterMs: 750 });
      expect(error).toMatchObject({ name: 'LocalTextGenerationError', code, retryAfterMs: 750 });
      expect(error.message).not.toContain('prompt');
    }
    expect(isLocalTextGenerationErrorCode('provider-message')).toBe(false);
    expect(new LocalTextGenerationError('busy', { retryAfterMs: -1 }).retryAfterMs).toBeUndefined();
    expect(new LocalTextGenerationError('busy', {
      retryAfterMs: LOCAL_TEXT_GENERATION_LIMITS.retryAfterMs + 1,
    }).retryAfterMs).toBeUndefined();
  });

  it('accepts exact request boundaries including the conservative 256-token product cap', () => {
    expect(LOCAL_TEXT_GENERATION_LIMITS.maximumOutputTokens).toBe(256);
    expect(() => validateLocalTextGenerationRequest({
      requestId: 'r'.repeat(LOCAL_TEXT_GENERATION_LIMITS.requestIdCodeUnits),
      input: 'i'.repeat(LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits),
      instructions: 's'.repeat(LOCAL_TEXT_GENERATION_LIMITS.instructionCodeUnits),
      maximumOutputTokens: 256,
    })).not.toThrow();
  });

  it.each([
    ['blank request ID', { requestId: '' }],
    ['long request ID', { requestId: 'r'.repeat(129) }],
    ['blank input', { input: '  \n ' }],
    ['long instructions', { instructions: 's'.repeat(8_193) }],
    ['zero output tokens', { maximumOutputTokens: 0 }],
    ['non-integer output tokens', { maximumOutputTokens: 1.5 }],
    ['provider-sized output request outside C5.1 policy', { maximumOutputTokens: 4_096 }],
  ])('rejects %s before provider invocation', (_name, patch) => {
    expect(() => validateLocalTextGenerationRequest({ ...validRequest(), ...patch }))
      .toThrowError(expect.objectContaining({ code: 'invalid_request' }));
  });

  it('distinguishes oversized input from malformed requests', () => {
    expect(() => validateLocalTextGenerationRequest({
      ...validRequest(),
      input: 'i'.repeat(LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits + 1),
    })).toThrowError(expect.objectContaining({ code: 'input_too_large' }));
  });

  it('validates provider output without trimming or normalizing it', () => {
    const text = '  exact output  ';
    expect(() => validateLocalTextGenerationOutput(text)).not.toThrow();
    expect(text).toBe('  exact output  ');
    expect(() => validateLocalTextGenerationOutput('x'.repeat(65_537)))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
    expect(() => validateLocalTextGenerationOutput(null))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });
});
