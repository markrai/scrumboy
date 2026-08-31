import type { CommandFailure } from './schema.js';
import type { VoiceCurrentTodoTarget } from './conversation-state.js';

export type VoiceConversationIntent =
  | Readonly<{
      kind: 'open-todo';
      target: VoiceCurrentTodoTarget;
    }>
  | Readonly<{
      kind: 'update-todo-title';
      target: VoiceCurrentTodoTarget;
      title: string | null;
    }>;

export type VoiceInterpreterConversationContext = Readonly<{
  pending: null | Readonly<{
    action: 'todo.update_title';
    slot: 'title';
  }>;
}>;

export type VoiceCommandInterpretation =
  | { kind: 'candidate'; command: string }
  | { kind: 'conversation'; intent: VoiceConversationIntent }
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
