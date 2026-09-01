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
    }>;

export function isVoiceSemanticMutationIntent(intent: VoiceSemanticIntent): boolean {
  return intent.kind !== 'open-todo';
}
