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

function board(
  backlog: Todo[] = [],
  doing: Todo[] = [],
  done: Todo[] = [],
  tags: string[] = [],
): Board {
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', dominantColor: '#123456' },
    tags: tags.map((name) => ({ name, count: 0 })),
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

  it.each([
    [
      { kind: 'create-todo', title: null } as VoiceSemanticIntent,
      'title',
      'voice.question.createTitle',
    ],
    [
      { kind: 'move-todo', target: { kind: 'local-id', localId: 355 }, destination: null } as VoiceSemanticIntent,
      'destination',
      'voice.question.moveDestination',
    ],
    [
      { kind: 'assign-todo', target: { kind: 'local-id', localId: 355 }, assignee: null } as VoiceSemanticIntent,
      'assignee',
      'voice.question.assignMember',
    ],
    [
      { kind: 'append-todo-notes', target: { kind: 'local-id', localId: 355 }, notes: null } as VoiceSemanticIntent,
      'notes',
      'voice.question.appendNotes',
    ],
    [
      { kind: 'replace-todo-notes', target: { kind: 'local-id', localId: 355 }, notes: null } as VoiceSemanticIntent,
      'notes',
      'voice.question.replaceNotes',
    ],
    [
      { kind: 'add-todo-tag', target: { kind: 'local-id', localId: 355 }, tag: null } as VoiceSemanticIntent,
      'tag',
      'voice.question.whichTag',
    ],
    [
      { kind: 'remove-todo-tag', target: { kind: 'local-id', localId: 355 }, tag: null } as VoiceSemanticIntent,
      'tag',
      'voice.question.whichTag',
    ],
  ])('returns an authoritative generalized %s slot question', async (intent, pendingSlot, key) => {
    await expect(resolve(intent, board(
      [todo(355, 'Fixed Radical Login', 'backlog')],
      [],
      [],
      ['backend'],
    ))).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'question',
        pendingSlot: { slot: pendingSlot },
        interaction: { message: { key } },
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

  it('distinguishes append notes from replacement and materializes the existing update body', async () => {
    const existing = { ...todo(351, 'Bogus', 'backlog'), body: 'Existing context' };
    const appended = await resolve({
      kind: 'append-todo-notes',
      target: { kind: 'current' },
      notes: 'Investigate retry timeout',
    }, board([existing]));
    expect(appended).toMatchObject({ ok: false, code: 'unknown_story' });

    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 351 });
    await expect(resolveVoiceSemanticIntent({
      kind: 'append-todo-notes',
      target: { kind: 'current' },
      notes: 'Investigate retry timeout',
    }, session.getState(), context(board([existing])))).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: {
            intent: 'todos.append_notes',
            entities: {
              localId: 351,
              body: 'Existing context\nInvestigate retry timeout',
              notes: 'Investigate retry timeout',
            },
          },
          requiresConfirmation: true,
        },
      },
    });

    await expect(resolveVoiceSemanticIntent({
      kind: 'replace-todo-notes',
      target: { kind: 'current' },
      notes: 'Blocked by API migration',
    }, session.getState(), context(board([existing])))).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: {
            intent: 'todos.replace_notes',
            entities: { body: 'Blocked by API migration' },
          },
        },
      },
    });
  });

  it('uses a fresh authoritative todo read before materializing an appended notes replacement', async () => {
    const sourceBoard = board([{ ...todo(351, 'Bogus', 'backlog'), body: 'Stale notes' }]);
    const session = createVoiceConversationSession();
    session.setActiveProject({ projectId: 1, projectSlug: 'alpha' });
    const callTool = async () => ({
      todo: { ...todo(351, 'Bogus', 'backlog'), body: 'Fresh notes' },
    });
    const result = await resolveVoiceSemanticIntent({
      kind: 'append-todo-notes',
      target: { kind: 'title', text: 'Bogus' },
      notes: 'Investigate timeout',
    }, session.getState(), { ...context(sourceBoard), callTool });

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: { ir: { entities: { body: 'Fresh notes\nInvestigate timeout' } } },
      },
    });
  });

  it('filters tag add no-ops before deciding todo ambiguity', async () => {
    const result = await resolve({
      kind: 'add-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'backend' },
    }, board([
      { ...todo(351, 'Bogus', 'backlog'), tags: ['backend'] },
      { ...todo(352, 'Bogus', 'backlog'), tags: [] },
    ], [], [], ['backend']));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: {
            intent: 'todos.add_tag',
            entities: { localId: 352, tags: ['backend'], tag: 'backend' },
          },
        },
      },
    });
  });

  it('returns tag add/remove no-ops without confirmation and removes through the existing tag list patch', async () => {
    const tagged = { ...todo(351, 'Bogus', 'backlog'), tags: ['frontend', 'backend'] };
    await expect(resolve({
      kind: 'add-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'backend' },
    }, board([tagged], [], [], ['backend']))).resolves.toMatchObject({
      ok: true,
      value: { kind: 'information', interaction: { message: { key: 'voice.info.tagAlreadyPresent' } } },
    });

    await expect(resolve({
      kind: 'remove-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'backend' },
    }, board([tagged], [], [], ['backend']))).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: {
            intent: 'todos.remove_tag',
            entities: { localId: 351, tags: ['frontend'], tag: 'backend' },
          },
        },
      },
    });
  });

  it('clarifies real todo and tag ambiguity with authoritative choices', async () => {
    const todoAmbiguity = await resolve({
      kind: 'add-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'backend' },
    }, board([
      { ...todo(351, 'Bogus', 'backlog'), tags: [] },
      { ...todo(352, 'Bogus', 'backlog'), tags: [] },
    ], [], [], ['backend']));
    expect(todoAmbiguity).toMatchObject({
      ok: true,
      value: { kind: 'clarification', choices: [{ kind: 'todo' }, { kind: 'todo' }] },
    });

    const tagAmbiguity = await resolve({
      kind: 'add-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'back' },
    }, board([todo(351, 'Bogus', 'backlog')], [], [], ['backend', 'backoffice']));
    expect(tagAmbiguity).toMatchObject({
      ok: true,
      value: {
        kind: 'clarification',
        interaction: {
          message: { key: 'voice.prompt.whichTag' },
          options: [{ id: 'tag:backend' }, { id: 'tag:backoffice' }],
        },
      },
    });
  });

  it('supports single-assignee clearing and suppresses an already-unassigned mutation', async () => {
    const assigned = await resolve({
      kind: 'unassign-todo',
      target: { kind: 'title', text: 'Bogus' },
    }, board([todo(351, 'Bogus', 'backlog', 7)]));
    expect(assigned).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: { intent: 'todos.unassign', entities: { localId: 351, assigneeUserId: null } },
        },
      },
    });

    await expect(resolve({
      kind: 'unassign-todo',
      target: { kind: 'title', text: 'Bogus' },
    }, board([todo(351, 'Bogus', 'backlog')]))).resolves.toMatchObject({
      ok: true,
      value: { kind: 'information', interaction: { message: { key: 'voice.info.alreadyUnassigned' } } },
    });
  });

  it('grounds a named unassign member and uses assignment viability to remove false todo ambiguity', async () => {
    const result = await resolve({
      kind: 'unassign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark Rai' },
    }, board([
      todo(351, 'Bogus', 'backlog', 7),
      todo(352, 'Bogus', 'backlog', 8),
    ]));

    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'command',
        command: {
          ir: { intent: 'todos.unassign', entities: { localId: 351, assigneeUserId: null } },
        },
      },
    });
  });

  it('does not clear a different assignee when the user explicitly names a member', async () => {
    await expect(resolve({
      kind: 'unassign-todo',
      target: { kind: 'title', text: 'Bogus' },
      assignee: { kind: 'name', text: 'Mark Rai' },
    }, board([todo(351, 'Bogus', 'backlog', 8)]))).resolves.toMatchObject({
      ok: true,
      value: {
        kind: 'information',
        interaction: { message: { key: 'voice.info.notAssignedToMember' } },
      },
    });
  });

  it.each([
    ['assignee', 'voice.info.todoAssignee'],
    ['tags', 'voice.info.todoTags'],
    ['notes', 'voice.info.todoNotes'],
    ['lane', 'voice.info.todoLane'],
  ] as const)('answers the authoritative %s inspection without a command', async (aspect, key) => {
    const existing = {
      ...todo(351, 'Bogus', 'backlog', 7),
      body: 'Investigate timeout',
      tags: ['backend'],
    };
    const result = await resolve({
      kind: 'inspect-todo',
      target: { kind: 'title', text: 'Bogus' },
      aspect,
    }, board([existing], [], [], ['backend']));
    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'information',
        target: { localId: 351 },
        interaction: { message: { key } },
      },
    });
  });

  it('clarifies ambiguous read targets instead of guessing an answer', async () => {
    const result = await resolve({
      kind: 'inspect-todo',
      target: { kind: 'title', text: 'Bogus' },
      aspect: 'assignee',
    }, board([todo(351, 'Bogus', 'backlog'), todo(352, 'Bogus', 'backlog')]));
    expect(result).toMatchObject({
      ok: true,
      value: { kind: 'clarification', choices: [{ kind: 'todo' }, { kind: 'todo' }] },
    });
  });

  it('renders a project completion count returned by the authoritative read tool', async () => {
    const session = createVoiceConversationSession();
    session.setActiveProject({ projectId: 1, projectSlug: 'alpha' });
    const calls: Array<{ tool: string; input: Record<string, unknown> }> = [];
    const result = await resolveVoiceSemanticIntent({
      kind: 'count-completed-todos',
      period: { kind: 'this-week' },
    }, session.getState(), {
      ...context(board()),
      timezone: 'America/New_York',
      callTool: async (tool, input) => {
        calls.push({ tool, input });
        return { count: 12 };
      },
    });
    expect(calls).toEqual([{
      tool: 'todos_countCompleted',
      input: {
        projectSlug: 'alpha',
        period: 'this-week',
        timezone: 'America/New_York',
      },
    }]);
    expect(result).toMatchObject({
      ok: true,
      value: {
        kind: 'information',
        interaction: { message: { key: 'voice.info.completedThisWeek', values: { count: 12 } } },
      },
    });
  });
});
