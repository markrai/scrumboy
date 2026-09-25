import type {
  VoiceSemanticIntent,
  VoiceSemanticMemberReference,
  VoiceSemanticTagReference,
} from './semantic-intent.js';

export type VoiceDialogueOperation =
  | 'todo.create'
  | 'todo.open'
  | 'todo.move'
  | 'todo.assign'
  | 'todo.unassign'
  | 'todo.delete'
  | 'todo.update_title'
  | 'todo.append_notes'
  | 'todo.replace_notes'
  | 'todo.add_tag'
  | 'todo.remove_tag'
  | 'todo.inspect'
  | 'todo.count_completed';

export type VoicePendingSlot =
  | Readonly<{ operation: 'todo.create'; slot: 'title' }>
  | Readonly<{ operation: 'todo.move'; slot: 'destination' }>
  | Readonly<{ operation: 'todo.assign'; slot: 'assignee' }>
  | Readonly<{ operation: 'todo.update_title'; slot: 'title' }>
  | Readonly<{ operation: 'todo.append_notes'; slot: 'notes' }>
  | Readonly<{ operation: 'todo.replace_notes'; slot: 'notes' }>
  | Readonly<{ operation: 'todo.add_tag'; slot: 'tag' }>
  | Readonly<{ operation: 'todo.remove_tag'; slot: 'tag' }>;

export type VoiceTodoChoiceSelector =
  | Readonly<{ kind: 'local-id'; localId: number }>
  | Readonly<{ kind: 'lane'; text: string }>
  | Readonly<{ kind: 'ordinal'; index: number }>;

export type VoiceDialogueChoiceSelector =
  | VoiceTodoChoiceSelector
  | Readonly<{ kind: 'text'; text: string }>
  | Readonly<{ kind: 'email'; text: string }>;

export type VoiceProvideSlotDialogueIntent =
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.create';
      slot: 'title';
      value: string;
    }>
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.move';
      slot: 'destination';
      value: Readonly<{ kind: 'name'; text: string }>;
    }>
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.assign';
      slot: 'assignee';
      value: VoiceSemanticMemberReference;
    }>
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.update_title';
      slot: 'title';
      value: string;
    }>
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.append_notes' | 'todo.replace_notes';
      slot: 'notes';
      value: string;
    }>
  | Readonly<{
      kind: 'provide-slot';
      operation: 'todo.add_tag' | 'todo.remove_tag';
      slot: 'tag';
      value: VoiceSemanticTagReference;
    }>;

export type VoiceCorrectionDialogueIntent =
  | Readonly<{
      kind: 'correct-choice';
      selector: VoiceTodoChoiceSelector;
    }>
  | Readonly<{
      kind: 'correct-value';
      operation: 'todo.create' | 'todo.update_title';
      slot: 'title';
      value: string;
    }>
  | Readonly<{
      kind: 'correct-value';
      operation: 'todo.append_notes' | 'todo.replace_notes';
      slot: 'notes';
      value: string;
    }>;

export type VoiceDialogueIntent =
  | Readonly<{ kind: 'confirm' }>
  | Readonly<{ kind: 'decline' }>
  | Readonly<{ kind: 'cancel' }>
  | Readonly<{
      kind: 'select-choice';
      selector: VoiceDialogueChoiceSelector;
    }>
  | VoiceProvideSlotDialogueIntent
  | VoiceCorrectionDialogueIntent;

export type VoiceTurnIntent = VoiceSemanticIntent | VoiceDialogueIntent;

export function voiceSemanticOperation(intent: VoiceSemanticIntent): VoiceDialogueOperation {
  switch (intent.kind) {
    case 'create-todo': return 'todo.create';
    case 'open-todo': return 'todo.open';
    case 'move-todo': return 'todo.move';
    case 'assign-todo': return 'todo.assign';
    case 'unassign-todo': return 'todo.unassign';
    case 'delete-todo': return 'todo.delete';
    case 'update-todo-title': return 'todo.update_title';
    case 'append-todo-notes': return 'todo.append_notes';
    case 'replace-todo-notes': return 'todo.replace_notes';
    case 'add-todo-tag': return 'todo.add_tag';
    case 'remove-todo-tag': return 'todo.remove_tag';
    case 'inspect-todo': return 'todo.inspect';
    case 'count-completed-todos': return 'todo.count_completed';
  }
}

export function isVoiceDialogueIntent(intent: VoiceTurnIntent): intent is VoiceDialogueIntent {
  return [
    'confirm',
    'decline',
    'cancel',
    'select-choice',
    'provide-slot',
    'correct-choice',
    'correct-value',
  ].includes(intent.kind);
}
