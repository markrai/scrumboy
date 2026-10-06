export const LOCAL_TEXT_GENERATION_CAPABILITY = 'local-text-generation';
export const LOCAL_TEXT_GENERATION_LIMITS = Object.freeze({
    requestIdCodeUnits: 128,
    inputCodeUnits: 32768,
    instructionCodeUnits: 8192,
    maximumOutputTokens: 256,
    outputCodeUnits: 65536,
    providerModelCodeUnits: 128,
    retryAfterMs: 86400000,
});
const ERROR_CODES = new Set([
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
const ERROR_MESSAGES = {
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
const NON_RECOVERABLE_ERRORS = new Set([
    'unsupported',
    'input_too_large',
    'invalid_request',
    'output_rejected',
]);
export function isLocalTextGenerationErrorCode(value) {
    return typeof value === 'string' && ERROR_CODES.has(value);
}
export class LocalTextGenerationError extends Error {
    constructor(code, options = {}) {
        super(ERROR_MESSAGES[code]);
        this.name = 'LocalTextGenerationError';
        this.code = code;
        this.recoverable = options.recoverable ?? !NON_RECOVERABLE_ERRORS.has(code);
        if (Number.isSafeInteger(options.retryAfterMs) &&
            options.retryAfterMs >= 0 &&
            options.retryAfterMs <= LOCAL_TEXT_GENERATION_LIMITS.retryAfterMs) {
            this.retryAfterMs = options.retryAfterMs;
        }
    }
}
function invalidRequest() {
    throw new LocalTextGenerationError('invalid_request', { recoverable: false });
}
export function validateLocalTextGenerationRequest(request) {
    if (typeof request !== 'object' ||
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
        request.maximumOutputTokens > LOCAL_TEXT_GENERATION_LIMITS.maximumOutputTokens) {
        invalidRequest();
    }
    if (request.input.length > LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits) {
        throw new LocalTextGenerationError('input_too_large', { recoverable: false });
    }
}
export function validateLocalTextGenerationOutput(text) {
    if (typeof text !== 'string' || text.length > LOCAL_TEXT_GENERATION_LIMITS.outputCodeUnits) {
        throw new LocalTextGenerationError('output_rejected', { recoverable: false });
    }
}
