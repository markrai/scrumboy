export type VoiceSemanticTodoReference =
  | Readonly<{
      kind: 'current';
    }>
  | Readonly<{
      kind: 'local-id';
      localId: number;
    }>
  | Readonly<{
      kind: 'title';
      text: string;
    }>;

export type VoiceSemanticLaneReference = Readonly<{
  kind: 'name';
  text: string;
}>;

export type VoiceSemanticMemberReference =
  | Readonly<{
      kind: 'name';
      text: string;
    }>
  | Readonly<{
      kind: 'email';
      text: string;
    }>;

export type VoiceSemanticTagReference = Readonly<{
  kind: 'name';
  text: string;
}>;

export type VoiceSemanticTodoInspectionAspect =
  | 'summary'
  | 'assignee'
  | 'lane'
  | 'tags'
  | 'notes';

export type VoiceSemanticIntent =
  | Readonly<{
      kind: 'create-todo';
      title: string;
    }>
  | Readonly<{
      kind: 'open-todo';
      target: VoiceSemanticTodoReference;
    }>
  | Readonly<{
      kind: 'move-todo';
      target: VoiceSemanticTodoReference;
      destination: VoiceSemanticLaneReference;
    }>
  | Readonly<{
      kind: 'assign-todo';
      target: VoiceSemanticTodoReference;
      assignee: VoiceSemanticMemberReference;
    }>
  | Readonly<{
      kind: 'delete-todo';
      target: VoiceSemanticTodoReference;
    }>
  | Readonly<{
      kind: 'update-todo-title';
      target: VoiceSemanticTodoReference;
      title: string | null;
    }>
  | Readonly<{
      kind: 'append-todo-notes';
      target: VoiceSemanticTodoReference;
      notes: string;
    }>
  | Readonly<{
      kind: 'replace-todo-notes';
      target: VoiceSemanticTodoReference;
      notes: string;
    }>
  | Readonly<{
      kind: 'add-todo-tag';
      target: VoiceSemanticTodoReference;
      tag: VoiceSemanticTagReference;
    }>
  | Readonly<{
      kind: 'remove-todo-tag';
      target: VoiceSemanticTodoReference;
      tag: VoiceSemanticTagReference;
    }>
  | Readonly<{
      kind: 'unassign-todo';
      target: VoiceSemanticTodoReference;
      assignee?: VoiceSemanticMemberReference;
    }>
  | Readonly<{
      kind: 'inspect-todo';
      target: VoiceSemanticTodoReference;
      aspect: VoiceSemanticTodoInspectionAspect;
    }>
  | Readonly<{
      kind: 'count-completed-todos';
      period: Readonly<{ kind: 'this-week' }>;
    }>;

export function isVoiceSemanticMutationIntent(intent: VoiceSemanticIntent): boolean {
  return intent.kind !== 'open-todo'
    && intent.kind !== 'inspect-todo'
    && intent.kind !== 'count-completed-todos';
}
