import { describe, expect, it } from 'vitest';
import { parseCommand } from './parser.js';

function idTarget(localId: number, ambiguousId = false) {
  return { kind: 'id', localId, ambiguousId, display: String(localId) };
}

function titleTarget(phrase: string) {
  return { kind: 'title', phrase, display: phrase };
}

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
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'in progress', display: 'move todo 56 to in progress' },
    });
    expect(parseCommand('move todo 56 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'done', display: 'move todo 56 to done' },
    });
    expect(parseCommand('move to do 56 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'done', display: 'move todo 56 to done' },
    });
    expect(parseCommand('delete story #56')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', target: idTarget(56), display: 'delete todo 56' },
    });
    expect(parseCommand('delete to-do #56')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', target: idTarget(56), display: 'delete todo 56' },
    });
    expect(parseCommand('assign story 56 to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', target: idTarget(56), rawUser: 'ada', display: 'assign todo 56 to ada' },
    });
    expect(parseCommand('assign to do 56 to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', target: idTarget(56), rawUser: 'ada', display: 'assign todo 56 to ada' },
    });
    expect(parseCommand('story 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'done', display: 'todo 56 is done' },
    });
    expect(parseCommand('todo 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'done', display: 'todo 56 is done' },
    });
    expect(parseCommand('to dos 56 is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(56), rawStatus: 'done', display: 'todo 56 is done' },
    });
  });

  it('accepts open and edit commands with explicit or bare IDs', () => {
    expect(parseCommand('open story twelve')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12), display: 'open todo 12' },
    });
    expect(parseCommand('edit todo 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12), display: 'edit todo 12' },
    });
    expect(parseCommand('open to do 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12), display: 'open todo 12' },
    });
    expect(parseCommand('edit to-do 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12), display: 'edit todo 12' },
    });
    expect(parseCommand('open 12')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12), display: 'open todo 12' },
    });
    expect(parseCommand('edit one two')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(12, true), display: 'edit todo 12' },
    });
  });

  it('accepts title targets without fuzzy intent guessing', () => {
    expect(parseCommand('open login page')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: titleTarget('login page'), display: 'open todo login page' },
    });
    expect(parseCommand('edit forgot password')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: titleTarget('forgot password'), display: 'edit todo forgot password' },
    });
    expect(parseCommand('delete landing page bug')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', target: titleTarget('landing page bug'), display: 'delete todo landing page bug' },
    });
    expect(parseCommand('move login redirect to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: titleTarget('login redirect'), rawStatus: 'done', display: 'move todo login redirect to done' },
    });
    expect(parseCommand('move forgot password to in progress')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: titleTarget('forgot password'), rawStatus: 'in progress', display: 'move todo forgot password to in progress' },
    });
    expect(parseCommand('story login bug is done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: titleTarget('login bug'), rawStatus: 'done', display: 'todo login bug is done' },
    });
    expect(parseCommand('todo welcome page is in progress')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: titleTarget('welcome page'), rawStatus: 'in progress', display: 'todo welcome page is in progress' },
    });
  });

  it('keeps enhanced discovery synonyms out of the deterministic basic grammar', () => {
    expect(parseCommand('open Goblin').ok).toBe(true);
    for (const text of ['find Goblin', 'search for Goblin', 'look up Goblin']) {
      expect(parseCommand(text).ok, text).toBe(false);
    }
  });

  it('allows bare IDs only for whitelisted complete commands', () => {
    expect(parseCommand('delete 12')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', target: idTarget(12), display: 'delete todo 12' },
    });
    expect(parseCommand('move 12 to done')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(12), rawStatus: 'done', display: 'move todo 12 to done' },
    });
    expect(parseCommand('12').ok).toBe(false);
    expect(parseCommand('move 12').ok).toBe(false);
  });

  it('canonicalizes spoken ID introducers before parsing commands', () => {
    expect(parseCommand('move number one to testing')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(1), rawStatus: 'testing', display: 'move todo 1 to testing' },
    });
    expect(parseCommand('move story number one to testing')).toEqual({
      ok: true,
      value: { intent: 'todos.move', target: idTarget(1), rawStatus: 'testing', display: 'move todo 1 to testing' },
    });
    expect(parseCommand('delete id one')).toEqual({
      ok: true,
      value: { intent: 'todos.delete', target: idTarget(1), display: 'delete todo 1' },
    });
    expect(parseCommand('open number one')).toEqual({
      ok: true,
      value: { intent: 'open_todo', target: idTarget(1), display: 'open todo 1' },
    });
    expect(parseCommand('assign story number one to Ada')).toEqual({
      ok: true,
      value: { intent: 'todos.assign', target: idTarget(1), rawUser: 'ada', display: 'assign todo 1 to ada' },
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
