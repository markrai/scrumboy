import { describe, expect, it } from 'vitest';
import { parseCommand } from './parser.js';

describe('voice command parser', () => {
  it('accepts story and todo aliases with canonical intents', () => {
    expect(parseCommand('create story Fix login')).toEqual({
      ok: true,
      value: { intent: 'todos.create', title: 'Fix login', display: 'create todo Fix login' },
    });
    expect(parseCommand('create todo Fix login')).toEqual({
      ok: true,
      value: { intent: 'todos.create', title: 'Fix login', display: 'create todo Fix login' },
    });
    expect(parseCommand('create to do Fix login')).toEqual({
      ok: true,
      value: { intent: 'todos.create', title: 'Fix login', display: 'create todo Fix login' },
    });
    expect(parseCommand('move story fifty six to in progress')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'in progress', ambiguousId: false, display: 'move todo 56 to in progress' },
    });
    expect(parseCommand('move todo 56 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'done', ambiguousId: false, display: 'move todo 56 to done' },
    });
    expect(parseCommand('move to do 56 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'done', ambiguousId: false, display: 'move todo 56 to done' },
    });
    expect(parseCommand('delete story #56')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', localId: 56, ambiguousId: false, display: 'delete todo 56' },
    });
    expect(parseCommand('delete to-do #56')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', localId: 56, ambiguousId: false, display: 'delete todo 56' },
    });
    expect(parseCommand('assign story 56 to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', localId: 56, rawUser: 'ada', ambiguousId: false, display: 'assign todo 56 to ada' },
    });
    expect(parseCommand('assign to do 56 to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', localId: 56, rawUser: 'ada', ambiguousId: false, display: 'assign todo 56 to ada' },
    });
    expect(parseCommand('story 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'done', ambiguousId: false, display: 'todo 56 is done' },
    });
    expect(parseCommand('todo 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'done', ambiguousId: false, display: 'todo 56 is done' },
    });
    expect(parseCommand('to dos 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 56, rawStatus: 'done', ambiguousId: false, display: 'todo 56 is done' },
    });
  });

  it('accepts open and edit commands with explicit or bare IDs', () => {
    expect(parseCommand('open story twelve')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: false, display: 'open todo 12' },
    });
    expect(parseCommand('edit todo 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: false, display: 'edit todo 12' },
    });
    expect(parseCommand('open to do 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: false, display: 'open todo 12' },
    });
    expect(parseCommand('edit to-do 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: false, display: 'edit todo 12' },
    });
    expect(parseCommand('open 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: false, display: 'open todo 12' },
    });
    expect(parseCommand('edit one two')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 12, ambiguousId: true, display: 'edit todo 12' },
    });
  });

  it('allows bare IDs only for whitelisted complete commands', () => {
    expect(parseCommand('delete 12')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', localId: 12, ambiguousId: false, display: 'delete todo 12' },
    });
    expect(parseCommand('move 12 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 12, rawStatus: 'done', ambiguousId: false, display: 'move todo 12 to done' },
    });
    expect(parseCommand('12').ok).toBe(false);
    expect(parseCommand('move 12').ok).toBe(false);
  });

  it('canonicalizes spoken ID introducers before parsing commands', () => {
    expect(parseCommand('move number one to testing')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 1, rawStatus: 'testing', ambiguousId: false, display: 'move todo 1 to testing' },
    });
    expect(parseCommand('move story number one to testing')).toEqual({
      ok: true,
      value: { intent: 'todos.move', localId: 1, rawStatus: 'testing', ambiguousId: false, display: 'move todo 1 to testing' },
    });
    expect(parseCommand('delete id one')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', localId: 1, ambiguousId: false, display: 'delete todo 1' },
    });
    expect(parseCommand('open number one')).toEqual({
      ok: true,
      value: { intent: 'open_todo', localId: 1, ambiguousId: false, display: 'open todo 1' },
    });
    expect(parseCommand('assign story number one to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', localId: 1, rawUser: 'ada', ambiguousId: false, display: 'assign todo 1 to ada' },
    });
    expect(parseCommand('move number to testing')).toEqual({
      ok: false,
      code: 'invalid_id',
      message: 'Todo ID was not recognized.',
    });
  });

  it('rejects unsupported grammar and project-scope phrases', () => {
    expect(parseCommand('new story Fix login').ok).toBe(false);
    expect(parseCommand('move it to done').ok).toBe(false);
    expect(parseCommand('move story 56 to done in project beta')).toEqual({
      ok: false,
      code: 'project_scope',
      message: 'Project scope is fixed by the current board.',
    });
  });
});
