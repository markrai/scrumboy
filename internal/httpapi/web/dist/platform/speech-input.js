export const SPEECH_INPUT_CAPABILITY = 'speech-input';
export const SPEECH_INPUT_MAX_DURATION_MS = 10000;
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
export function isSpeechInputErrorCode(value) {
    return typeof value === 'string' && ERROR_CODES.has(value);
}
export class SpeechInputError extends Error {
    constructor(code, options = {}) {
        super(ERROR_MESSAGES[code]);
        this.name = 'SpeechInputError';
        this.code = code;
        this.recoverable = options.recoverable ?? !NON_RECOVERABLE.has(code);
    }
}
export function validateSpeechInputListenOptions(options) {
    if (!options
        || typeof options !== 'object'
        || !Number.isInteger(options.maxDurationMs)
        || options.maxDurationMs < 1
        || options.maxDurationMs > SPEECH_INPUT_MAX_DURATION_MS
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
    if (!value
        || typeof value !== 'object'
        || Object.keys(value).length !== 1
        || typeof value.transcript !== 'string'
        || value.transcript.trim().length === 0
        || value.transcript.length > 260) {
        throw new SpeechInputError('recognition_failed');
    }
}
