export const SPEECH_OUTPUT_CAPABILITY = 'speech-output' as const;

export const SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS = 600;

export type SpeechOutputUnsupportedReason =
  | 'os'
  | 'provider'
  | 'policy'
  | 'no-local-voice';

export type SpeechOutputStatus =
  | Readonly<{ state: 'unsupported'; reason: SpeechOutputUnsupportedReason }>
  | Readonly<{ state: 'not-ready'; reason: 'initializing' }>
  | Readonly<{ state: 'ready' }>
  | Readonly<{
      state: 'temporarily-unavailable';
      reason: 'busy' | 'foreground' | 'provider';
    }>;

export type SpeechOutputErrorCode =
  | 'unsupported'
  | 'not_ready'
  | 'no_local_voice'
  | 'busy'
  | 'foreground_required'
  | 'cancelled'
  | 'synthesis_failed'
  | 'invalid_request'
  | 'internal';

const ERROR_CODES = new Set<SpeechOutputErrorCode>([
  'unsupported',
  'not_ready',
  'no_local_voice',
  'busy',
  'foreground_required',
  'cancelled',
  'synthesis_failed',
  'invalid_request',
  'internal',
]);

const ERROR_MESSAGES: Record<SpeechOutputErrorCode, string> = {
  unsupported: 'Local speech output is not supported',
  not_ready: 'Local speech output is not ready',
  no_local_voice: 'No local speech voice is installed',
  busy: 'Local speech output is busy',
  foreground_required: 'Local speech output requires the foreground',
  cancelled: 'Speech output was cancelled',
  synthesis_failed: 'Local speech synthesis failed',
  invalid_request: 'Invalid speech output request',
  internal: 'Local speech output failed',
};

export function isSpeechOutputErrorCode(value: unknown): value is SpeechOutputErrorCode {
  return typeof value === 'string' && ERROR_CODES.has(value as SpeechOutputErrorCode);
}

export class SpeechOutputError extends Error {
  readonly code: SpeechOutputErrorCode;
  readonly recoverable: boolean;

  constructor(code: SpeechOutputErrorCode, options: { recoverable?: boolean } = {}) {
    super(ERROR_MESSAGES[code]);
    this.name = 'SpeechOutputError';
    this.code = code;
    this.recoverable = options.recoverable
      ?? !['unsupported', 'no_local_voice', 'invalid_request'].includes(code);
  }
}

export type SpeechOutputStatusOptions = Readonly<{ signal?: AbortSignal }>;

export type SpeechOutputSpeakOptions = Readonly<{
  text: string;
  language?: string;
  signal?: AbortSignal;
}>;

export type SpeechOutputResult = Readonly<{ completed: true }>;

export interface SpeechOutputCapability {
  status(options?: SpeechOutputStatusOptions): Promise<SpeechOutputStatus>;
  speak(options: SpeechOutputSpeakOptions): Promise<SpeechOutputResult>;
  stop(): Promise<void>;
  invalidate(): Promise<void>;
}

export function validateSpeechOutputSpeakOptions(options: SpeechOutputSpeakOptions): void {
  if (
    !options
    || typeof options !== 'object'
    || typeof options.text !== 'string'
    || options.text.trim().length === 0
    || options.text.length > SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS
    || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/.test(options.text)
    || (
      options.language !== undefined
      && (
        typeof options.language !== 'string'
        || options.language.length === 0
        || options.language.length > 64
        || !/^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$/.test(options.language)
      )
    )
  ) {
    throw new SpeechOutputError('invalid_request', { recoverable: false });
  }
}
