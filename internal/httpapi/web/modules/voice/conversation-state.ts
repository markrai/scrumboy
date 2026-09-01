import type { VoiceSemanticInteraction } from './semantic-interaction.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';

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

export type VoicePendingMissingSlot = Readonly<{
  kind: 'missing-slot';
  action: 'todo.update_title';
  slot: 'title';
  target: VoiceTodoReference;
}>;

export type VoiceTodoClarificationChoice = Readonly<{
  kind: 'todo';
  reference: VoiceTodoReference;
  title: string;
  laneKey: string;
  laneName: string;
}>;

export type VoiceMemberClarificationChoice = Readonly<{
  kind: 'member';
  userId: number;
  name: string;
  email: string;
}>;

export type VoiceTagClarificationChoice = Readonly<{
  kind: 'tag';
  name: string;
}>;

export type VoiceClarificationChoice =
  | VoiceTodoClarificationChoice
  | VoiceMemberClarificationChoice
  | VoiceTagClarificationChoice;

export type VoiceConversationSelection = Readonly<{
  todo?: Readonly<{
    selectedLocalId: number;
    allowedLocalIds: readonly number[];
  }>;
  member?: Readonly<{
    selectedUserId: number;
    allowedUserIds: readonly number[];
  }>;
  tag?: Readonly<{
    selectedName: string;
    allowedNames: readonly string[];
  }>;
}>;

export type VoicePendingClarification = Readonly<{
  kind: 'clarification';
  intent: VoiceSemanticIntent;
  choices: readonly VoiceClarificationChoice[];
  selection: VoiceConversationSelection;
}>;

export type VoicePendingInteraction = VoicePendingMissingSlot | VoicePendingClarification;

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
