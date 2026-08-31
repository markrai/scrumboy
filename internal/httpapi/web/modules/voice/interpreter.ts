import type { CommandFailure } from './schema.js';
import type { VoiceCurrentTodoTarget } from './conversation-state.js';

export type VoiceConversationIntent = Readonly<{
  kind: 'open-todo';
  target: VoiceCurrentTodoTarget;
}>;

export type VoiceCommandInterpretation =
  | { kind: 'candidate'; command: string }
  | { kind: 'conversation'; intent: VoiceConversationIntent }
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
