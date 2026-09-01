export const SPEECH_OUTPUT_CAPABILITY = 'speech-output';
export const SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS = 600;
const ERROR_CODES = new Set([
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
const ERROR_MESSAGES = {
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
export function isSpeechOutputErrorCode(value) {
    return typeof value === 'string' && ERROR_CODES.has(value);
}
export class SpeechOutputError extends Error {
    constructor(code, options = {}) {
        super(ERROR_MESSAGES[code]);
        this.name = 'SpeechOutputError';
        this.code = code;
        this.recoverable = options.recoverable
            ?? !['unsupported', 'no_local_voice', 'invalid_request'].includes(code);
    }
}
export function validateSpeechOutputSpeakOptions(options) {
    if (!options
        || typeof options !== 'object'
        || typeof options.text !== 'string'
        || options.text.trim().length === 0
        || options.text.length > SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS
        || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/.test(options.text)
        || (options.language !== undefined
            && (typeof options.language !== 'string'
                || options.language.length === 0
                || options.language.length > 64
                || !/^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$/.test(options.language)))) {
        throw new SpeechOutputError('invalid_request', { recoverable: false });
    }
}
