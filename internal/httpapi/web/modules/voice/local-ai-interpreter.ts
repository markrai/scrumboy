import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import type { VoiceCommandInterpreter } from './interpreter.js';
import { interpretVoiceCommand } from './local-interpretation.js';
import { localizedCommandFailure } from './schema.js';

export type LocalAiVoiceCommandInterpreterDependencies = {
  capability: LocalTextGenerationCapability;
  locale: string;
};

export function createLocalAiVoiceCommandInterpreter(
  dependencies: LocalAiVoiceCommandInterpreterDependencies,
): VoiceCommandInterpreter {
  const interpreter: VoiceCommandInterpreter = {
    async interpret(input, options = {}) {
      const interpretation = await interpretVoiceCommand({
        transcript: input,
        capability: dependencies.capability,
        locale: dependencies.locale,
        signal: options.signal,
        conversation: options.conversation,
      });
      if (interpretation.kind !== 'refused') return interpretation;
      return {
        kind: 'unsupported',
        failure: localizedCommandFailure(
          'unsupported',
          'voice.errors.unsupportedCommand',
          'Unsupported command.',
        ),
      };
    },
  };
  return Object.freeze(interpreter);
}
