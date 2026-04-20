import { describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';

function board(overrides: Partial<Board> = {}): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
    },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'not_started', name: 'Not Started', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'testing', name: 'Testing', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: {
      backlog: [],
      not_started: [],
      doing: [{ id: 10, localId: 56, title: 'Fix login', status: 'doing' }],
      testing: [],
      done: [],
    },
    ...overrides,
  };
}

const members: BoardMember[] = [
  { userId: 7, name: 'Ada Lovelace', email: 'ada@example.com', role: 'maintainer' },
  { userId: 8, name: 'Grace Hopper', email: 'grace@example.com', role: 'contributor' },
];

async function parseAndResolve(input: string, sourceBoard = board()) {
  const parsed = parseCommand(input);
  if (!parsed.ok) throw new Error('parse failed');
  return resolveCommandDraft(parsed.value, {
    projectId: 1,
    projectSlug: 'alpha',
    board: sourceBoard,
    members,
  });
}

describe('voice command resolution', () => {
  it('maps spoken status aliases to active board lane keys', async () => {
    const resolved = await parseAndResolve('todo 56 is in progress');

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'todos.move',
          projectId: 1,
          projectSlug: 'alpha',
          entities: { localId: 56, toColumnKey: 'doing' },
        },
      },
    });
  });

  it('maps explicit status aliases from the active board', async () => {
    const sourceBoard = board({
      columnOrder: [
        { key: 'todo', name: 'To Do', isDone: false },
        { key: 'done', name: 'Done', isDone: true },
      ],
      columns: {
        todo: [{ id: 10, localId: 56, title: 'Fix login', status: 'todo' }],
        done: [],
      },
    });

    const resolved = await parseAndResolve('move todo 56 to to do', sourceBoard);

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'todos.move',
          entities: { localId: 56, toColumnKey: 'todo' },
        },
      },
    });
  });

  it('resolves open commands inside the active project', async () => {
    const resolved = await parseAndResolve('open todo 56');

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'open_todo',
          projectId: 1,
          projectSlug: 'alpha',
          entities: { localId: 56 },
        },
        requiresConfirmation: false,
      },
    });
  });

  it('resolves assignees only from active project members', async () => {
    const resolved = await parseAndResolve('assign story 56 to ada');

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'todos.assign',
          entities: { localId: 56, assigneeUserId: 7 },
        },
      },
    });
  });

  it('uses local board and member state before remote MCP lookups', async () => {
    const parsed = parseCommand('assign story 56 to ada lovelace');
    if (!parsed.ok) throw new Error('parse failed');
    const callTool = vi.fn();

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board(),
      members,
      callTool,
    });

    expect(resolved.ok).toBe(true);
    expect(callTool).not.toHaveBeenCalled();
  });

  it('rejects ambiguous lane names', async () => {
    const customBoard = board({
      columnOrder: [
        { key: 'review_a', name: 'Review', isDone: false },
        { key: 'review_b', name: 'Review', isDone: false },
      ],
      columns: {
        review_a: [{ id: 10, localId: 56, title: 'Fix login', status: 'review_a' }],
        review_b: [],
      },
    });

    const resolved = await parseAndResolve('move story 56 to review', customBoard);

    expect(resolved).toEqual({
      ok: false,
      code: 'ambiguous_status',
      message: 'Status matches more than one lane.',
    });
  });

  it('verifies unloaded stories through the active project MCP tool only', async () => {
    const parsed = parseCommand('delete story 99');
    if (!parsed.ok) throw new Error('parse failed');
    const callTool = vi.fn().mockResolvedValue({
      todo: { id: 11, localId: 99, title: 'Deferred story', status: 'backlog' },
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board(),
      members,
      callTool,
    });

    expect(callTool).toHaveBeenCalledWith('todos.get', { projectSlug: 'alpha', localId: 99 });
    expect(resolved).toMatchObject({
      ok: true,
      value: { ir: { intent: 'todos.delete', entities: { localId: 99 } } },
    });
  });

  it('refreshes members once when local members do not resolve an assignee', async () => {
    const parsed = parseCommand('assign story 56 to grace');
    if (!parsed.ok) throw new Error('parse failed');
    const callTool = vi.fn().mockResolvedValue({
      items: [{ userId: 8, name: 'Grace Hopper', email: 'grace@example.com', role: 'contributor' }],
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board(),
      members: [members[0]],
      callTool,
    });

    expect(callTool).toHaveBeenCalledTimes(1);
    expect(callTool).toHaveBeenCalledWith('members.list', { projectSlug: 'alpha' });
    expect(resolved).toMatchObject({
      ok: true,
      value: { ir: { intent: 'todos.assign', entities: { localId: 56, assigneeUserId: 8 } } },
    });
  });

  it('rejects ambiguous users safely', async () => {
    const parsed = parseCommand('assign story 56 to ada lovelace');
    if (!parsed.ok) throw new Error('parse failed');

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board(),
      members: [
        ...members,
        { userId: 9, name: 'Ada Lovelace', email: 'ada2@example.com', role: 'contributor' },
      ],
    });

    expect(resolved).toEqual({
      ok: false,
      code: 'ambiguous_user',
      message: 'Assignee matches more than one project member.',
    });
  });

  it('auto-resolves exact title matches inside the active project', async () => {
    const sourceBoard = board({
      columns: {
        backlog: [{ id: 1, localId: 12, title: 'Login Page', status: 'backlog' }],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });

    const resolved = await parseAndResolve('open login page', sourceBoard);

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'open_todo',
          projectSlug: 'alpha',
          entities: { localId: 12 },
        },
        summary: 'Open todo #12: Login Page',
      },
    });
  });

  it('resolves trailing spoken number markers as title suffixes', async () => {
    const sourceBoard = board({
      columns: {
        backlog: [
          { id: 1, localId: 3, title: 'Numeric local ID should not win', status: 'backlog' },
          { id: 2, localId: 21, title: 'Notification Test!', status: 'backlog' },
          { id: 3, localId: 22, title: 'Notificatoin Test #2', status: 'backlog' },
          { id: 4, localId: 23, title: 'Notification Test #3', status: 'backlog' },
        ],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });
    const parsed = parseCommand('move notification test number 3 to not started');
    if (!parsed.ok) throw new Error('parse failed');

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: sourceBoard,
      members,
    });

    expect(parsed.value).toMatchObject({
      intent: 'todos.move',
      display: 'move todo notification test 3 to not started',
    });
    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'todos.move',
          projectSlug: 'alpha',
          entities: { localId: 23, toColumnKey: 'not_started' },
        },
        summary: 'Move todo #23: Notification Test #3 to Not Started',
      },
    });
  });

  it('keeps pure number targets on the numeric ID path', async () => {
    const sourceBoard = board({
      columns: {
        backlog: [
          { id: 1, localId: 3, title: 'Numeric local ID should win', status: 'backlog' },
          { id: 2, localId: 23, title: 'Notification Test #3', status: 'backlog' },
        ],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });

    const resolved = await parseAndResolve('move number 3 to done', sourceBoard);

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: {
          intent: 'todos.move',
          entities: { localId: 3, toColumnKey: 'done' },
        },
        summary: 'Move todo #3: Numeric local ID should win to Done',
      },
    });
  });

  it('uses active-project todos.search as title candidate generation for unloaded todos', async () => {
    const parsed = parseCommand('move login redirect to done');
    if (!parsed.ok) throw new Error('parse failed');
    const callTool = vi.fn(async (tool: string, input: Record<string, unknown>) => {
      if (tool === 'todos.search') {
        return {
          items: [
            { projectSlug: 'alpha', localId: 12, title: 'Fix login redirect' },
            { projectSlug: 'other', localId: 44, title: 'Fix login redirect' },
          ],
        };
      }
      if (tool === 'todos.get' && input.projectSlug === 'alpha' && input.localId === 12) {
        return { todo: { id: 12, localId: 12, title: 'Fix login redirect', status: 'backlog' } };
      }
      throw new Error('unexpected call');
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board({ columns: { backlog: [], not_started: [], doing: [], testing: [], done: [] } }),
      members,
      callTool,
    });

    expect(callTool).toHaveBeenCalledWith('todos.search', { projectSlug: 'alpha', query: 'login redirect', limit: 10 });
    expect(callTool).toHaveBeenCalledWith('todos.get', { projectSlug: 'alpha', localId: 12 });
    expect(resolved).toMatchObject({
      ok: true,
      value: { ir: { intent: 'todos.move', entities: { localId: 12, toColumnKey: 'done' } } },
    });
  });

  it('returns top three ambiguous title candidates without guessing', async () => {
    const parsed = parseCommand('open login');
    if (!parsed.ok) throw new Error('parse failed');

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board({
        columns: {
          backlog: [
            { id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' },
            { id: 2, localId: 13, title: 'Fix login validation', status: 'backlog' },
            { id: 3, localId: 14, title: 'Fix login button style', status: 'backlog' },
            { id: 4, localId: 15, title: 'Fix login tooltip', status: 'backlog' },
          ],
          not_started: [],
          doing: [],
          testing: [],
          done: [],
        },
      }),
      members,
    });

    expect(resolved).toEqual({
      ok: false,
      code: 'ambiguous_story',
      message: 'More than one todo matched. Choose one.',
      candidates: [
        { localId: 12, title: 'Fix login redirect' },
        { localId: 13, title: 'Fix login validation' },
        { localId: 14, title: 'Fix login button style' },
      ],
      draft: parsed.value,
    });
  });

  it('refuses weak title matches instead of presenting junk contenders', async () => {
    const parsed = parseCommand('delete dashboard');
    if (!parsed.ok) throw new Error('parse failed');

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: board({
        columns: {
          backlog: [{ id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' }],
          not_started: [],
          doing: [],
          testing: [],
          done: [],
        },
      }),
      members,
    });

    expect(resolved).toEqual({
      ok: false,
      code: 'unknown_story',
      message: 'No strong todo title match was found in this project.',
    });
  });

  it('resolves an ambiguous title through an explicitly offered selected local ID', async () => {
    const parsed = parseCommand('delete login');
    if (!parsed.ok) throw new Error('parse failed');
    const sourceBoard = board({
      columns: {
        backlog: [
          { id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' },
          { id: 2, localId: 13, title: 'Fix login validation', status: 'backlog' },
        ],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: sourceBoard,
      members,
    }, { selectedLocalId: 13, allowedLocalIds: [12, 13] });

    expect(resolved).toMatchObject({
      ok: true,
      value: {
        ir: { intent: 'todos.delete', projectSlug: 'alpha', entities: { localId: 13 } },
        summary: 'Delete todo #13: Fix login validation',
      },
    });
  });

  it('rejects selected local IDs that were not offered as ambiguity candidates', async () => {
    const parsed = parseCommand('delete login');
    if (!parsed.ok) throw new Error('parse failed');
    const sourceBoard = board({
      columns: {
        backlog: [
          { id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' },
          { id: 2, localId: 13, title: 'Fix login validation', status: 'backlog' },
          { id: 3, localId: 99, title: 'Unrelated active project todo', status: 'backlog' },
        ],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: sourceBoard,
      members,
    }, { selectedLocalId: 99, allowedLocalIds: [12, 13] });

    expect(resolved).toEqual({
      ok: false,
      code: 'invalid_schema',
      message: 'Selected todo was not one of the offered choices.',
    });
  });

  it('rejects selected local IDs when no offered-candidate allowlist is supplied', async () => {
    const parsed = parseCommand('delete login');
    if (!parsed.ok) throw new Error('parse failed');
    const sourceBoard = board({
      columns: {
        backlog: [
          { id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' },
          { id: 2, localId: 13, title: 'Fix login validation', status: 'backlog' },
        ],
        not_started: [],
        doing: [],
        testing: [],
        done: [],
      },
    });

    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: sourceBoard,
      members,
    }, { selectedLocalId: 13 });

    expect(resolved).toEqual({
      ok: false,
      code: 'invalid_schema',
      message: 'Selected todo was not one of the offered choices.',
    });
  });
});
