import { describe, expect, it } from 'vitest';
import type { VoicePendingClarification } from './conversation-state.js';
import { resolvePendingClarificationSelector } from './dialogue-resolver.js';

const todoPending: VoicePendingClarification = {
  kind: 'clarification',
  intent: {
    kind: 'move-todo',
    target: { kind: 'title', text: 'Bogus' },
    destination: { kind: 'name', text: 'Done' },
  },
  choices: [
    {
      kind: 'todo',
      reference: { kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 353 },
      title: 'Bogus',
      laneKey: 'done',
      laneName: 'Done',
    },
    {
      kind: 'todo',
      reference: { kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 354 },
      title: 'Bogus',
      laneKey: 'backlog',
      laneName: 'Backlog',
    },
  ],
  selection: {},
};

describe('bounded dialogue choice resolution', () => {
  it.each([
    [{ kind: 'local-id', localId: 353 } as const, 353],
    [{ kind: 'lane', text: 'backlog' } as const, 354],
    [{ kind: 'ordinal', index: 2 } as const, 354],
  ])('selects a todo only from the authoritative offered set', (selector, localId) => {
    expect(resolvePendingClarificationSelector(todoPending, selector)).toMatchObject({
      kind: 'selected',
      selection: { todo: { selectedLocalId: localId, allowedLocalIds: [353, 354] } },
    });
  });

  it('does not select a non-offered local ID or board-wide lane', () => {
    expect(resolvePendingClarificationSelector(
      todoPending,
      { kind: 'local-id', localId: 999 },
    )).toEqual({ kind: 'no-match' });
    expect(resolvePendingClarificationSelector(
      todoPending,
      { kind: 'lane', text: 'In Progress' },
    )).toEqual({ kind: 'no-match' });
    expect(resolvePendingClarificationSelector(
      todoPending,
      { kind: 'ordinal', index: 3 },
    )).toEqual({ kind: 'no-match' });
  });

  it('resolves member names, emails, and ordinals only among offered members', () => {
    const pending: VoicePendingClarification = {
      kind: 'clarification',
      intent: {
        kind: 'assign-todo',
        target: { kind: 'local-id', localId: 353 },
        assignee: { kind: 'name', text: 'Mark' },
      },
      choices: [
        { kind: 'member', userId: 7, name: 'Mark Rai', email: 'mark@example.com' },
        { kind: 'member', userId: 8, name: 'Ada Lovelace', email: 'ada@example.com' },
      ],
      selection: {},
    };
    expect(resolvePendingClarificationSelector(pending, { kind: 'text', text: 'Ada' }))
      .toMatchObject({ kind: 'selected', selection: { member: { selectedUserId: 8 } } });
    expect(resolvePendingClarificationSelector(pending, { kind: 'email', text: 'mark@example.com' }))
      .toMatchObject({ kind: 'selected', selection: { member: { selectedUserId: 7 } } });
    expect(resolvePendingClarificationSelector(pending, { kind: 'ordinal', index: 2 }))
      .toMatchObject({ kind: 'selected', selection: { member: { selectedUserId: 8 } } });
    expect(resolvePendingClarificationSelector(pending, { kind: 'text', text: 'Grace' }))
      .toEqual({ kind: 'no-match' });
  });

  it('resolves tag text and ordinals only among offered tags', () => {
    const pending: VoicePendingClarification = {
      kind: 'clarification',
      intent: {
        kind: 'add-todo-tag',
        target: { kind: 'local-id', localId: 353 },
        tag: { kind: 'name', text: 'back' },
      },
      choices: [
        { kind: 'tag', name: 'backend' },
        { kind: 'tag', name: 'frontend' },
      ],
      selection: {},
    };
    expect(resolvePendingClarificationSelector(pending, { kind: 'text', text: 'backend' }))
      .toMatchObject({ kind: 'selected', selection: { tag: { selectedName: 'backend' } } });
    expect(resolvePendingClarificationSelector(pending, { kind: 'ordinal', index: 2 }))
      .toMatchObject({ kind: 'selected', selection: { tag: { selectedName: 'frontend' } } });
    expect(resolvePendingClarificationSelector(pending, { kind: 'text', text: 'security' }))
      .toEqual({ kind: 'no-match' });
  });
});
