import type { VoiceSemanticInteraction } from './semantic-interaction.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import type { VoiceDialogueOperation } from './dialogue-intent.js';

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

export type VoicePendingMissingSlot =
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.create';
      slot: 'title';
      intent: Extract<VoiceSemanticIntent, { kind: 'create-todo' }>;
      selection: VoiceConversationSelection;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.move';
      slot: 'destination';
      intent: Extract<VoiceSemanticIntent, { kind: 'move-todo' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.assign';
      slot: 'assignee';
      intent: Extract<VoiceSemanticIntent, { kind: 'assign-todo' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.update_title';
      slot: 'title';
      intent: Extract<VoiceSemanticIntent, { kind: 'update-todo-title' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.append_notes';
      slot: 'notes';
      intent: Extract<VoiceSemanticIntent, { kind: 'append-todo-notes' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.replace_notes';
      slot: 'notes';
      intent: Extract<VoiceSemanticIntent, { kind: 'replace-todo-notes' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.add_tag';
      slot: 'tag';
      intent: Extract<VoiceSemanticIntent, { kind: 'add-todo-tag' }>;
      selection: VoiceConversationSelection;
      target: VoiceTodoReference;
    }>
  | Readonly<{
      kind: 'missing-slot';
      operation: 'todo.remove_tag';
      slot: 'tag';
      intent: Extract<VoiceSemanticIntent, { kind: 'remove-todo-tag' }>;
      selection: VoiceConversationSelection;
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

export type VoicePendingConfirmation = Readonly<{
  kind: 'confirmation';
  operation: VoiceDialogueOperation;
}>;

export type VoicePendingInteraction =
  | VoicePendingMissingSlot
  | VoicePendingClarification
  | VoicePendingConfirmation;

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
