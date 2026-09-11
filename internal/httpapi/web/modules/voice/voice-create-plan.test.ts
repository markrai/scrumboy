import { describe, expect, it, vi } from 'vitest';
import { executableCreatePlan, parseVoiceCreatePlan, guardCreateRequest, VOICE_CREATE_LIMITS, type VoiceCreatePlanV1 } from './voice-create-plan.js';
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
  it.each(['bad', '[]', 'null', `${JSON.stringify(base)} extra`, 'x'.repeat(8193)])('rejects malformed/prose output %#', raw => expect(() => parseVoiceCreatePlan(raw)).toThrow());
  it('accepts only a complete plain or JSON-labelled triple-backtick fence', () => {
    const json = JSON.stringify(base);
    expect(parseVoiceCreatePlan(json)).toEqual(base);
    expect(parseVoiceCreatePlan(`\`\`\`json\n${json}\n\`\`\``)).toEqual(base);
    expect(parseVoiceCreatePlan(`\`\`\`\n${json}\n\`\`\``)).toEqual(base);
    expect(parseVoiceCreatePlan(` \r\n\`\`\`JSON\r\n\r\n  ${json}  \r\n\r\n\`\`\`\n `)).toEqual(base);
  });
  it.each([
    ['prose before', `Sure!\n\`\`\`json\n${JSON.stringify(base)}\n\`\`\``],
    ['prose after', `\`\`\`json\n${JSON.stringify(base)}\n\`\`\`\nDone!`],
    ['prose around', `prefix\n\`\`\`\n${JSON.stringify(base)}\n\`\`\`\nsuffix`],
    ['unsupported language', `\`\`\`javascript\n${JSON.stringify(base)}\n\`\`\``],
    ['missing closing fence', `\`\`\`json\n${JSON.stringify(base)}`],
  ])('rejects %s as surrounding prose', (_name, raw) => {
    expect(() => parseVoiceCreatePlan(raw)).toThrow(expect.objectContaining({ code: 'surrounding_prose' }));
  });
  it.each([
    ['malformed JSON', '```json\n{"version":1,\n```', 'invalid_json'],
    ['an array', '```json\n[]\n```', 'surrounding_prose'],
    ['an unknown field', '```json\n{"version":1,"kind":"create","title":"Fred","project":"x"}\n```', 'unknown_fields'],
    ['two objects', '```json\n{"version":1,"kind":"create"}{"version":1,"kind":"create"}\n```', 'invalid_json'],
  ])('preserves strict validation for %s inside a valid fence', (_name, raw, code) => {
    expect(() => parseVoiceCreatePlan(raw)).toThrow(expect.objectContaining({ code }));
  });
  it('enforces the output limit before removing a valid fence', () => {
    const oversized = `   ${' '.repeat(VOICE_CREATE_LIMITS.output - JSON.stringify(base).length)}\`\`\`json\n${JSON.stringify(base)}\n\`\`\``;
    expect(oversized.length).toBeGreaterThan(VOICE_CREATE_LIMITS.output);
    expect(() => parseVoiceCreatePlan(oversized)).toThrow(expect.objectContaining({ code: 'output_too_large' }));
  });
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
  it.each([
    'Create Jonas and make the tag architecture',
    'Create Jonas and also make the tag architecture',
    'Create Jonas and make it architecture',
    'Create Jonas and add the architecture tag',
    'Create Jonas and assign it to Mark',
    'uh, create a story called Jonas, and assigned to Mark and also make the tag architecture',
  ])('does not mistake field wording for another mutation: %s', transcript => {
    expect(() => guardCreateRequest({ ...base, title: 'Jonas' } as VoiceCreatePlanV1, transcript)).not.toThrow();
  });
  it.each([
    'Create Big Man and delete Bob',
    'Create Big Man and move Bob to Done',
    'Create Big Man and open Bob',
    'Create Big Man and rename Bob',
    'Create Big Man and make another story called Alice',
    'Create Big Man and create another todo called Alice',
  ])('still blocks an additional Todo mutation: %s', transcript => {
    expect(() => guardCreateRequest(base as VoiceCreatePlanV1, transcript)).toThrow(expect.objectContaining({ code: 'incomplete_request' }));
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
  it('accepts fenced output in one generation through both production and dry-run planner modes', async () => {
    const fenced = `\`\`\`json\n${JSON.stringify(base)}\n\`\`\``;
    for (const options of [undefined, { includeDryRunParserOutputPreview: true }]) {
      const generate = vi.fn(async request => ({ requestId: request.requestId, text: fenced }));
      const planner = options
        ? createVoiceCreatePlanner({ generate }, options)
        : createVoiceCreatePlanner({ generate });
      await expect(planner('Create Big Man', new AbortController().signal)).resolves.toEqual(base);
      expect(generate).toHaveBeenCalledOnce();
    }
  });
  it('keeps output previews out of production planning and non-parser failures', async () => {
    const prose = 'Sure! {"version":1,"kind":"create","title":"Big Man"}';
    const productionGenerate = vi.fn(async request => ({ requestId: request.requestId, text: prose }));
    await expect(createVoiceCreatePlanner({ generate: productionGenerate })('Create Big Man', new AbortController().signal))
      .rejects.toMatchObject({ code: 'surrounding_prose', details: { outputLength: prose.length } });
    try {
      await createVoiceCreatePlanner({ generate: productionGenerate })('Create Big Man', new AbortController().signal);
    } catch (error) {
      expect(error).not.toMatchObject({ details: { outputPreview: expect.anything() } });
    }

    const contract = '{"version":1,"kind":"create","title":"Big Man","project":"hidden"}';
    const debugGenerate = vi.fn(async request => ({ requestId: request.requestId, text: contract }));
    try {
      await createVoiceCreatePlanner(
        { generate: debugGenerate },
        { includeDryRunParserOutputPreview: true },
      )('Create Big Man', new AbortController().signal);
    } catch (error) {
      expect(error).toMatchObject({ code: 'unknown_fields', details: { outputLength: contract.length } });
      expect(error).not.toMatchObject({ details: { outputPreview: expect.anything() } });
    }
  });
});
