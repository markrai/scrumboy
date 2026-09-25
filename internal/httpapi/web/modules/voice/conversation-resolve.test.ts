import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type { Board, Todo } from '../types.js';
import type { VoiceConversationState, VoiceTodoReference } from './conversation-state.js';
import {
  activeTodoTransitionAfterSuccessfulIR,
  resolveConversationTodoTarget,
  todoReferenceFromResolvedIR,
} from './conversation-resolve.js';
import type { CommandIR } from './schema.js';

const activeTodo: VoiceTodoReference = {
  kind: 'todo',
  projectId: 1,
  projectSlug: 'alpha',
  localId: 553,
};

function board(todo: Todo | null = {
  id: 10,
  localId: 553,
  title: 'Fresh authoritative title',
  status: 'doing',
}): Board {
  return {
    project: { id: 1, name: 'Alpha', slug: 'alpha', dominantColor: '#123456' },
    tags: [],
    columns: { doing: todo ? [todo] : [] },
  };
}

function state(reference: VoiceTodoReference | null): Pick<VoiceConversationState, 'activeTodo'> {
  return { activeTodo: reference };
}

function context(overrides: Partial<{ projectId: number; projectSlug: string; board: Board }> = {}) {
  return {
    projectId: 1,
    projectSlug: 'alpha',
    board: board(),
    ...overrides,
  };
}

describe('VoiceFlow current-todo resolution', () => {
  it.each([
    [
      'open',
      { intent: 'open_todo', projectId: 1, projectSlug: 'alpha', entities: { localId: 553 } },
    ],
    [
      'move',
      { intent: 'todos.move', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, toColumnKey: 'done' } },
    ],
    [
      'assign',
      { intent: 'todos.assign', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, assigneeUserId: 7 } },
    ],
    [
      'title update',
      { intent: 'todos.update_title', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, title: 'New title' } },
    ],
    [
      'delete',
      { intent: 'todos.delete', projectId: 1, projectSlug: 'alpha', entities: { localId: 553 } },
    ],
  ] satisfies Array<[string, CommandIR]>)('derives a concrete project-scoped reference from resolved %s IR', (_label, ir) => {
    expect(todoReferenceFromResolvedIR(ir)).toEqual(activeTodo);
  });

  it('does not fabricate a todo identity for create', () => {
    expect(todoReferenceFromResolvedIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login', columnKey: 'todo' },
    })).toBeNull();
  });

  it('resolves current to fresh authoritative todo data on the matching project', () => {
    const freshTodo: Todo = {
      id: 10,
      localId: 553,
      title: 'Title changed after context was captured',
      status: 'done',
    };

    const resolved = resolveConversationTodoTarget(
      { kind: 'current' },
      state(activeTodo),
      context({ board: board(freshTodo) }),
    );

    expect(resolved).toEqual({
      ok: true,
      value: { reference: activeTodo, todo: freshTodo },
    });
    if (resolved.ok) expect(resolved.value.todo).toBe(freshTodo);
  });

  it('rejects project mismatch without searching for the same local ID in another project', () => {
    const betaBoard = board({
      id: 20,
      localId: 553,
      title: 'Unrelated beta todo',
      status: 'todo',
    });

    expect(resolveConversationTodoTarget(
      { kind: 'current' },
      state(activeTodo),
      context({ projectId: 2, projectSlug: 'beta', board: betaBoard }),
    )).toEqual({
      ok: false,
      reason: 'stale_context',
      code: 'project_mismatch',
    });
  });

  it('fails cleanly when there is no active todo', () => {
    expect(resolveConversationTodoTarget(
      { kind: 'current' },
      state(null),
      context(),
    )).toEqual({
      ok: false,
      reason: 'missing_information',
      code: 'no_active_todo',
    });
  });

  it('fails stale when the active todo disappeared from the current board', () => {
    expect(resolveConversationTodoTarget(
      { kind: 'current' },
      state(activeTodo),
      context({ board: board(null) }),
    )).toEqual({
      ok: false,
      reason: 'stale_context',
      code: 'todo_missing',
    });
  });

  it.each([
    [
      'open',
      { intent: 'open_todo', projectId: 1, projectSlug: 'alpha', entities: { localId: 553 } },
    ],
    [
      'move',
      { intent: 'todos.move', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, toColumnKey: 'done' } },
    ],
    [
      'assign',
      { intent: 'todos.assign', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, assigneeUserId: 7 } },
    ],
    [
      'title update',
      { intent: 'todos.update_title', projectId: 1, projectSlug: 'alpha', entities: { localId: 553, title: 'New title' } },
    ],
  ] satisfies Array<[string, CommandIR]>)('sets the active todo after successful %s', (_label, ir) => {
    expect(activeTodoTransitionAfterSuccessfulIR(ir, null)).toEqual({
      kind: 'set',
      reference: activeTodo,
    });
  });

  it('clears a successfully deleted active target and preserves a different active todo', () => {
    const deleteIR: CommandIR = {
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 553 },
    };
    expect(activeTodoTransitionAfterSuccessfulIR(deleteIR, activeTodo)).toEqual({ kind: 'clear' });
    expect(activeTodoTransitionAfterSuccessfulIR(deleteIR, {
      ...activeTodo,
      localId: 619,
    })).toEqual({ kind: 'preserve' });
  });

  it('preserves active context after create because no authoritative created ID is available', () => {
    expect(activeTodoTransitionAfterSuccessfulIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login', columnKey: 'todo' },
    }, activeTodo)).toEqual({ kind: 'preserve' });
  });

  it('keeps forbidden execution, transport, persistence, provider, and speech dependencies out', () => {
    const source = readFileSync(new URL('./conversation-resolve.ts', import.meta.url), 'utf8');
    expect(source).not.toMatch(
      /executeCommandIR|callMcpTool|ServerTransport|\bfetch\s*\(|localStorage|indexedDB|speechSynthesis|local-text-generation/i,
    );
  });
});
