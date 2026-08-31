import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type {
  VoicePendingInteraction,
  VoiceTodoReference,
} from './conversation-state.js';
import { createVoiceConversationSession } from './conversation-session.js';

const firstTodo: VoiceTodoReference = {
  kind: 'todo',
  projectId: 1,
  projectSlug: 'alpha',
  localId: 553,
};

const secondTodo: VoiceTodoReference = {
  kind: 'todo',
  projectId: 1,
  projectSlug: 'alpha',
  localId: 619,
};

const otherProjectTodo: VoiceTodoReference = {
  kind: 'todo',
  projectId: 2,
  projectSlug: 'beta',
  localId: 1,
};

const pendingTitle: VoicePendingInteraction = {
  kind: 'missing-slot',
  action: 'todo.update_title',
  slot: 'title',
  target: firstTodo,
};

describe('VoiceFlow conversation session foundation', () => {
  it('starts as an empty, single-command, memory-only session', () => {
    const session = createVoiceConversationSession();

    expect(session.getState()).toEqual({
      activeProject: null,
      activeTodo: null,
      pending: null,
      lastInteraction: null,
      continuationEnabled: false,
    });
  });

  it('sets and replaces the active project-scoped todo reference', () => {
    const session = createVoiceConversationSession();

    session.setActiveTodo(firstTodo);
    expect(session.getState()).toMatchObject({
      activeProject: { projectId: 1, projectSlug: 'alpha' },
      activeTodo: firstTodo,
    });

    session.setActiveTodo(secondTodo);
    expect(session.getState().activeTodo).toEqual(secondTodo);
  });

  it('sets and clears one bounded pending interaction', () => {
    const session = createVoiceConversationSession();

    session.setPendingInteraction(pendingTitle);
    expect(session.getState().pending).toEqual(pendingTitle);

    session.clearPendingInteraction();
    expect(session.getState().pending).toBeNull();
  });

  it('clears the active todo and its pending work while preserving project context', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.clearActiveTodo();

    expect(session.getState()).toMatchObject({
      activeProject: { projectId: 1, projectSlug: 'alpha' },
      activeTodo: null,
      pending: null,
    });
  });

  it.each([
    ['another todo in the same project', secondTodo],
    ['a todo in another project', otherProjectTodo],
  ])('clears pending work when the active todo changes to %s', (_case, replacement) => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.setActiveTodo(replacement);

    expect(session.getState().activeTodo).toEqual(replacement);
    expect(session.getState().activeProject).toEqual({
      projectId: replacement.projectId,
      projectSlug: replacement.projectSlug,
    });
    expect(session.getState().pending).toBeNull();
  });

  it('keeps pending work when the canonical active todo reference is unchanged', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.setActiveTodo({ ...firstTodo });

    expect(session.getState().pending).toEqual(pendingTitle);
  });

  it('resets active context, pending work, semantic output, and continuation state', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);
    session.setLastInteraction({
      kind: 'question',
      message: {
        key: 'voice.conversation.titleQuestion',
        fallback: 'What would you like to change the title to?',
      },
      response: 'free-text',
    });
    session.setContinuationEnabled(true);

    session.reset();

    expect(session.getState()).toEqual({
      activeProject: null,
      activeTodo: null,
      pending: null,
      lastInteraction: null,
      continuationEnabled: false,
    });
  });

  it('does not persist state across sessions and disposal permanently empties a session', () => {
    const first = createVoiceConversationSession();
    first.setActiveTodo(firstTodo);
    first.dispose();

    const reopened = createVoiceConversationSession();
    expect(first.getState().activeTodo).toBeNull();
    expect(reopened.getState().activeTodo).toBeNull();
    expect(() => first.setActiveTodo(firstTodo)).toThrow(/disposed/i);
  });

  it('exposes state coordination only and keeps forbidden capabilities out of the foundation', () => {
    const session = createVoiceConversationSession();
    expect(Object.keys(session).sort()).toEqual([
      'clearActiveTodo',
      'clearPendingInteraction',
      'dispose',
      'getState',
      'reset',
      'setActiveProject',
      'setActiveTodo',
      'setContinuationEnabled',
      'setLastInteraction',
      'setPendingInteraction',
    ]);

    const foundation = [
      readFileSync(new URL('./conversation-state.ts', import.meta.url), 'utf8'),
      readFileSync(new URL('./conversation-session.ts', import.meta.url), 'utf8'),
      readFileSync(new URL('./semantic-interaction.ts', import.meta.url), 'utf8'),
    ].join('\n');

    expect(foundation).not.toMatch(
      /executeCommandIR|callMcpTool|ServerTransport|\bfetch\s*\(|localStorage|indexedDB|speechSynthesis|local-text-generation/i,
    );
  });
});
