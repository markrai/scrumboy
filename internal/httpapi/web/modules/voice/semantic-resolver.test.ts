import { describe, expect, it } from 'vitest';
import type { Board, Todo } from '../types.js';
import type { BoardMember } from '../state/state.js';
import { createVoiceConversationSession } from './conversation-session.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import {
  resolveVoiceSemanticIntent,
  type VoiceSemanticResolveContext,
  type VoiceSemanticSelection,
} from './semantic-resolver.js';

const members: BoardMember[] = [
  { userId: 7, name: 'Mark Rai', email: 'mark.rai@example.com', role: 'maintainer' },
  { userId: 8, name: 'Ada Lovelace', email: 'ada@example.com', role: 'contributor' },
];

function todo(localId: number, title: string, status: string, assigneeUserId?: number): Todo {
  return { id: localId, localId, title, status, ...(assigneeUserId == null ? {} : { assigneeUserId }) };
}

function board(backlog: Todo[] = [], doing: Todo[] = [], done: Todo[] = []): Board {
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', dominantColor: '#123456' },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: { backlog, doing, done },
  };
}

function context(sourceBoard: Board, projectMembers: BoardMember[] = members): VoiceSemanticResolveContext {
  return {
    projectId: 1,
    projectSlug: 'alpha',
    board: sourceBoard,
    members: projectMembers,
  };
}

async function resolve(
  intent: VoiceSemanticIntent,
  sourceBoard: Board,
  selection: VoiceSemanticSelection = {},
  projectMembers: BoardMember[] = members,
) {
  const session = createVoiceConversationSession();
  session.setActiveProject({ projectId: 1, projectSlug: 'alpha' });
  return resolveVoiceSemanticIntent(intent, session.getState(), context(sourceBoard, projectMembers), selection);
}

