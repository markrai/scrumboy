// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { harness } from './agent.test.utils.js';
import { prepareVoiceCreate } from './voice-create-prepare.js';
import type { VoiceCreatePlanV1 } from './voice-create-plan.js';
import { buildMcpCall, executeCommandIR } from './execute.js';
import { validateCommandIR } from './schema.js';

const base: VoiceCreatePlanV1 = { version: 1, kind: 'create', title: 'Big Man' };
describe('deterministic enriched create preparation', () => {
  it('uses authoritative leftmost, not Backlog, and suppresses empty default details', () => {
    const h = harness();
    h.board.columnOrder!.unshift({ key: 'triage', name: 'Triage', isDone: false }); h.board.columns.triage = [];
    const result = prepareVoiceCreate(base, h.context(), []);
    expect(result.kind).toBe('prepared'); if (result.kind !== 'prepared') return;
    expect(result.value.command.ir).toEqual({ intent: 'todos.create', projectId: 1, projectSlug: 'alpha', entities: { title: 'Big Man', columnKey: 'triage', body: '', tags: [], assigneeUserId: null } });
    expect(result.value.command.summary).toBe('Create "Big Man" in Triage');
    expect(h.execute).not.toHaveBeenCalled(); expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('resolves explicit Done and never defaults an invalid explicit lane', () => {
    const h = harness();
    const result = prepareVoiceCreate({ ...base, lane: 'Done' }, h.context(), []);
    expect(result.kind === 'prepared' && result.value.command.ir.entities.columnKey).toBe('done');
    expect(() => prepareVoiceCreate({ ...base, lane: 'Unknown' }, h.context(), [])).toThrow('lane');
    h.board.columnOrder = undefined;
    expect(() => prepareVoiceCreate(base, h.context(), [])).toThrow('lane');
  });
  it('returns deterministic person choices and binds only an offered member', () => {
    const h = harness(); h.context().members.push({ userId: 9, name: 'Mark Smith', email: 'ms@example.test', role: 'maintainer' });
    h.context().members[0].name = 'Mark Jones';
    const plan = { ...base, assignee: 'Mark' };
    const result = prepareVoiceCreate(plan, h.context(), h.context().members);
    expect(result.kind).toBe('member-choice');
    if (result.kind !== 'member-choice') return;
    expect(result.choices).toHaveLength(2);
    expect(prepareVoiceCreate(plan, h.context(), h.context().members, result.choices[1]).kind).toBe('prepared');
    expect(() => prepareVoiceCreate(plan, h.context(), h.context().members, { userId: 55, name: 'Fake', email: 'fake' })).toThrow();
  });
  it('compiles all fields into exactly one existing MCP create, with no follow-up mutation', async () => {
    const h = harness();
    const result = prepareVoiceCreate({ ...base, lane: 'Backlog', assignee: 'Mark', tags: ['urgent'], notes: 'Call tomorrow' }, h.context(), h.context().members);
    if (result.kind !== 'prepared') throw new Error('not prepared');
    expect(buildMcpCall(result.value.command.ir as never)).toEqual({ tool: 'todos_create', input: { projectSlug: 'alpha', title: 'Big Man', columnKey: 'backlog', assigneeUserId: 8, tags: ['urgent'], body: 'Call tomorrow' } });
    expect(result.value.command.summary).toContain('Assign Mark'); expect(result.value.command.summary).toContain('urgent'); expect(result.value.command.summary).toContain('Call tomorrow');
    await executeCommandIR(result.value.command.ir, { callTool: h.callTool as never, recordMutation: () => {} });
    expect(h.callTool).toHaveBeenCalledOnce(); expect(h.callTool.mock.calls[0][0]).toBe('todos_create');
    expect(Object.isFrozen(result.value.command.ir.entities.tags)).toBe(true);
  });
  it('blocks unsupported content, missing titles, tags and unauthorized context', () => {
    const h = harness();
    for (const plan of [{ version: 1, kind: 'create' }, { ...base, tags: ['invented'] }, { ...base, unhandled: [{ text: 'schedule it', reason: 'unsupported' }] }]) {
      expect(() => prepareVoiceCreate(plan as VoiceCreatePlanV1, h.context(), [])).toThrow();
    }
    h.context().role = 'viewer'; expect(() => prepareVoiceCreate(base, h.context(), [])).toThrow('unauthorized');
  });
  it('keeps legacy creates valid and rejects partial/invalid enriched fields', () => {
    const h = harness();
    const ir = { intent: 'todos.create', projectId: 1, projectSlug: 'alpha', entities: { title: 'Old', columnKey: 'backlog' } };
    expect(validateCommandIR(ir, h.context()).ok).toBe(true);
    for (const extra of [{ body: '' }, { body: '', tags: [], assigneeUserId: -1 }, { body: '', tags: ['New Tag'], assigneeUserId: null }]) {
      expect(validateCommandIR({ ...ir, entities: { ...ir.entities, ...extra } }, h.context()).ok).toBe(false);
    }
  });
});
