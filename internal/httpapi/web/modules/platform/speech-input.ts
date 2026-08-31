export const SPEECH_INPUT_CAPABILITY = 'speech-input' as const;

export const SPEECH_INPUT_MAX_DURATION_MS = 10_000;

export type SpeechInputUnsupportedReason = 'os' | 'device' | 'provider' | 'policy';
export type SpeechInputUnavailableReason = 'busy' | 'foreground' | 'provider';

export type SpeechInputStatus =
  | { state: 'unsupported'; reason: SpeechInputUnsupportedReason }
  | { state: 'ready' }
  | { state: 'temporarily-unavailable'; reason: SpeechInputUnavailableReason };

export type SpeechInputErrorCode =
  | 'unsupported'
  | 'not_ready'
  | 'permission_denied'
  | 'permission_denied_permanently'
  | 'busy'
  | 'foreground_required'
  | 'no_speech'
  | 'timeout'
  | 'cancelled'
  | 'recognition_failed'
  | 'invalid_request'
  | 'internal';

const ERROR_CODES = new Set<SpeechInputErrorCode>([
  'unsupported',
  'not_ready',
  'permission_denied',
  'permission_denied_permanently',
  'busy',
  'foreground_required',
  'no_speech',
  'timeout',
  'cancelled',
  'recognition_failed',
  'invalid_request',
  'internal',
]);

const ERROR_MESSAGES: Record<SpeechInputErrorCode, string> = {
  unsupported: 'On-device speech input is not supported',
  not_ready: 'On-device speech input is not ready',
  permission_denied: 'Microphone permission was denied',
  permission_denied_permanently: 'Microphone permission is blocked',
  busy: 'On-device speech input is busy',
  foreground_required: 'On-device speech input requires the foreground',
  no_speech: 'No speech was recognized',
  timeout: 'Listening timed out',
  cancelled: 'Listening was cancelled',
  recognition_failed: 'On-device speech recognition failed',
  invalid_request: 'Invalid speech input request',
  internal: 'On-device speech input failed',
};

const NON_RECOVERABLE = new Set<SpeechInputErrorCode>([
  'unsupported',
  'permission_denied_permanently',
  'invalid_request',
]);

export function isSpeechInputErrorCode(value: unknown): value is SpeechInputErrorCode {
  return typeof value === 'string' && ERROR_CODES.has(value as SpeechInputErrorCode);
}

export class SpeechInputError extends Error {
  readonly code: SpeechInputErrorCode;
  readonly recoverable: boolean;

  constructor(code: SpeechInputErrorCode, options: { recoverable?: boolean } = {}) {
    super(ERROR_MESSAGES[code]);
    this.name = 'SpeechInputError';
    this.code = code;
    this.recoverable = options.recoverable ?? !NON_RECOVERABLE.has(code);
  }
}

export type SpeechInputStatusOptions = Readonly<{
  signal?: AbortSignal;
}>;

export type SpeechInputListenOptions = Readonly<{
  maxDurationMs: number;
  language?: string;
  signal?: AbortSignal;
  onListening?: () => void;
}>;

export type SpeechInputResult = Readonly<{
  transcript: string;
}>;

export interface SpeechInputCapability {
  status(options?: SpeechInputStatusOptions): Promise<SpeechInputStatus>;
  listen(options: SpeechInputListenOptions): Promise<SpeechInputResult>;
}

export function validateSpeechInputListenOptions(options: SpeechInputListenOptions): void {
  if (
    !options
    || typeof options !== 'object'
    || !Number.isInteger(options.maxDurationMs)
    || options.maxDurationMs < 1
    || options.maxDurationMs > SPEECH_INPUT_MAX_DURATION_MS
    || (options.onListening !== undefined && typeof options.onListening !== 'function')
    || (
      options.language !== undefined
      && (
        typeof options.language !== 'string'
        || options.language.length === 0
        || options.language.length > 64
        || !/^[A-Za-z0-9-]+$/.test(options.language)
      )
    )
  ) {
    throw new SpeechInputError('invalid_request', { recoverable: false });
  }
}

export function validateSpeechInputResult(value: unknown): asserts value is SpeechInputResult {
  if (
    !value
    || typeof value !== 'object'
    || Object.keys(value).length !== 1
    || typeof (value as { transcript?: unknown }).transcript !== 'string'
    || (value as { transcript: string }).transcript.trim().length === 0
    || (value as { transcript: string }).transcript.length > 260
  ) {
    throw new SpeechInputError('recognition_failed');
  }
}
