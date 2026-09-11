export const SPEECH_INPUT_CAPABILITY = 'speech-input';
export const SPEECH_INPUT_MAX_DURATION_MS = 10000;
// Capability request bound, not the default duration chosen by existing callers.
export const SPEECH_INPUT_DURATION_CEILING_MS = 45000;
export const SPEECH_INPUT_MAX_TRANSCRIPT_CODE_UNITS = 2000;
const ERROR_CODES = new Set([
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
const ERROR_MESSAGES = {
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
const NON_RECOVERABLE = new Set([
    'unsupported',
    'permission_denied_permanently',
    'invalid_request',
]);
const PROVIDER_REASONS = new Set([
    'audio',
    'client',
    'network',
    'network_timeout',
    'server',
    'server_disconnected',
    'recognizer_busy',
    'too_many_requests',
    'no_match',
    'speech_timeout',
    'language_not_supported',
    'language_unavailable',
    'unknown',
]);
export function isSpeechInputProviderReason(value) {
    return typeof value === 'string' && PROVIDER_REASONS.has(value);
}
export function isSpeechInputProviderCode(value) {
    return Number.isInteger(value) && value >= 0 && value <= 65535;
}
export function isSpeechInputErrorCode(value) {
    return typeof value === 'string' && ERROR_CODES.has(value);
}
export class SpeechInputError extends Error {
    constructor(code, options = {}) {
        super(ERROR_MESSAGES[code]);
        this.name = 'SpeechInputError';
        this.code = code;
        this.recoverable = options.recoverable ?? !NON_RECOVERABLE.has(code);
        if (isSpeechInputProviderCode(options.providerCode))
            this.providerCode = options.providerCode;
        if (isSpeechInputProviderReason(options.providerReason))
            this.providerReason = options.providerReason;
    }
}
const CAPTURE_CONTEXTS = new Set([
    'initial_create_capture',
    'member_clarification_capture',
    'tag_suggestion_capture',
    'binary_clarification_capture',
    'final_confirmation_capture',
]);
export function validateSpeechInputListenOptions(options) {
    if (!options
        || typeof options !== 'object'
        || !Number.isInteger(options.maxDurationMs)
        || options.maxDurationMs < 1
        || options.maxDurationMs > SPEECH_INPUT_DURATION_CEILING_MS
        || (options.aggregationMode !== undefined && options.aggregationMode !== 'single' && options.aggregationMode !== 'create_v2')
        || (options.captureContext !== undefined && !CAPTURE_CONTEXTS.has(options.captureContext))
        || (options.postFinalGraceMs !== undefined && (!Number.isInteger(options.postFinalGraceMs) || options.postFinalGraceMs < 1 || options.postFinalGraceMs > 10000))
        || (options.onListening !== undefined && typeof options.onListening !== 'function')
        || (options.language !== undefined
            && (typeof options.language !== 'string'
                || options.language.length === 0
                || options.language.length > 64
                || !/^[A-Za-z0-9-]+$/.test(options.language)))) {
        throw new SpeechInputError('invalid_request', { recoverable: false });
    }
}
export function validateSpeechInputResult(value) {
    const keys = value && typeof value === 'object' ? Object.keys(value) : [];
    const allowedKeys = new Set(['transcript', 'segmentCount']);
    const hasSegmentCount = keys.includes('segmentCount');
    if (!value
        || typeof value !== 'object'
        || keys.some(key => !allowedKeys.has(key))
        || !keys.includes('transcript')
        || typeof value.transcript !== 'string'
        || (hasSegmentCount && (!Number.isInteger(value.segmentCount) || value.segmentCount < 1))
        || value.transcript.trim().length === 0
        || value.transcript.length > SPEECH_INPUT_MAX_TRANSCRIPT_CODE_UNITS) {
        throw new SpeechInputError('recognition_failed');
    }
}
