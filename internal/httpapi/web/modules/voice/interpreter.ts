import type { CommandFailure } from './schema.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';

export type VoiceInterpreterConversationContext = Readonly<{
  pending: null | Readonly<{
    action: 'todo.update_title';
    slot: 'title';
  }>;
}>;

export type VoiceCommandInterpretation =
  | { kind: 'candidate'; command: string }
  | { kind: 'semantic'; intent: VoiceSemanticIntent }
  | { kind: 'unsupported'; failure: CommandFailure };

export type VoiceCommandInterpretationOptions = {
  signal?: AbortSignal;
  conversation?: VoiceInterpreterConversationContext;
};

export interface VoiceCommandInterpreter {
  interpret(
    input: string,
    options?: VoiceCommandInterpretationOptions,
  ): Promise<VoiceCommandInterpretation>;
}
