import { describe, expect, it } from 'vitest';
import {
  SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS,
  SpeechOutputError,
  validateSpeechOutputSpeakOptions,
} from './speech-output.js';

describe('speech-output contract', () => {
  it('accepts bounded provider-neutral text and language', () => {
    expect(() => validateSpeechOutputSpeakOptions({ text: 'Done.', language: 'en-US' }))
      .not.toThrow();
    expect(() => validateSpeechOutputSpeakOptions({
      text: 'x'.repeat(SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS),
      language: 'en-US-x-local',
    })).not.toThrow();
  });

  it.each([undefined, 0.5, 1, 1.25, 1.5, 1.75, 2, 3])(
    'accepts provider-neutral rate %s',
    (rate) => {
      expect(() => validateSpeechOutputSpeakOptions({ text: 'Done.', rate })).not.toThrow();
    },
  );

  it.each([
    '1.5',
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.NEGATIVE_INFINITY,
    0,
    -1,
    0.49,
    3.01,
  ])('rejects invalid provider-neutral rate %s', (rate) => {
    expect(() => validateSpeechOutputSpeakOptions({ text: 'Done.', rate } as never))
      .toThrowError(expect.objectContaining({ code: 'invalid_request', recoverable: false }));
  });

  it.each([
    { text: '' },
    { text: '   ' },
    { text: 'x'.repeat(SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS + 1) },
    { text: 'hello\u0000world' },
    { text: 'hello\u0085world' },
    { text: 'Done.', language: '' },
    { text: 'Done.', language: 'en_US' },
    { text: 'Done.', language: 'en--US' },
    { text: 'Done.', language: '123' },
  ])('rejects an invalid privileged-boundary request %#', (request) => {
    expect(() => validateSpeechOutputSpeakOptions(request))
      .toThrowError(expect.objectContaining({ code: 'invalid_request', recoverable: false }));
  });

  it('keeps stable error recoverability defaults', () => {
    expect(new SpeechOutputError('no_local_voice')).toMatchObject({ recoverable: false });
    expect(new SpeechOutputError('unsupported')).toMatchObject({ recoverable: false });
    expect(new SpeechOutputError('invalid_request')).toMatchObject({ recoverable: false });
    expect(new SpeechOutputError('cancelled')).toMatchObject({ recoverable: true });
    expect(new SpeechOutputError('synthesis_failed')).toMatchObject({ recoverable: true });
  });
});
