import { describe, expect, it } from 'vitest';
import { deterministicVoiceCommandInterpreter } from './deterministic-interpreter.js';
import { parseCommand } from './parser.js';

describe('deterministic VoiceFlow interpreter', () => {
  it.each([
    ['create', 'create story Fix login', 'create todo Fix login'],
    ['move', 'move story fifty six to in progress', 'move todo 56 to in progress'],
    ['assign', 'assign story 56 to Ada', 'assign todo 56 to ada'],
    ['open alias', 'edit to-do 12', 'edit todo 12'],
    ['delete', 'delete story #56', 'delete todo 56'],
    ['status alias', 'to dos 56 is done', 'todo 56 is done'],
  ])('delegates the %s grammar family and returns canonical text', async (_name, input, command) => {
    const interpretation = await deterministicVoiceCommandInterpreter.interpret(input);

    expect(interpretation).toEqual({ kind: 'candidate', command });
    if (interpretation.kind !== 'candidate') throw new Error('expected a candidate');
    expect(parseCommand(interpretation.command)).toEqual(parseCommand(input));
  });

  it.each([
    ['unsupported natural language', 'Could you move the login card?', 'unsupported'],
    ['empty input', '   ', 'unsupported'],
    ['malformed command', 'move 12', 'unsupported'],
    ['project override', 'move story 56 to done in project beta', 'project_scope'],
    ['invalid identifier', 'move number to testing', 'invalid_id'],
  ])('preserves the parser failure for %s', async (_name, input, code) => {
    const parserResult = parseCommand(input);
    const interpretation = await deterministicVoiceCommandInterpreter.interpret(input);

    expect(interpretation).toEqual({ kind: 'unsupported', failure: parserResult });
    expect(interpretation.kind).toBe('unsupported');
    if (interpretation.kind !== 'unsupported') throw new Error('expected unsupported input');
    expect(interpretation.failure.code).toBe(code);
  });

  it('returns only the provider-neutral candidate shape', async () => {
    const interpretation = await deterministicVoiceCommandInterpreter.interpret('open todo 56');

    expect(Object.keys(interpretation).sort()).toEqual(['command', 'kind']);
    expect(interpretation).toEqual({ kind: 'candidate', command: 'open todo 56' });
  });

  it('does not add contextual pronoun handling to the deterministic path', async () => {
    const parserResult = parseCommand('Open it');

    await expect(deterministicVoiceCommandInterpreter.interpret('Open it'))
      .resolves.toEqual({ kind: 'unsupported', failure: parserResult });
  });

  it('rejects an already-aborted request without starting lifecycle work', async () => {
    const controller = new AbortController();
    controller.abort();

    await expect(deterministicVoiceCommandInterpreter.interpret(
      'open todo 56',
      { signal: controller.signal },
    )).rejects.toMatchObject({ name: 'AbortError' });
  });
});
