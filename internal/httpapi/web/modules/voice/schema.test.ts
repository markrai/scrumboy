import { describe, expect, it } from 'vitest';
import type { Board } from '../types.js';
import { validateCommandIR } from './schema.js';

const board: Board = {
  project: { id: 1, name: 'Alpha', slug: 'alpha', dominantColor: '#123456' },
  tags: [],
  columnOrder: [{ key: 'doing', name: 'In Progress', isDone: false }],
  columns: { doing: [] },
};

describe('voice command schema validation', () => {
  it('rejects stale project context', () => {
    const result = validateCommandIR({
      intent: 'todos.delete',
      projectId: 2,
      projectSlug: 'beta',
      entities: { localId: 56 },
    }, { projectId: 1, projectSlug: 'alpha', board });

    expect(result).toEqual({
      ok: false,
      code: 'stale_context',
      message: 'The board changed before the command could run.',
    });
  });

  it('rejects extra fields in the IR and entities', () => {
    expect(validateCommandIR({
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      extra: true,
      entities: { localId: 56 },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);

    expect(validateCommandIR({
      intent: 'todos.delete',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, title: 'wrong' },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);

    expect(validateCommandIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login', columnKey: 'doing', unexpected: true },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);

    expect(validateCommandIR({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, title: 'wrong' },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);
  });

  it('requires create commands to carry an active board column key', () => {
    expect(validateCommandIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login', columnKey: 'doing' },
    }, { projectId: 1, projectSlug: 'alpha', board })).toMatchObject({
      ok: true,
      value: { entities: { title: 'Fix login', columnKey: 'doing' } },
    });

    expect(validateCommandIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login' },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);

    expect(validateCommandIR({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Fix login', columnKey: 'backlog' },
    }, { projectId: 1, projectSlug: 'alpha', board })).toMatchObject({
      ok: false,
      code: 'unknown_status',
    });
  });

  it('rejects a move onto agenda when agenda is not a workflow column', () => {
    const result = validateCommandIR({
      intent: 'todos.move',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 12, toColumnKey: 'agenda' },
    }, { projectId: 1, projectSlug: 'alpha', board });

    expect(result).toMatchObject({
      ok: false,
      code: 'unknown_status',
    });
  });

  it('still rejects todos.move to agenda when Agenda is enabled', () => {
    const withAgenda: Board = {
      ...board,
      agenda: { enabled: true, timezone: 'UTC', events: [] },
    };
    const result = validateCommandIR({
      intent: 'todos.move',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 12, toColumnKey: 'agenda' },
    }, { projectId: 1, projectSlug: 'alpha', board: withAgenda });

    expect(result).toMatchObject({
      ok: false,
      code: 'unknown_status',
    });
  });

  it('normalizes title updates with the same authoritative title bounds as create', () => {
    expect(validateCommandIR({
      intent: 'todos.update_title',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, title: '  Fix the login race condition  ' },
    }, { projectId: 1, projectSlug: 'alpha', board })).toMatchObject({
      ok: true,
      value: { entities: { localId: 56, title: 'Fix the login race condition' } },
    });

    for (const title of ['', '   ', 'x'.repeat(201)]) {
      expect(validateCommandIR({
        intent: 'todos.update_title',
        projectId: 1,
        projectSlug: 'alpha',
        entities: { localId: 56, title },
      }, { projectId: 1, projectSlug: 'alpha', board })).toMatchObject({
        ok: false,
        code: 'invalid_title',
      });
    }
  });

  it('rejects smuggled title-update fields', () => {
    expect(validateCommandIR({
      intent: 'todos.update_title',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, title: 'New title', notes: 'also replace these' },
    }, { projectId: 1, projectSlug: 'alpha', board }).ok).toBe(false);
  });
});
