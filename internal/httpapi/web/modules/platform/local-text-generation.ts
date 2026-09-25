export const LOCAL_TEXT_GENERATION_CAPABILITY = 'local-text-generation' as const;

export const LOCAL_TEXT_GENERATION_LIMITS = Object.freeze({
  requestIdCodeUnits: 128,
  inputCodeUnits: 32_768,
  instructionCodeUnits: 8_192,
  maximumOutputTokens: 256,
  outputCodeUnits: 65_536,
  providerModelCodeUnits: 128,
  retryAfterMs: 86_400_000,
});

export type LocalTextGenerationUnsupportedReason = 'os' | 'device' | 'provider' | 'policy';
export type LocalTextGenerationAction = 'download' | 'enable' | 'system-update';
export type LocalTextGenerationUnavailableReason =
  | 'initializing'
  | 'busy'
  | 'quota'
  | 'foreground'
  | 'storage'
  | 'provider';

export type LocalTextGenerationStatus =
  | { state: 'unsupported'; reason: LocalTextGenerationUnsupportedReason }
  | { state: 'action-required'; action: LocalTextGenerationAction }
  | { state: 'preparing'; downloadedBytes?: number; totalBytes?: number }
  | {
      state: 'ready';
      maximumOutputTokens: number;
      contextTokenLimit?: number;
      providerModel?: string;
    }
  | {
      state: 'temporarily-unavailable';
      reason: LocalTextGenerationUnavailableReason;
      retryAfterMs?: number;
    };

export type LocalTextGenerationErrorCode =
  | 'unsupported'
  | 'disabled'
  | 'not_ready'
  | 'download_failed'
  | 'foreground_required'
  | 'busy'
  | 'quota_exceeded'
  | 'insufficient_storage'
  | 'input_too_large'
  | 'invalid_request'
  | 'output_rejected'
  | 'cancelled'
  | 'internal';

const ERROR_CODES = new Set<LocalTextGenerationErrorCode>([
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
]);

const ERROR_MESSAGES: Record<LocalTextGenerationErrorCode, string> = {
  unsupported: 'Local text generation is not supported',
  disabled: 'Local text generation is disabled',
  not_ready: 'Local text generation is not ready',
  download_failed: 'Local text generation preparation failed',
  foreground_required: 'Local text generation requires the foreground',
  busy: 'Local text generation is busy',
  quota_exceeded: 'Local text generation quota is exhausted',
  insufficient_storage: 'Local text generation needs more storage',
  input_too_large: 'Local text generation input is too large',
  invalid_request: 'Invalid local text generation request',
  output_rejected: 'Local text generation output was rejected',
  cancelled: 'Local text generation was cancelled',
  internal: 'Local text generation failed',
};

const NON_RECOVERABLE_ERRORS = new Set<LocalTextGenerationErrorCode>([
  'unsupported',
  'input_too_large',
  'invalid_request',
  'output_rejected',
]);

export function isLocalTextGenerationErrorCode(value: unknown): value is LocalTextGenerationErrorCode {
  return typeof value === 'string' && ERROR_CODES.has(value as LocalTextGenerationErrorCode);
}

export class LocalTextGenerationError extends Error {
  readonly code: LocalTextGenerationErrorCode;
  readonly recoverable: boolean;
  readonly retryAfterMs?: number;

  constructor(
    code: LocalTextGenerationErrorCode,
    options: { recoverable?: boolean; retryAfterMs?: number } = {},
  ) {
    super(ERROR_MESSAGES[code]);
    this.name = 'LocalTextGenerationError';
    this.code = code;
    this.recoverable = options.recoverable ?? !NON_RECOVERABLE_ERRORS.has(code);
    if (
      Number.isSafeInteger(options.retryAfterMs) &&
      options.retryAfterMs! >= 0 &&
      options.retryAfterMs! <= LOCAL_TEXT_GENERATION_LIMITS.retryAfterMs
    ) {
      this.retryAfterMs = options.retryAfterMs;
    }
  }
}

export interface LocalTextGenerationStatusOptions {
  signal?: AbortSignal;
}

export interface LocalTextGenerationPrepareOptions {
  userInitiated: true;
  signal?: AbortSignal;
}

export interface LocalTextGenerationRequest {
  requestId: string;
  input: string;
  instructions: string;
  maximumOutputTokens: number;
  signal?: AbortSignal;
}

export interface LocalTextGenerationResult {
  requestId: string;
  text: string;
}

export interface LocalTextGenerationCapability {
  status(options?: LocalTextGenerationStatusOptions): Promise<LocalTextGenerationStatus>;
  prepare(options: LocalTextGenerationPrepareOptions): Promise<void>;
  generate(options: LocalTextGenerationRequest): Promise<LocalTextGenerationResult>;
}

function invalidRequest(): never {
  throw new LocalTextGenerationError('invalid_request', { recoverable: false });
}

export function validateLocalTextGenerationRequest(request: LocalTextGenerationRequest): void {
  if (
    typeof request !== 'object' ||
    request === null ||
    typeof request.requestId !== 'string' ||
    request.requestId.length === 0 ||
    request.requestId.length > LOCAL_TEXT_GENERATION_LIMITS.requestIdCodeUnits ||
    typeof request.input !== 'string' ||
    request.input.trim().length === 0 ||
    typeof request.instructions !== 'string' ||
    request.instructions.length > LOCAL_TEXT_GENERATION_LIMITS.instructionCodeUnits ||
    !Number.isInteger(request.maximumOutputTokens) ||
    request.maximumOutputTokens < 1 ||
    request.maximumOutputTokens > LOCAL_TEXT_GENERATION_LIMITS.maximumOutputTokens
  ) {
    invalidRequest();
  }
  if (request.input.length > LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits) {
    throw new LocalTextGenerationError('input_too_large', { recoverable: false });
  }
}

export function validateLocalTextGenerationOutput(text: unknown): asserts text is string {
  if (typeof text !== 'string' || text.length > LOCAL_TEXT_GENERATION_LIMITS.outputCodeUnits) {
    throw new LocalTextGenerationError('output_rejected', { recoverable: false });
  }
}
