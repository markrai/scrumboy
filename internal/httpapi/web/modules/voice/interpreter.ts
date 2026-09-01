import type { CommandFailure } from './schema.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import type { VoiceDialogueIntent, VoiceDialogueOperation, VoicePendingSlot } from './dialogue-intent.js';

export type VoiceInterpreterConversationContext = Readonly<{
  pending:
    | null
    | Readonly<{ kind: 'todo-choice' }>
    | Readonly<{ kind: 'member-choice' }>
    | Readonly<{ kind: 'tag-choice' }>
    | (VoicePendingSlot & Readonly<{ kind: 'missing-slot' }>)
    | Readonly<{
        kind: 'confirmation';
        operation: VoiceDialogueOperation;
      }>;
}>;

export type VoiceCommandInterpretation =
  | { kind: 'candidate'; command: string }
  | { kind: 'semantic'; intent: VoiceSemanticIntent }
  | { kind: 'dialogue'; intent: VoiceDialogueIntent }
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
