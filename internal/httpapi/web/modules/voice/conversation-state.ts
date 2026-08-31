import type { VoiceSemanticInteraction } from './semantic-interaction.js';

export type VoiceProjectReference = Readonly<{
  projectId: number;
  projectSlug: string;
}>;

export type VoiceTodoReference = Readonly<{
  kind: 'todo';
  projectId: number;
  projectSlug: string;
  localId: number;
}>;

export type VoiceCurrentTodoTarget = Readonly<{
  kind: 'current';
}>;

export type VoiceConversationTodoTarget = VoiceTodoReference | VoiceCurrentTodoTarget;

export type VoicePendingInteraction = Readonly<{
  kind: 'missing-slot';
  action: 'todo.update_title';
  slot: 'title';
  target: VoiceTodoReference;
}>;

export type VoiceConversationState = Readonly<{
  activeProject: VoiceProjectReference | null;
  activeTodo: VoiceTodoReference | null;
  pending: VoicePendingInteraction | null;
  lastInteraction: VoiceSemanticInteraction | null;
  continuationEnabled: boolean;
}>;

export function createEmptyVoiceConversationState(): VoiceConversationState {
  return Object.freeze({
    activeProject: null,
    activeTodo: null,
    pending: null,
    lastInteraction: null,
    continuationEnabled: false,
  });
}
