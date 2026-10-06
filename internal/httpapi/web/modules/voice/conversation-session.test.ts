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
  operation: 'todo.update_title',
  slot: 'title',
  intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
  selection: {},
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

  it('keeps bound pending work when the active todo changes within the project', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.setActiveTodo(secondTodo);

    expect(session.getState().activeTodo).toEqual(secondTodo);
    expect(session.getState().activeProject).toEqual({
      projectId: secondTodo.projectId,
      projectSlug: secondTodo.projectSlug,
    });
    expect(session.getState().pending).toEqual(pendingTitle);
  });

  it('clears pending work when the active todo changes project', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.setActiveTodo(otherProjectTodo);

    expect(session.getState().activeTodo).toEqual(otherProjectTodo);
    expect(session.getState().pending).toBeNull();
  });

  it('keeps pending work when the canonical active todo reference is unchanged', () => {
    const session = createVoiceConversationSession();
    session.setActiveTodo(firstTodo);
    session.setPendingInteraction(pendingTitle);

    session.setActiveTodo({ ...firstTodo });

    expect(session.getState().pending).toEqual(pendingTitle);
  });

  it('defensively freezes bounded semantic clarification ownership', () => {
    const session = createVoiceConversationSession();
    const pending: VoicePendingInteraction = {
      kind: 'clarification',
      intent: {
        kind: 'move-todo',
        target: { kind: 'title', text: 'Bogus' },
        destination: { kind: 'name', text: 'done' },
      },
      choices: [
        {
          kind: 'todo',
          reference: firstTodo,
          title: 'Bogus',
          laneKey: 'backlog',
          laneName: 'Backlog',
        },
        {
          kind: 'todo',
          reference: secondTodo,
          title: 'Bogus',
          laneKey: 'doing',
          laneName: 'In Progress',
        },
      ],
      selection: {},
    };

    session.setPendingInteraction(pending);
    const stored = session.getState().pending;

    expect(stored).toEqual(pending);
    expect(Object.isFrozen(stored)).toBe(true);
    if (stored?.kind === 'clarification') {
      expect(Object.isFrozen(stored.intent)).toBe(true);
      expect(Object.isFrozen(stored.choices)).toBe(true);
      expect(Object.isFrozen(stored.choices[0])).toBe(true);
      expect(Object.isFrozen(stored.selection)).toBe(true);
    }
  });

  it('defensively freezes tag clarification references and selections', () => {
    const session = createVoiceConversationSession();
    const pending: VoicePendingInteraction = {
      kind: 'clarification',
      intent: {
        kind: 'add-todo-tag',
        target: { kind: 'title', text: 'Bogus' },
        tag: { kind: 'name', text: 'back' },
      },
      choices: [
        { kind: 'tag', name: 'backend' },
        { kind: 'tag', name: 'backlog' },
      ],
      selection: {
        tag: {
          selectedName: 'backend',
          allowedNames: ['backend', 'backlog'],
        },
      },
    };

    session.setPendingInteraction(pending);
    const stored = session.getState().pending;

    expect(stored).toEqual(pending);
    if (stored?.kind === 'clarification') {
      expect(Object.isFrozen(stored.intent)).toBe(true);
      expect(Object.isFrozen(stored.intent.kind === 'add-todo-tag' && stored.intent.tag)).toBe(true);
      expect(Object.isFrozen(stored.choices[0])).toBe(true);
      expect(Object.isFrozen(stored.selection.tag)).toBe(true);
      expect(Object.isFrozen(stored.selection.tag?.allowedNames)).toBe(true);
    }
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