describe('semantic VoiceFlow domain resolution', () => {
  it('uses the authoritative column key when the transport status is uppercase', async () => {
    const existing: Todo = {
      ...todo(355, 'Fixed Radical Login', 'DONE'),
      columnKey: 'done',
    };
    const result = await resolve({
      kind: 'move-todo',
      target: { kind: 'title', text: 'Fixed Radical Login' },
      destination: { kind: 'name', text: 'done' },
    }, board([], [], [existing]));

    expect(result).toMatchObject({
      ok: true,
      value: { kind: 'information' },
    });
  });

  it('resolves the flagship title move without a numeric ID in the intent', async () => {
    const result = await resolve({
      kind: 'move-todo',
      target: { kind: 'title', text: 'Fixed Radical Login' },
      destination: { kind: 'name', text: 'done' },
    }, board([todo(355, 'Fixed Radical Login', 'backlog')]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: {
            intent: 'todos.move',
            entities: { localId: 355, toColumnKey: 'done' },
          },
          requiresConfirmation: true,
        },
      },
    });
  });

  it('filters an already-satisfied duplicate before deciding move ambiguity', async () => {
    const result = await resolve({
      kind: 'move-todo',
      target: { kind: 'title', text: 'Bogus' },
      destination: { kind: 'name', text: 'done' },
    }, board([todo(351, 'Bogus', 'backlog')], [], [todo(352, 'Bogus', 'done')]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { ir: { entities: { localId: 351, toColumnKey: 'done' } } },
      },
    });
  });

  it('returns authoritative lane-qualified choices for genuinely actionable duplicates', async () => {
    const intent: VoiceSemanticIntent = {
      kind: 'move-todo',
      target: { kind: 'title', text: 'Bogus' },
      destination: { kind: 'name', text: 'done' },
    };
    const result = await resolve(
      intent,
      board([todo(351, 'Bogus', 'backlog')], [todo(352, 'Bogus', 'doing')]),
    );

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'clarification',
        interaction: {
          kind: 'clarification',
          options: [
            { id: 'todo:351', label: '#351 · Bogus · Backlog' },
            { id: 'todo:352', label: '#352 · Bogus · In Progress' },
          ],
        },
        choices: [
          { kind: 'todo', reference: { localId: 351 }, laneName: 'Backlog' },
          { kind: 'todo', reference: { localId: 352 }, laneName: 'In Progress' },
        ],
      },
    });
  });

  it('returns an informative no-op when all matching todos already satisfy the move', async () => {
    const result = await resolve({
      kind: 'move-todo',
      target: { kind: 'title', text: 'Bogus' },
      destination: { kind: 'name', text: 'done' },
    }, board([], [], [todo(351, 'Bogus', 'done'), todo(352, 'Bogus', 'done')]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'information',
        interaction: { message: { key: 'voice.info.alreadyInLane' } },
      },
    });
  });

  it.each([
    ['Mark Rai', 7],
    ['Mark', 7],
    ['mark.rai@example.com', 7],
  ])('resolves a deterministic unique member reference %s', async (text, userId) => {
    const result = await resolve({
      kind: 'assign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: text.includes('@') ? 'email' : 'name', text },
    }, board([todo(351, 'Bogus', 'backlog')]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { ir: { entities: { localId: 351, assigneeUserId: userId } } },
      },
    });
  });

  it('clarifies a genuinely ambiguous partial member name from authoritative members', async () => {
    const result = await resolve({
      kind: 'assign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark' },
    }, board([todo(351, 'Bogus', 'backlog')]), {}, [
      members[0],
      { userId: 9, name: 'Mark Smith', email: 'mark.smith@example.com', role: 'contributor' },
    ]);

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'clarification',
        interaction: {
          message: { key: 'voice.prompt.whichPerson' },
          options: [
            { id: 'member:7', label: 'Mark Rai · mark.rai@example.com' },
            { id: 'member:9', label: 'Mark Smith · mark.smith@example.com' },
          ],
        },
      },
    });
  });

  it('filters an already-assigned duplicate before deciding assignment ambiguity', async () => {
    const result = await resolve({
      kind: 'assign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark Rai' },
    }, board([
      todo(351, 'Bogus', 'backlog'),
      todo(352, 'Bogus', 'backlog', 7),
    ]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { ir: { entities: { localId: 351, assigneeUserId: 7 } } },
      },
    });
  });

  it('retains an authoritative member choice while a later todo clarification is resolved', async () => {
    const projectMembers = [
      members[0],
      { userId: 9, name: 'Mark Smith', email: 'mark.smith@example.com', role: 'contributor' },
    ];
    const sourceBoard = board([
      todo(351, 'Bogus', 'backlog'),
      todo(352, 'Bogus', 'backlog'),
    ]);
    const intent: VoiceSemanticIntent = {
      kind: 'assign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark' },
    };
    const memberSelection = {
      member: { selectedUserId: 7, allowedUserIds: [7, 9] },
    } as const;

    const afterMember = await resolve(intent, sourceBoard, memberSelection, projectMembers);
    expect(afterMember).toMatchObject({
      ok: true,
      value: {
        kind: 'clarification',
        selection: memberSelection,
        choices: [{ kind: 'todo' }, { kind: 'todo' }],
      },
    });

    const completed = await resolve(intent, sourceBoard, {
      ...memberSelection,
      todo: { selectedLocalId: 351, allowedLocalIds: [351, 352] },
    }, projectMembers);
    expect(completed).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { ir: { entities: { localId: 351, assigneeUserId: 7 } } },
      },
    });
  });

  it('rejects an offered member choice removed from the freshly listed project members', async () => {
    const session = createVoiceConversationSession();
    session.setActiveProject({ projectId: 1, projectSlug: 'alpha' });
    const result = await resolveVoiceSemanticIntent({
      kind: 'assign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark' },
    }, session.getState(), {
      ...context(board([todo(351, 'Bogus', 'backlog')]), [
        members[0],
        { userId: 9, name: 'Mark Smith', email: 'mark.smith@example.com', role: 'contributor' },
      ]),
      callTool: async () => ({ items: [members[1]] }),
    }, {
      member: { selectedUserId: 7, allowedUserIds: [7, 9] },
    });

    expect(result).toMatchObject({ ok: false, code: 'stale_context' });
  });

  it('revalidates an offered todo choice against fresh authoritative state', async () => {
    const intent: VoiceSemanticIntent = {
      kind: 'move-todo',
      target: { kind: 'title', text: 'Bogus' },
      destination: { kind: 'name', text: 'done' },
    };
    const selection = {
      todo: { selectedLocalId: 351, allowedLocalIds: [351, 352] },
    } as const;
    const selected = await resolve(
      intent,
      board([todo(351, 'Bogus', 'backlog')], [todo(352, 'Bogus', 'doing')]),
      selection,
    );
    expect(selected).toMatchObject({
      ok: true,
      value: { kind: 'command', command: { ir: { entities: { localId: 351 } } } },
    });

    const changed = await resolve(
      intent,
      board([], [todo(352, 'Bogus', 'doing')], [todo(351, 'Bogus', 'done')]),
      selection,
    );
    expect(changed).toMatchObject({ ok: true, value: { kind: 'information' } });
  });

  it('rejects a selected todo that was not in the authoritative offered set', async () => {
    const result = await resolve({
      kind: 'delete-todo',
      target: { kind: 'title', text: 'Bogus' },
    }, board([todo(351, 'Bogus', 'backlog'), todo(352, 'Bogus', 'backlog')]), {
      todo: { selectedLocalId: 999, allowedLocalIds: [351, 352] },
    });
    expect(result).toMatchObject({ ok: false, code: 'invalid_schema' });
  });

  it('supports current open and the bounded missing-title question', async () => {
    const sourceBoard = board([todo(355, 'Fixed Radical Login', 'backlog')]);
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });

    await expect(resolveVoiceSemanticIntent(
      { kind: 'open-todo', target: { kind: 'current' } },
      session.getState(),
      context(sourceBoard),
    )).resolves.toMatchObject({
      ok: true,
      value: { kind: 'command', command: { ir: { intent: 'open_todo' } } },
    });
    await expect(resolveVoiceSemanticIntent(
      { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      session.getState(),
      context(sourceBoard),
    )).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'question',
        target: { localId: 355 },
        interaction: { message: { key: 'voice.question.updateTitle' } },
      },
    });
  });

  it('converges create, open, delete, and title update on existing IR intents', async () => {
    const sourceBoard = board([todo(355, 'Fixed Radical Login', 'backlog')]);
    const cases: Array<[VoiceSemanticIntent, string]> = [
      [{ kind: 'create-todo', title: 'New story' }, 'todos.create'],
      [{ kind: 'open-todo', target: { kind: 'title', text: 'Fixed Radical Login' } }, 'open_todo'],
      [{ kind: 'delete-todo', target: { kind: 'title', text: 'Fixed Radical Login' } }, 'todos.delete'],
      [{
        kind: 'update-todo-title',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        title: 'Fix the login race condition',
      }, 'todos.update_title'],
    ];
    for (const [intent, expected] of cases) {
      const result = await resolve(intent, sourceBoard);
      expect(result).toMatchObject({
        ok: true,
        value: { kind: 'command', command: { ir: { intent: expected } } },
      });
    }
  });

  it('does not confirm a normalized no-op title update', async () => {
    const result = await resolve({
      kind: 'update-todo-title',
      target: { kind: 'title', text: 'Fixed Radical Login' },
      title: '  fixed radical login  ',
    }, board([todo(355, 'Fixed Radical Login', 'backlog')]));
    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'information',
        interaction: { message: { key: 'voice.info.titleUnchanged' } },
      },
    });
  });

  it('keeps delete destructive and open non-mutating without confirmation', async () => {
    const sourceBoard = board([todo(355, 'Fixed Radical Login', 'backlog')]);
    const deletion = await resolve({
      kind: 'delete-todo',
      target: { kind: 'title', text: 'Fixed Radical Login' },
    }, sourceBoard);
    expect(deletion).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { danger: true, requiresConfirmation: true },
      },
    });

    const opening = await resolve({
      kind: 'open-todo',
      target: { kind: 'title', text: 'Fixed Radical Login' },
    }, sourceBoard);
    expect(opening).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { danger: false, requiresConfirmation: false },
      },
    });
  });
});
