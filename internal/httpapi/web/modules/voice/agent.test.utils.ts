// Shared focused agent test fixture; excluded from the production build below.
import { vi } from 'vitest';
import type { Board, Todo } from '../types.js';
import type { VoiceCommandContext } from './command-context.js';
import type { AgentEnvelope } from './agent-protocol.js';
import { VoiceAgentSkillRegistry } from './agent-skills.js';
import { VoiceAgentLoop } from './agent-loop.js';

export type Step = AgentEnvelope | string | ((input: string) => AgentEnvelope | string);
export const skill = (name: string, args: object): AgentEnvelope => ({ kind: 'skill_call', skill: name, arguments: args } as AgentEnvelope);
export const finish: AgentEnvelope = { kind: 'finish' };
export const latestRef = (input: string): string => {
  const trace = JSON.parse(input).trace;
  return [...trace].reverse().find(entry => entry.skillResult?.result.todoRef).skillResult.result.todoRef;
};
export function harness(steps: Step[] = [], keepListening = false) {
  const todo: Todo = { id: 91, localId: 355, title: 'Happy Birthday', status: 'backlog', columnKey: 'backlog', body: 'Existing', tags: [] };
  const board: Board = { project: { id: 1, slug: 'alpha', name: 'Alpha', creatorUserId: 7, dominantColor: '#123456' },
    tags: [{ name: 'urgent' }], columnOrder: [{ key: 'backlog', name: 'Backlog', isDone: false }, { key: 'done', name: 'Done', isDone: true }], columns: { backlog: [todo], done: [] } };
  let context: VoiceCommandContext | null = { userId: 7, projectId: 1, projectSlug: 'alpha', board, members: [{ userId: 8, name: 'Mark', email: 'mark@example.test', role: 'maintainer' }], role: 'maintainer' };
  const events: string[] = [];
  const callTool = vi.fn(async (name: string, input: { localId?: number }) => {
    events.push(name);
    if (name === 'todos_get') return { todo: Object.values(board.columns).flat().find(todo => todo.localId === input.localId) };
    if (name === 'members_list') return { items: context?.members ?? [] };
    if (name === 'todos_countCompleted') return { count: 3 };
    return { items: [] };
  });
  const execute = vi.fn(async (ir) => {
    events.push(`execute:${ir.intent}`);
    const target = Object.values(board.columns).flat().find(todo => todo.localId === ir.entities.localId);
    if (ir.intent === 'todos.append_notes' || ir.intent === 'todos.replace_notes') target!.body = ir.entities.body;
    if (ir.intent === 'todos.move') target!.columnKey = ir.entities.toColumnKey;
    if (ir.intent === 'todos.assign' || ir.intent === 'todos.unassign') target!.assigneeUserId = ir.entities.assigneeUserId;
    if (ir.intent === 'todos.update_title') target!.title = ir.entities.title;
    if (ir.intent === 'todos.add_tag' || ir.intent === 'todos.remove_tag') target!.tags = ir.entities.tags;
    return { ok: true };
  });
  const options = { initialUserId: 7, initialProjectId: 1, initialProjectSlug: 'alpha', getContext: () => context,
    refreshBoard: vi.fn(async () => { events.push('refresh'); }), openTodo: vi.fn(async () => { events.push('open'); }) };
  const model = vi.fn(async (input: string) => {
    events.push('model');
    const next = steps.shift();
    if (next === undefined) throw new Error('Unexpected model call');
    const output = typeof next === 'function' ? next(input) : next;
    return typeof output === 'string' ? output : JSON.stringify(output);
  });
  const registry = new VoiceAgentSkillRegistry(options, { callTool: callTool as never, execute });
  const loop = new VoiceAgentLoop(model, registry, keepListening);
  const signal = new AbortController().signal;
  return { todo, board, context: () => context!, setContext: (value: VoiceCommandContext | null) => { context = value; }, events, callTool, execute, options, registry, loop, model, signal, steps };
}
