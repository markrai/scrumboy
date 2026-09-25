import type { Board, Todo } from '../types.js';
import type {
  VoiceConversationState,
  VoiceConversationTodoTarget,
  VoiceTodoReference,
} from './conversation-state.js';
import type { CommandIR } from './schema.js';

export type VoiceConversationResolveContext = Readonly<{
  projectId: number;
  projectSlug: string;
  board: Board;
}>;

export type VoiceConversationTodoResolution =
  | Readonly<{
      ok: true;
      value: Readonly<{
        reference: VoiceTodoReference;
        todo: Todo;
      }>;
    }>
  | Readonly<{
      ok: false;
      reason: 'missing_information';
      code: 'no_active_todo';
    }>
  | Readonly<{
      ok: false;
      reason: 'stale_context';
      code: 'project_mismatch' | 'todo_missing';
    }>;

export type VoiceActiveTodoTransition =
  | Readonly<{ kind: 'set'; reference: VoiceTodoReference }>
  | Readonly<{ kind: 'clear' }>
  | Readonly<{ kind: 'preserve' }>;

function sameTodoReference(
  left: VoiceTodoReference | null,
  right: VoiceTodoReference,
): boolean {
  return left?.projectId === right.projectId
    && left.projectSlug === right.projectSlug
    && left.localId === right.localId;
}

function todoReference(projectId: number, projectSlug: string, localId: number): VoiceTodoReference {
  return Object.freeze({ kind: 'todo', projectId, projectSlug, localId });
}

function findTodo(board: Board, localId: number): Todo | null {
  for (const todos of Object.values(board.columns ?? {})) {
    const todo = todos.find((candidate) => candidate.localId === localId);
    if (todo) return todo;
  }
  return null;
}

export function todoReferenceFromResolvedIR(ir: CommandIR): VoiceTodoReference | null {
  switch (ir.intent) {
    case 'todos.create':
      return null;
    case 'todos.move':
    case 'todos.delete':
    case 'todos.assign':
    case 'todos.unassign':
    case 'todos.update_title':
    case 'todos.append_notes':
    case 'todos.replace_notes':
    case 'todos.add_tag':
    case 'todos.remove_tag':
    case 'open_todo':
      return todoReference(ir.projectId, ir.projectSlug, ir.entities.localId);
  }
}

export function activeTodoTransitionAfterSuccessfulIR(
  ir: CommandIR,
  current: VoiceTodoReference | null,
): VoiceActiveTodoTransition {
  const reference = todoReferenceFromResolvedIR(ir);
  if (!reference) return Object.freeze({ kind: 'preserve' });
  if (ir.intent === 'todos.delete') {
    return Object.freeze(sameTodoReference(current, reference)
      ? { kind: 'clear' }
      : { kind: 'preserve' });
  }
  return Object.freeze({ kind: 'set', reference });
}

export function resolveConversationTodoTarget(
  target: VoiceConversationTodoTarget,
  state: Pick<VoiceConversationState, 'activeTodo'>,
  context: VoiceConversationResolveContext,
): VoiceConversationTodoResolution {
  const reference = target.kind === 'current' ? state.activeTodo : target;
  if (!reference) {
    return Object.freeze({
      ok: false,
      reason: 'missing_information',
      code: 'no_active_todo',
    });
  }
  if (
    reference.projectId !== context.projectId
    || reference.projectSlug !== context.projectSlug
  ) {
    return Object.freeze({
      ok: false,
      reason: 'stale_context',
      code: 'project_mismatch',
    });
  }
  const todo = findTodo(context.board, reference.localId);
  if (!todo) {
    return Object.freeze({
      ok: false,
      reason: 'stale_context',
      code: 'todo_missing',
    });
  }
  return Object.freeze({
    ok: true,
    value: Object.freeze({
      reference: todoReference(context.projectId, context.projectSlug, todo.localId),
      todo,
    }),
  });
}
