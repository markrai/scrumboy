import type { CommandFailure } from './schema.js';

export type VoiceCommandInterpretation =
  | { kind: 'candidate'; command: string }
  | { kind: 'unsupported'; failure: CommandFailure };

export type VoiceCommandInterpretationOptions = {
  signal?: AbortSignal;
};

export interface VoiceCommandInterpreter {
  interpret(
    input: string,
    options?: VoiceCommandInterpretationOptions,
  ): Promise<VoiceCommandInterpretation>;
}
