// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { harness } from './agent.test.utils.js';
import { formatVoiceCreateMember, prepareVoiceCreate } from './voice-create-prepare.js';
import type { VoiceCreatePlanV1 } from './voice-create-plan.js';
import { buildMcpCall, executeCommandIR } from './execute.js';
import { validateCommandIR } from './schema.js';

const base: VoiceCreatePlanV1 = { version: 1, kind: 'create', title: 'Big Man' };
const boardTags = (h: ReturnType<typeof harness>) => h.board.tags.map(tag => ({ name: tag.name }));
describe('deterministic enriched create preparation', () => {
  it('never renders a missing member field as literal undefined', () => {
    expect(formatVoiceCreateMember({ userId: 8, name: 'Mark Rai' })).toBe('Mark Rai');
    expect(formatVoiceCreateMember({ userId: 8, email: 'mark@example.test' })).toBe('mark@example.test');
    expect(formatVoiceCreateMember({ userId: 8 })).toBe('8');
    expect(formatVoiceCreateMember({ userId: 8, name: 'Mark Rai' })).not.toContain('undefined');
  });

  it('uses authoritative leftmost, not Backlog, and suppresses empty default details', () => {
    const h = harness();
    h.board.columnOrder!.unshift({ key: 'triage', name: 'Triage', isDone: false }); h.board.columns.triage = [];
    const result = prepareVoiceCreate(base, h.context(), [], []);
    expect(result.kind).toBe('prepared'); if (result.kind !== 'prepared') return;
    expect(result.value.command.ir).toEqual({ intent: 'todos.create', projectId: 1, projectSlug: 'alpha', entities: { title: 'Big Man', columnKey: 'triage', body: '', tags: [], assigneeUserId: null } });
    expect(result.value.command.summary).toBe('Create "Big Man" in Triage');
    expect(h.execute).not.toHaveBeenCalled(); expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('resolves explicit Done and never defaults an invalid explicit lane', () => {
    const h = harness();
    const result = prepareVoiceCreate({ ...base, lane: 'Done' }, h.context(), [], []);
    expect(result.kind === 'prepared' && result.value.command.ir.entities.columnKey).toBe('done');
    expect(() => prepareVoiceCreate({ ...base, lane: 'Unknown' }, h.context(), [], [])).toThrow('lane');
    h.board.columnOrder = undefined;
    expect(() => prepareVoiceCreate(base, h.context(), [], [])).toThrow('lane');
    h.board.columns.testing = [];
    const explicit = prepareVoiceCreate({ ...base, lane: 'Testing' }, h.context(), [], []);
    expect(explicit.kind === 'prepared' && explicit.value.command.ir.entities.columnKey).toBe('testing');
  });
  it('uses the rendered authoritative lane order for an omitted lane', () => {
    const h = harness();
    h.board.columnOrder = [
      { key: 'not_started', name: 'Not Started', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'testing', name: 'Testing', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ];
    h.board.columns = { not_started: [], doing: [], testing: [], done: [] };
    const result = prepareVoiceCreate(base, h.context(), [], []);
    expect(result.kind).toBe('prepared');
    if (result.kind !== 'prepared') return;
    expect(result.value.command.ir.entities.columnKey).toBe('not_started');
    expect(result.value.command.summary).toContain('Not Started');
  });
  it('returns deterministic person choices and binds only an offered member', () => {
    const h = harness(); h.context().members.push({ userId: 9, name: 'Mark Smith', email: 'ms@example.test', role: 'maintainer' });
    h.context().members[0].name = 'Mark Jones';
    const plan = { ...base, assignee: 'Mark' };
    const result = prepareVoiceCreate(plan, h.context(), h.context().members, []);
    expect(result.kind).toBe('member-choice');
    if (result.kind !== 'member-choice') return;
    expect(result.choices).toHaveLength(2);
    expect(prepareVoiceCreate(plan, h.context(), h.context().members, [], result.choices[1]).kind).toBe('prepared');
    expect(() => prepareVoiceCreate(plan, h.context(), h.context().members, [], { userId: 55, name: 'Fake', email: 'fake' })).toThrow();
  });
  it('compiles all fields into exactly one existing MCP create, with no follow-up mutation', async () => {
    const h = harness();
    const result = prepareVoiceCreate({ ...base, lane: 'Backlog', assignee: 'Mark', tags: ['urgent'], notes: 'Call tomorrow' }, h.context(), h.context().members, boardTags(h));
    if (result.kind !== 'prepared') throw new Error('not prepared');
    expect(buildMcpCall(result.value.command.ir as never)).toEqual({ tool: 'todos_create', input: { projectSlug: 'alpha', title: 'Big Man', columnKey: 'backlog', assigneeUserId: 8, tags: ['urgent'], body: 'Call tomorrow' } });
    expect(result.value.command.summary).toContain('Assign Mark'); expect(result.value.command.summary).toContain('urgent'); expect(result.value.command.summary).toContain('Call tomorrow');
    await executeCommandIR(result.value.command.ir, { callTool: h.callTool as never, recordMutation: () => {} });
    expect(h.callTool).toHaveBeenCalledOnce(); expect(h.callTool.mock.calls[0][0]).toBe('todos_create');
    expect(Object.isFrozen(result.value.command.ir.entities.tags)).toBe(true);
  });
  it('resolves acronym speech forms to the exact authoritative tag and rejects normalized collisions', () => {
    const h = harness();
    h.board.tags = [{ name: 'ux', count: 0 }, { name: 'architecture', count: 0 }, { name: 'mobile', count: 0 }];
    const result = prepareVoiceCreate({ ...base, assignee: 'Mark', tags: ['U.X.'] }, h.context(), h.context().members, boardTags(h));
    expect(result.kind).toBe('prepared');
    if (result.kind !== 'prepared') return;
    expect(result.value.command.ir.entities.tags).toEqual(['ux']);
    expect(result.value.command.summary).toContain('Tags: ux');
    expect(result.value.tagReferenceNormalizationApplied).toBe(true);

    const architecture = prepareVoiceCreate({ ...base, tags: ['architecture'] }, h.context(), [], boardTags(h));
    expect(architecture.kind === 'prepared' && architecture.value.command.ir.entities.tags).toEqual(['architecture']);

    const mobile = prepareVoiceCreate({ ...base, tags: ['mobile'] }, h.context(), [], boardTags(h));
    expect(mobile.kind === 'prepared' && mobile.value.command.ir.entities.tags).toEqual(['mobile']);

    const split = prepareVoiceCreate({ ...base, tags: ['U', 'X'] }, h.context(), [], boardTags(h));
    expect(split.kind).toBe('prepared');
    if (split.kind !== 'prepared') return;
    expect(split.value.command.ir.entities.tags).toEqual(['ux']);
    expect(split.value.tagReferenceNormalizationApplied).toBe(true);
    expect(h.callTool).not.toHaveBeenCalled();

    h.board.tags = [{ name: 'RD', count: 0 }, { name: 'R&D', count: 0 }];
    expect(() => prepareVoiceCreate({ ...base, tags: ['R D'] }, h.context(), [], boardTags(h))).toThrow('tag');
  });
  it('keeps strict tag matches authoritative and returns close matches only as suggestions', () => {
    const h = harness();
    const plan = { ...base, tags: ['Bugs'] };
    const strict = prepareVoiceCreate(plan, h.context(), [], [{ name: 'bug' }, { name: 'bugs' }]);
    expect(strict.kind).toBe('prepared');
    if (strict.kind !== 'prepared') return;
    expect(strict.value.command.ir.entities.tags).toEqual(['bugs']);

    const suggested = prepareVoiceCreate(plan, h.context(), [], [{ name: 'bug' }]);
    expect(suggested).toEqual({
      kind: 'tag-suggestion',
      suggestion: { referenceIndex: 0, reference: 'Bugs', tag: 'bug', kind: 'terminal_s' },
    });
    const accepted = prepareVoiceCreate(plan, h.context(), [], [{ name: 'bug' }], undefined, [{ referenceIndex: 0, tag: 'bug' }]);
    expect(accepted.kind === 'prepared' && accepted.value.command.ir.entities.tags).toEqual(['bug']);
    const recased = prepareVoiceCreate(plan, h.context(), [], [{ name: 'Bug' }], undefined, [{ referenceIndex: 0, tag: 'bug' }]);
    expect(recased.kind === 'prepared' && recased.value.command.ir.entities.tags).toEqual(['Bug']);
    expect(() => prepareVoiceCreate(plan, h.context(), [], [], undefined, [{ referenceIndex: 0, tag: 'bug' }])).toThrow('stale_context');
    expect(() => prepareVoiceCreate(plan, h.context(), [], [{ name: 'bugs' }], undefined, [{ referenceIndex: 0, tag: 'bug' }])).toThrow('stale_context');
  });
  it('fails closed for multiple equally close tag candidates', () => {
    const h = harness();
    try {
      prepareVoiceCreate({ ...base, tags: ['bock'] }, h.context(), [], [{ name: 'book' }, { name: 'back' }]);
      throw new Error('expected ambiguous tag failure');
    } catch (error) {
      expect(error).toMatchObject({ code: 'tag', details: { result: 'ambiguous', candidateCount: 2 } });
    }
  });
  it('accepts authoritative legacy-style labels exactly when server canonicalization accepts them', () => {
    const h = harness();
    h.board.tags = [{ name: 'make space', count: 0 }];
    const result = prepareVoiceCreate({ ...base, tags: ['make space'] }, h.context(), [], boardTags(h));
    expect(result.kind).toBe('prepared');
    if (result.kind !== 'prepared') return;
    expect(result.value.command.ir.entities.tags).toEqual(['make space']);
    expect(buildMcpCall(result.value.command.ir as never).input.tags).toEqual(['make space']);

    h.board.tags = [{ name: 'R&D', count: 0 }];
    expect(() => prepareVoiceCreate({ ...base, tags: ['R&D'] }, h.context(), [], boardTags(h))).toThrow('invalid_plan');
  });
  it('blocks unsupported content, missing titles, tags and unauthorized context', () => {
    const h = harness();
    for (const plan of [{ version: 1, kind: 'create' }, { ...base, tags: ['invented'] }, { ...base, unhandled: [{ text: 'schedule it', reason: 'unsupported' }] }]) {
      expect(() => prepareVoiceCreate(plan as VoiceCreatePlanV1, h.context(), [], boardTags(h))).toThrow();
    }
    expect(h.callTool).not.toHaveBeenCalled();
    h.context().role = 'viewer'; expect(() => prepareVoiceCreate(base, h.context(), [], [])).toThrow('unauthorized');
  });
  it('keeps legacy creates valid and rejects partial/invalid enriched fields', () => {
    const h = harness();
    const ir = { intent: 'todos.create', projectId: 1, projectSlug: 'alpha', entities: { title: 'Old', columnKey: 'backlog' } };
    expect(validateCommandIR(ir, h.context()).ok).toBe(true);
    expect(validateCommandIR({ ...ir, entities: { ...ir.entities, body: '', tags: ['New Tag'], assigneeUserId: null } }, h.context()).ok).toBe(true);
    for (const extra of [{ body: '' }, { body: '', tags: [], assigneeUserId: -1 }, { body: '', tags: ['R&D'], assigneeUserId: null }]) {
      expect(validateCommandIR({ ...ir, entities: { ...ir.entities, ...extra } }, h.context()).ok).toBe(false);
    }
  });
});
