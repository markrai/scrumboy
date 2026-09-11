// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { BoardMember } from '../state/state.js';
import type { Board } from '../types.js';
import type { VoiceCommandContext } from './command-context.js';
import { createVoiceCreateDryRunSession, evaluateVoiceCreateDryRun, type VoiceCreateDryRunOptions } from './voice-create-evaluation.js';
import type { VoiceCreatePlanResult, VoiceCreatePlanV1 } from './voice-create-plan.js';
import { createVoiceCreatePlanner, VOICE_CREATE_DRY_RUN_OUTPUT_PREVIEW_CODE_UNITS } from './voice-create-planner.js';

const members: BoardMember[] = [
  { userId: 8, name: 'Mark', email: 'mark@example.test', role: 'maintainer' },
  { userId: 9, name: 'Sarah', email: 'sarah@example.test', role: 'contributor' },
];

function makeBoard(): Board {
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', creatorUserId: 7, dominantColor: '#123456' },
    tags: [
      { tagId: 11, name: 'architecture', count: 0 },
      { tagId: 12, name: 'ux', count: 0 },
      { tagId: 13, name: 'backend', count: 0 },
      { tagId: 14, name: 'mobile', count: 0 },
    ],
    columnOrder: [
      { key: 'not_started', name: 'Not Started', isDone: false },
      { key: 'testing', name: 'Testing', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: { not_started: [], testing: [], done: [] },
  } as Board;
}

function harness(plan: VoiceCreatePlanResult, overrides: Partial<VoiceCreateDryRunOptions> = {}) {
  const board = makeBoard();
  let context: VoiceCommandContext | null = {
    userId: 7,
    projectId: 1,
    projectSlug: 'alpha',
    board,
    members,
    role: 'maintainer',
  };
  const planner = vi.fn(async () => plan);
  const refreshBoard = vi.fn(async () => undefined);
  const readMembers = vi.fn(async () => members);
  const readTags = vi.fn(async () => board.tags.map(tag => ({ name: tag.name })));
  const options: VoiceCreateDryRunOptions = {
    planner,
    getContext: () => context,
    refreshBoard,
    readMembers,
    readTags,
    ...overrides,
  };
  return {
    board,
    planner,
    refreshBoard,
    readMembers,
    readTags,
    options,
    setContext(value: VoiceCommandContext | null) { context = value; },
  };
}

const create = (fields: Partial<VoiceCreatePlanV1> = {}): VoiceCreatePlanV1 => ({
  version: 1,
  kind: 'create',
  title: 'Fred',
  ...fields,
});

describe('Voice Create dry-run v1', () => {
  it('keeps the evaluator transport-minimal while production and device adapters use the shared readers', () => {
    const evaluationSource = readFileSync(resolve('modules/voice/voice-create-evaluation.ts'), 'utf8');
    const deviceSource = readFileSync(resolve('modules/voice/voice-create-device-evaluation.ts'), 'utf8');
    const sessionSource = readFileSync(resolve('modules/voice/voice-create-session.ts'), 'utf8');
    const preparationSource = readFileSync(resolve('modules/voice/voice-create-prepare.ts'), 'utf8');
    const boardSource = readFileSync(resolve('modules/views/board.ts'), 'utf8');
    expect(evaluationSource).not.toMatch(/mcp-client|callMcpTool|members_list|apiFetch/);
    expect(deviceSource).toMatch(/readVoiceCreateMembers/);
    expect(deviceSource).toMatch(/readVoiceCreateTags/);
    expect(sessionSource).toMatch(/readVoiceCreateTags/);
    expect(preparationSource).toMatch(/reconcileVoiceCreateTagReferences/);
    expect(boardSource.match(/getVoiceCreateDryRunBoardPorts[\s\S]*?\n}/)?.[0] ?? '').not.toMatch(/context\.members/);
  });

  it('returns a structured ready create and exact confirmation summary', async () => {
    const f = harness(create());
    const result = await evaluateVoiceCreateDryRun('Create Fred', f.options);
    expect(result).toMatchObject({
      version: 1,
      outcome: 'ready',
      planner: { status: 'ok', plan: create() },
      preparation: {
        status: 'ready',
        title: 'Fred',
        lane: { key: 'not_started', name: 'Not Started', defaulted: true },
        assignee: null,
        tags: [],
        notesPresent: false,
        summary: 'Create "Fred" in Not Started',
      },
      confirmationReady: true,
      plannerCallCount: 1,
      mutationExecuted: false,
    });
  });

  it('has no mutation port and ignores an injected execute-shaped extra', async () => {
    const execute = vi.fn(() => { throw new Error('mutation reached'); });
    const f = harness(create());
    const result = await evaluateVoiceCreateDryRun('Create Fred', { ...f.options, execute } as VoiceCreateDryRunOptions);
    expect(result.mutationExecuted).toBe(false);
    expect(execute).not.toHaveBeenCalled();
    expect(f.readMembers).not.toHaveBeenCalled();
    expect(f.readTags).not.toHaveBeenCalled();
  });

  it('reports parser failure separately and never exposes raw Nano output', async () => {
    const raw = 'super-secret-nano-output';
    const generate = vi.fn(async (request: { requestId: string }) => ({ requestId: request.requestId, text: raw }));
    const f = harness(create(), { planner: createVoiceCreatePlanner({ generate } as never) });
    const result = await evaluateVoiceCreateDryRun('Create Fred', f.options);
    expect(result).toMatchObject({ outcome: 'planner_failed', planner: { status: 'failed', stage: 'planner_parsing', code: 'surrounding_prose' }, plannerCallCount: 1 });
    expect(JSON.stringify(result)).not.toContain(raw);
    expect(generate).toHaveBeenCalledOnce();
  });

  it('includes only a bounded debug dry-run preview for parser failures', async () => {
    const raw = `Sure! Here is the JSON:\n\`\`\`json\n{"version":1,"kind":"create","title":"Fred"}\n\`\`\` ${'x'.repeat(500)}`;
    const generate = vi.fn(async (request: { requestId: string }) => ({ requestId: request.requestId, text: raw }));
    const f = harness(create(), {
      planner: createVoiceCreatePlanner({ generate } as never, { includeDryRunParserOutputPreview: true }),
    });
    const result = await evaluateVoiceCreateDryRun('Create Fred', f.options);
    expect(result).toMatchObject({
      planner: {
        status: 'failed',
        stage: 'planner_parsing',
        code: 'surrounding_prose',
        details: { outputLength: raw.length, outputPreview: raw.slice(0, VOICE_CREATE_DRY_RUN_OUTPUT_PREVIEW_CODE_UNITS) },
      },
    });
    const preview = result.planner.status === 'failed' ? result.planner.details?.outputPreview : undefined;
    expect(preview).toHaveLength(VOICE_CREATE_DRY_RUN_OUTPUT_PREVIEW_CODE_UNITS);
    expect(JSON.stringify(result)).not.toContain(raw);
  });

  it('reports unknown and ambiguous tags distinctly', async () => {
    const unknown = harness(create({ tags: ['TotallyInventedTag'] }));
    await expect(evaluateVoiceCreateDryRun('Create Fred and tag it TotallyInventedTag', unknown.options)).resolves.toMatchObject({
      outcome: 'resolution_failed',
      preparation: { status: 'failed', stage: 'tag_resolution', code: 'unknown_tag', details: { result: 'unavailable', candidateCount: 0 } },
    });
    const ambiguous = harness(create({ tags: ['R D'] }));
    ambiguous.board.tags = [{ tagId: 20, name: 'RD', count: 0 }, { tagId: 21, name: 'R&D', count: 0 }];
    await expect(evaluateVoiceCreateDryRun('Create Fred and tag it R D', ambiguous.options)).resolves.toMatchObject({
      outcome: 'resolution_failed',
      preparation: { status: 'failed', stage: 'tag_resolution', code: 'ambiguous_tag', details: { result: 'ambiguous', candidateCount: 2 } },
    });
  });

  it('reports unknown lane and unknown member at their resolution stages', async () => {
    const lane = harness(create({ lane: 'TotallyInventedLane' }));
    await expect(evaluateVoiceCreateDryRun('Create Fred in TotallyInventedLane', lane.options)).resolves.toMatchObject({
      preparation: { stage: 'lane_resolution', code: 'unknown_lane' },
    });
    const member = harness(create({ assignee: 'SomeoneWhoDoesNotExist' }));
    await expect(evaluateVoiceCreateDryRun('Create Fred for SomeoneWhoDoesNotExist', member.options)).resolves.toMatchObject({
      preparation: { stage: 'member_resolution', code: 'unknown_member' },
    });
  });

  it('resolves an assignee by authoritative email from the injected member reader', async () => {
    const f = harness(create({ assignee: 'mark.rai@example.test' }), {
      readMembers: async () => [
        { userId: 8, name: 'Mark Rai', email: 'mark.rai@example.test', role: 'maintainer' },
      ],
    });
    const result = await evaluateVoiceCreateDryRun('Create Fred for mark.rai@example.test', f.options);
    expect(result).toMatchObject({
      outcome: 'ready',
      preparation: {
        assignee: { userId: 8, name: 'Mark Rai' },
        summary: expect.stringContaining('Assign Mark Rai · mark.rai@example.test'),
      },
      plannerCallCount: 1,
      mutationExecuted: false,
    });
    expect(JSON.stringify(result)).not.toContain('undefined');
  });

  it('reports ambiguous member resolution without asking for a selection', async () => {
    const f = harness(create({ assignee: 'Mark' }), {
      readMembers: async () => [
        { userId: 8, name: 'Mark Smith', email: 'mark.smith@example.test', role: 'maintainer' },
        { userId: 10, name: 'Mark Stone', email: 'mark.stone@example.test', role: 'contributor' },
      ],
    });
    await expect(evaluateVoiceCreateDryRun('Create Fred for Mark', f.options)).resolves.toMatchObject({
      outcome: 'resolution_failed',
      preparation: { stage: 'member_resolution', code: 'ambiguous_member', details: { candidateCount: 2 } },
      confirmationReady: false,
    });
  });

  it('preserves explicit and authoritative default lanes', async () => {
    const explicit = harness(create({ lane: 'Testing' }));
    await expect(evaluateVoiceCreateDryRun('Create Fred in Testing', explicit.options)).resolves.toMatchObject({
      preparation: { status: 'ready', lane: { key: 'testing', name: 'Testing', defaulted: false } },
    });
    const defaulted = harness(create());
    await expect(evaluateVoiceCreateDryRun('Create Fred', defaulted.options)).resolves.toMatchObject({
      preparation: { status: 'ready', lane: { key: 'not_started', name: 'Not Started', defaulted: true } },
    });
  });

  it.each([
    ['mobile', 'mobile'],
    ['UX', 'ux'],
    ['U.X.', 'ux'],
    ['Architecture', 'architecture'],
  ])('resolves spoken tag %s to authoritative %s', async (reference, authoritative) => {
    const f = harness(create({ tags: [reference] }));
    await expect(evaluateVoiceCreateDryRun(`Create Fred and tag it ${reference}`, f.options)).resolves.toMatchObject({
      preparation: { status: 'ready', tags: [authoritative] },
    });
  });

  it('keeps split Nano tags visible while deterministically preparing one authoritative acronym', async () => {
    const plan = create({ tags: ['U', 'X'] });
    const f = harness(plan);
    await expect(evaluateVoiceCreateDryRun('Create Fred and tag it U X', f.options)).resolves.toMatchObject({
      outcome: 'ready',
      planner: { status: 'ok', plan: { tags: ['U', 'X'] } },
      preparation: { status: 'ready', tags: ['ux'] },
      confirmationReady: true,
      plannerCallCount: 1,
      mutationExecuted: false,
    });
    expect(f.planner).toHaveBeenCalledOnce();
  });

  it('resolves a personal cross-project tag independently of the current filtered board payload', async () => {
    const f = harness(create({ tags: ['Architecture'] }), {
      readTags: async () => [{ name: 'architecture' }, { name: 'mobile' }],
    });
    f.board.tags = [{ tagId: 12, name: 'mobile', count: 0 }];
    f.board.columns = { not_started: [], testing: [], done: [] };

    await expect(evaluateVoiceCreateDryRun('Create Fred and tag it Architecture', f.options)).resolves.toMatchObject({
      outcome: 'ready',
      preparation: { status: 'ready', tags: ['architecture'] },
      plannerCallCount: 1,
      mutationExecuted: false,
    });
  });

  it('keeps the single-turn dry-run non-interactive when a session-level tag suggestion is available', async () => {
    const f = harness(create({ tags: ['Bugs'] }), {
      readTags: async () => [{ name: 'bug' }],
    });
    await expect(evaluateVoiceCreateDryRun('Create Fred and tag it Bugs', f.options)).resolves.toMatchObject({
      outcome: 'resolution_failed',
      planner: { status: 'ok', plan: { tags: ['Bugs'] } },
      preparation: { status: 'failed', stage: 'tag_resolution', code: 'unknown_tag' },
      confirmationReady: false,
      plannerCallCount: 1,
      mutationExecuted: false,
    });
    expect(f.planner).toHaveBeenCalledOnce();
  });

  it('returns one combined assignee, tag, lane and notes preparation', async () => {
    const plan = create({ title: 'Refactor Navigation', lane: 'Testing', assignee: 'Mark', tags: ['Architecture', 'UX'], notes: 'revisit the keyboard flow' });
    const f = harness(plan);
    await expect(evaluateVoiceCreateDryRun('combined request', f.options)).resolves.toMatchObject({
      preparation: {
        status: 'ready',
        title: 'Refactor Navigation',
        lane: { name: 'Testing', defaulted: false },
        assignee: { userId: 8, name: 'Mark' },
        tags: ['architecture', 'ux'],
        notesPresent: true,
      },
      plannerCallCount: 1,
      mutationExecuted: false,
    });
    expect(f.readMembers).toHaveBeenCalledOnce();
    expect(f.readTags).toHaveBeenCalledOnce();
  });

  it('prepares the physically observed Jonas field wording with a controlled planner result', async () => {
    const plan = create({ title: 'Jonas', assignee: 'Mark', tags: ['architecture'] });
    const f = harness(plan);
    const transcript = 'uh, create a story called Jonas, and assigned to Mark and also make the tag architecture';

    await expect(evaluateVoiceCreateDryRun(transcript, f.options)).resolves.toMatchObject({
      outcome: 'ready',
      planner: { status: 'ok', plan },
      preparation: {
        status: 'ready',
        title: 'Jonas',
        assignee: { userId: 8, name: 'Mark' },
        tags: ['architecture'],
      },
      confirmationReady: true,
      plannerCallCount: 1,
      mutationExecuted: false,
    });
    expect(f.planner).toHaveBeenCalledOnce();
  });

  it('returns parsed planner visibility when unsupported semantics block preparation', async () => {
    const plan = create({ unhandled: [{ text: 'schedule it for Tuesday', reason: 'unsupported' }] });
    const f = harness(plan);
    const result = await evaluateVoiceCreateDryRun('Create Fred and schedule it for Tuesday', f.options);
    expect(result).toMatchObject({
      outcome: 'blocked',
      planner: { status: 'ok', plan },
      preparation: { stage: 'unsupported_request_guard', code: 'incomplete_request' },
      confirmationReady: false,
    });
  });

  it('does not share state across sequential dry-runs', async () => {
    const f = harness(create());
    const planner = vi.fn()
      .mockResolvedValueOnce(create({ title: 'First' }))
      .mockResolvedValueOnce(create({ title: 'Second', lane: 'Testing' }));
    const options = { ...f.options, planner };
    const first = await evaluateVoiceCreateDryRun('Create First', options);
    const second = await evaluateVoiceCreateDryRun('Create Second in Testing', options);
    expect(first).toMatchObject({ preparation: { status: 'ready', title: 'First' }, plannerCallCount: 1 });
    expect(second).toMatchObject({ preparation: { status: 'ready', title: 'Second', lane: { name: 'Testing' } }, plannerCallCount: 1 });
  });

  it('makes exactly one generation and never performs repair generation', async () => {
    const generate = vi.fn(async (request: { requestId: string }) => ({ requestId: request.requestId, text: JSON.stringify(create()) }));
    const f = harness(create(), { planner: createVoiceCreatePlanner({ generate } as never) });
    const result = await evaluateVoiceCreateDryRun('Create Fred', f.options);
    expect(result).toMatchObject({ outcome: 'ready', plannerCallCount: 1 });
    expect(JSON.stringify(result)).not.toContain('outputPreview');
    expect(generate).toHaveBeenCalledOnce();
  });

  it('reports provider unavailability without calling the planner or falling back', async () => {
    const f = harness(create(), { provider: { status: async () => ({ state: 'temporarily-unavailable', reason: 'busy' }) } });
    const result = await evaluateVoiceCreateDryRun('Create Fred', f.options);
    expect(result).toMatchObject({
      outcome: 'planner_failed',
      planner: { stage: 'provider_readiness', code: 'provider_unavailable', details: { providerState: 'temporarily-unavailable', providerReason: 'busy' } },
      plannerCallCount: 0,
    });
    expect(f.planner).not.toHaveBeenCalled();
  });

  it('reports a bounded planner timeout', async () => {
    const f = harness(create(), { planner: (_text, signal) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true })), timeoutMs: 5 });
    await expect(evaluateVoiceCreateDryRun('Create Fred', f.options)).resolves.toMatchObject({
      planner: { stage: 'planner_invocation', code: 'planner_timeout' },
      plannerCallCount: 1,
      mutationExecuted: false,
    });
  });

  it('hard-times a completely non-cooperative planner', async () => {
    vi.useFakeTimers();
    try {
      const planner = vi.fn(async () => new Promise<VoiceCreatePlanResult>(() => {}));
      const f = harness(create(), { planner, timeoutMs: 5 });
      const pending = evaluateVoiceCreateDryRun('Create Fred', f.options);
      await vi.advanceTimersByTimeAsync(5);
      await expect(pending).resolves.toMatchObject({
        outcome: 'planner_failed',
        planner: { status: 'failed', stage: 'planner_invocation', code: 'planner_timeout' },
        plannerCallCount: 1,
        mutationExecuted: false,
      });
      expect(planner).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it('poisons a timed-out device session and never overlaps planner generations', async () => {
    vi.useFakeTimers();
    try {
      let active = 0;
      let maximumActive = 0;
      const planner = vi.fn(async () => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        return new Promise<VoiceCreatePlanResult>(() => {});
      });
      const f = harness(create(), { planner, timeoutMs: 5 });
      const session = createVoiceCreateDryRunSession((input, options) => evaluateVoiceCreateDryRun(input, {
        ...f.options,
        timeoutMs: options?.timeoutMs,
      }));

      const first = session('Create First', { timeoutMs: 5 });
      await expect(session('Create Overlap', { timeoutMs: 5 })).resolves.toMatchObject({
        planner: { status: 'failed', stage: 'provider_readiness', code: 'provider_session_busy' },
        plannerCallCount: 0,
        mutationExecuted: false,
      });
      await vi.advanceTimersByTimeAsync(5);
      await expect(first).resolves.toMatchObject({
        planner: { code: 'planner_timeout' },
        plannerCallCount: 1,
        mutationExecuted: false,
      });
      await expect(session('Create Second', { timeoutMs: 5 })).resolves.toMatchObject({
        planner: { status: 'failed', stage: 'provider_readiness', code: 'provider_session_poisoned' },
        plannerCallCount: 0,
        mutationExecuted: false,
      });
      expect(planner).toHaveBeenCalledOnce();
      expect(maximumActive).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('fails closed when current authorization or context is unavailable', async () => {
    const f = harness(create());
    f.setContext(null);
    await expect(evaluateVoiceCreateDryRun('Create Fred', f.options)).resolves.toMatchObject({
      outcome: 'context_failed',
      planner: { stage: 'context_authorization', code: 'context_unavailable' },
      plannerCallCount: 0,
    });
  });
});
