import { describe, expect, it, vi } from 'vitest';
import { executableCreatePlan, parseVoiceCreatePlan, guardCreateRequest, type VoiceCreatePlanV1 } from './voice-create-plan.js';
import { createVoiceCreatePlanner, VOICE_CREATE_PLANNER_VERSION } from './voice-create-planner.js';

const base = { version: 1, kind: 'create', title: 'Big Man' };
describe('bounded semantic create contract', () => {
  it('accepts all fields without inventing defaults or identities', () => {
    expect(parseVoiceCreatePlan(JSON.stringify(base))).toEqual(base);
    const all = { ...base, lane: 'Backlog', assignee: 'Mark', tags: ['urgent'], notes: 'Call tomorrow' };
    expect(parseVoiceCreatePlan(JSON.stringify(all))).toEqual(all);
    expect(parseVoiceCreatePlan('{"version":1,"kind":"not_create"}')).toEqual({ version: 1, kind: 'not_create' });
  });
  it.each(['projectId', 'projectSlug', 'id', 'actionId', 'columnKey', 'assigneeUserId', 'summary', 'authorized'])('rejects model authority field %s', field => {
    expect(() => parseVoiceCreatePlan(JSON.stringify({ ...base, [field]: 'invented' }))).toThrow();
  });
  it.each([
    { ...base, version: 2 }, { ...base, kind: 'finish' }, { ...base, title: ' ' }, { ...base, title: 'é'.repeat(101) },
    { ...base, title: 'x'.repeat(201) }, { ...base, lane: '' }, { ...base, assignee: null },
    { ...base, tags: Array(6).fill('urgent') }, { ...base, tags: [1] }, { ...base, notes: 'x'.repeat(1001) },
    { ...base, unhandled: [{ text: 'schedule it', reason: 'oops' }] },
    { version: 1, kind: 'not_create', title: 'Big Man' },
  ])('rejects invalid structure %#', value => expect(() => parseVoiceCreatePlan(JSON.stringify(value))).toThrow());
  it.each(['bad', '[]', 'null', '```json\n{"version":1,"kind":"create"}\n```', `${JSON.stringify(base)} extra`, 'x'.repeat(8193)])('rejects malformed/prose output %#', raw => expect(() => parseVoiceCreatePlan(raw)).toThrow());
  it.each([
    ['bad-json', '{"version":1,', 'invalid_json'], ['wrong-version', '{"version":2,"kind":"create"}', 'wrong_version'],
    ['unknown-fields', '{"version":1,"kind":"create","title":"Big Man","project":"x"}', 'unknown_fields'],
    ['invalid-tags', '{"version":1,"kind":"create","title":"Big Man","tags":[1]}', 'invalid_tags'],
  ])('returns bounded structural diagnostic %s', (_name, raw, code) => {
    try { parseVoiceCreatePlan(raw); throw new Error('expected parser failure'); } catch (error) { expect(error).toMatchObject({ code }); }
  });
  it('preserves missing-title intent but blocks execution, as it does material unhandled', () => {
    expect(parseVoiceCreatePlan('{"version":1,"kind":"create"}')).toEqual({ version: 1, kind: 'create' });
    expect(() => executableCreatePlan({ version: 1, kind: 'create' })).toThrow('missing_title');
    expect(() => executableCreatePlan({ ...base, unhandled: [{ text: 'schedule Tuesday', reason: 'unsupported' }] } as VoiceCreatePlanV1)).toThrow('incomplete_request');
  });
  it('blocks obvious dropped unsupported clauses without reinterpreting literal title/notes', () => {
    expect(() => guardCreateRequest(base as VoiceCreatePlanV1, 'Create Big Man and schedule it for Tuesday')).toThrow();
    expect(() => guardCreateRequest({ ...base, title: 'Schedule Tuesday', notes: 'Remind the customer' } as VoiceCreatePlanV1, 'Create a card called Schedule Tuesday with notes Remind the customer')).not.toThrow();
  });
});
describe('one inference provider', () => {
  it('sends the complete thought once, no board facts, no iterative completion', async () => {
    const generate = vi.fn(async request => ({ requestId: request.requestId, text: JSON.stringify(base) }));
    const planner = createVoiceCreatePlanner({ generate });
    const transcript = '  Hey, create a story. Call it Big Man.  ';
    await expect(planner(transcript, new AbortController().signal)).resolves.toEqual(base);
    expect(generate).toHaveBeenCalledOnce();
    expect(generate.mock.calls[0][0]).toMatchObject({ input: transcript, maximumOutputTokens: 256 });
    expect(generate.mock.calls[0][0].requestId).toContain(VOICE_CREATE_PLANNER_VERSION);
  });
  it('rejects stale IDs and malformed output without repair inference', async () => {
    for (const response of [{ requestId: 'wrong', text: JSON.stringify(base) }, { text: 'bad' }]) {
      const generate = vi.fn(async request => ({ requestId: request.requestId, ...response }));
      await expect(createVoiceCreatePlanner({ generate })('Create Big Man', new AbortController().signal)).rejects.toThrow();
      expect(generate).toHaveBeenCalledOnce();
    }
  });
  it('attaches only bounded parser diagnostics to malformed planner output', async () => {
    const generate = vi.fn(async request => ({ requestId: request.requestId, text: '{"version":1,"kind":"create","project":"hidden"}' }));
    await expect(createVoiceCreatePlanner({ generate })('Create Big Man', new AbortController().signal))
      .rejects.toMatchObject({ code: 'unknown_fields', details: { outputLength: expect.any(Number), unexpectedFields: ['project'] } });
    expect(JSON.stringify(generate.mock.results)).not.toContain('hidden');
  });
});
