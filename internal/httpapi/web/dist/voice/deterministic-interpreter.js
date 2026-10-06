import { parseCommand } from './parser.js';
import { isCommandFailure } from './schema.js';
function throwIfAborted(signal) {
    if (signal?.aborted) {
        throw new DOMException('The operation was aborted.', 'AbortError');
    }
}
const deterministicVoiceCommandInterpreterImplementation = {
    async interpret(input, options = {}) {
        throwIfAborted(options.signal);
        const parsed = parseCommand(input);
        if (isCommandFailure(parsed)) {
            return { kind: 'unsupported', failure: parsed };
        }
        return { kind: 'candidate', command: parsed.value.display };
    },
};
export const deterministicVoiceCommandInterpreter = Object.freeze(deterministicVoiceCommandInterpreterImplementation);
