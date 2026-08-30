import { interpretVoiceCommand } from './local-interpretation.js';
import { localizedCommandFailure } from './schema.js';
export function createLocalAiVoiceCommandInterpreter(dependencies) {
    const interpreter = {
        async interpret(input, options = {}) {
            const interpretation = await interpretVoiceCommand({
                transcript: input,
                capability: dependencies.capability,
                locale: dependencies.locale,
                signal: options.signal,
            });
            if (interpretation.kind === 'candidate')
                return interpretation;
            return {
                kind: 'unsupported',
                failure: localizedCommandFailure('unsupported', 'voice.errors.unsupportedCommand', 'Unsupported command.'),
            };
        },
    };
    return Object.freeze(interpreter);
}
