import { describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';
import {
  LocalTextGenerationError,
  type LocalTextGenerationCapability,
} from '../platform/local-text-generation.js';
import {
  diagnoseVoiceInterpretation,
  runInterpretationLabBatch,
  type InterpretationLabResolveContext,
} from './interpretation-diagnostics.js';
import {
  CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS,
  CANDIDATE_VOICE_INTERPRETATION_PROMPT_VERSION,
  EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS,
  EXPERIMENTAL_VOICE_INTERPRETATION_PROMPT_VERSION,
  INTERPRETATION_LAB_CANDIDATE_PROFILE,
  INTERPRETATION_LAB_CURRENT_PROFILE,
  INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
} from './interpretation-lab-prompts.js';
import {
  VOICE_INTERPRETATION_INSTRUCTIONS,
  VOICE_INTERPRETATION_PROMPT_VERSION,
} from './local-interpretation.js';

const members: BoardMember[] = [
  { userId: 7, name: 'Ada', email: 'ada@example.com', role: 'maintainer' },
];

function board(): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 7,
    },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: {
      backlog: [{ id: 10, localId: 353, title: 'Bogus', status: 'backlog' }],
      done: [],
    },
  };
}

function context(): InterpretationLabResolveContext {
  return {
    projectId: 1,
    projectSlug: 'alpha',
    board: board(),
    members,
  };
}

function capability(
  output: string | ((input: string) => string),
): LocalTextGenerationCapability & {
  status: ReturnType<typeof vi.fn>;
  generate: ReturnType<typeof vi.fn>;
} {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready', maximumOutputTokens: 256 }),
    prepare: vi.fn().mockResolvedValue(undefined),
    generate: vi.fn().mockImplementation(async (request) => ({
      requestId: request.requestId,
      text: typeof output === 'function' ? output(request.input) : output,
    })),
  };
}

describe('temporary VoiceFlow interpretation diagnostics', () => {
  it('selects exact current-v2, unchanged experimental-v3, and separate candidate-v3 instructions', async () => {
    const local = capability('{"command":"create todo Clean the garage","unrepresented":[]}');

    await diagnoseVoiceInterpretation({
      input: 'Create a to-do about cleaning the garage',
      profile: INTERPRETATION_LAB_CURRENT_PROFILE,
      capability: local,
      getResolveContext: context,
    });

    expect(VOICE_INTERPRETATION_PROMPT_VERSION).toBe('voice-command-canonical-v2');
    expect(local.generate).toHaveBeenCalledWith(expect.objectContaining({
      instructions: VOICE_INTERPRETATION_INSTRUCTIONS,
    }));
    expect(local.generate.mock.calls[0][0].instructions)
      .not.toBe(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS);

    await diagnoseVoiceInterpretation({
      input: 'Create a to-do about cleaning the garage',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: local,
      getResolveContext: context,
    });
    await diagnoseVoiceInterpretation({
      input: 'Create a to-do about cleaning the garage',
      profile: INTERPRETATION_LAB_CANDIDATE_PROFILE,
      capability: local,
      getResolveContext: context,
    });

    expect(local.generate.mock.calls[1][0].instructions)
      .toBe(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS);
    expect(local.generate.mock.calls[2][0].instructions)
      .toBe(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS);
  });

  it('makes the temporary prompt punctuation-independent and natural-language aware without domain authority', () => {
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_PROMPT_VERSION)
      .toBe('voice-command-natural-v3-experimental');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Scrumboy, a task and kanban application');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('speech-transcribed English');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('without depending on colons');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Infer meaning');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Never invent or rename domain entities');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('project IDs');
    expect(EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('unrepresented');
  });

  it('defines the production-candidate prompt as natural-language aware and semantically fail-closed', () => {
    expect(CANDIDATE_VOICE_INTERPRETATION_PROMPT_VERSION)
      .toBe('voice-command-natural-v3-candidate');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Scrumboy, a task and kanban application');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS).toContain('speech-transcribed English');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Do not rely on colons, commas, periods, quotation marks, capitalization');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('normalize the user-authored content into a concise natural task title');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('preserve identity');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('CRITICAL SEMANTIC COMPLETENESS RULE');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('If the user requests two or more actions, do not choose');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":null,"unrepresented":["Open and delete Bogus"]}');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":"create todo Fix the bathroom","unrepresented":["by 6:00 p.m."]}');
    expect(CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Never invent or rename authoritative domain entities');
  });

  it('preserves exact raw output and distinguishes recognized semantic residue and model refusal', async () => {
    const recognizedRaw = '```json\n{"command":"create todo Clean the garage","unrepresented":[]}\n```';
    const recognized = await diagnoseVoiceInterpretation({
      input: 'Create a to-do about cleaning the garage',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: capability(recognizedRaw),
      getResolveContext: context,
    });
    expect(recognized).toMatchObject({
      rawOutput: recognizedRaw,
      provider: 'ok',
      envelope: { state: 'candidate', command: 'create todo Clean the garage', unrepresented: [] },
      canonicalParse: { state: 'ok' },
      resolution: { state: 'ok' },
      finalClassification: 'recognized',
    });

    const partial = await diagnoseVoiceInterpretation({
      input: 'Create a to-do about cleaning the garage today',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: capability('{"command":"create todo Clean the garage","unrepresented":["today"]}'),
      getResolveContext: context,
    });
    expect(partial).toMatchObject({
      envelope: { state: 'refused', command: 'create todo Clean the garage', unrepresented: ['today'] },
      canonicalParse: { state: 'ok' },
      resolution: { state: 'ok' },
      finalClassification: 'partial',
    });

    const refused = await diagnoseVoiceInterpretation({
      input: 'Open and delete Bogus',
      profile: INTERPRETATION_LAB_CURRENT_PROFILE,
      capability: capability('{"command":null,"unrepresented":[]}'),
      getResolveContext: context,
    });
    expect(refused).toMatchObject({
      envelope: { state: 'refused', command: null, unrepresented: [] },
      canonicalParse: { state: 'not-run' },
      resolution: { state: 'not-run' },
      finalClassification: 'model-refused',
    });
  });

  it('distinguishes envelope preservation canonical and resolution rejection stages', async () => {
    const invalidEnvelope = await diagnoseVoiceInterpretation({
      input: 'Open Bogus',
      profile: INTERPRETATION_LAB_CURRENT_PROFILE,
      capability: capability('not json'),
      getResolveContext: context,
    });
    expect(invalidEnvelope).toMatchObject({
      envelope: { state: 'rejected', reason: 'envelope-or-safety-contract' },
      finalClassification: 'output-rejected',
    });

    const preservation = await diagnoseVoiceInterpretation({
      input: 'Could you move Bogus to Done?',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: capability('{"command":"move todo Garage Cleanup to Done","unrepresented":[]}'),
      getResolveContext: context,
    });
    expect(preservation).toMatchObject({
      envelope: { state: 'rejected', reason: 'candidate-preservation' },
      finalClassification: 'output-rejected',
    });

    const canonical = await diagnoseVoiceInterpretation({
      input: 'Create a todo to do this in the project',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: capability('{"command":"create todo Do this in the project","unrepresented":[]}'),
      getResolveContext: context,
    });
    expect(canonical).toMatchObject({
      canonicalParse: { state: 'failure', code: 'project_scope' },
      resolution: { state: 'not-run' },
      finalClassification: 'canonical-rejected',
    });

    const resolution = await diagnoseVoiceInterpretation({
      input: 'Open Missing',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: capability('{"command":"open todo Missing","unrepresented":[]}'),
      getResolveContext: context,
    });
    expect(resolution).toMatchObject({
      canonicalParse: { state: 'ok' },
      resolution: { state: 'failure', code: 'unknown_story' },
      finalClassification: 'resolution-failed',
    });
  });

  it('records resolution as not-run when current board context is unavailable', async () => {
    const result = await diagnoseVoiceInterpretation({
      input: 'Create a todo to clean the garage',
      profile: INTERPRETATION_LAB_CURRENT_PROFILE,
      capability: capability('{"command":"create todo Clean the garage","unrepresented":[]}'),
      getResolveContext: () => null,
    });
    expect(result).toMatchObject({
      canonicalParse: { state: 'ok' },
      resolution: { state: 'not-run' },
      finalClassification: 'recognized',
    });
  });

  it('classifies provider failure separately without retrying', async () => {
    const local = capability('{"command":null,"unrepresented":[]}');
    local.generate.mockRejectedValue(new Error('provider detail must not escape'));

    const result = await diagnoseVoiceInterpretation({
      input: 'Create a todo to clean the garage',
      profile: INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
      capability: local,
      getResolveContext: context,
    });

    expect(result).toMatchObject({
      provider: 'error',
      envelope: { state: 'rejected', reason: 'provider:internal' },
      canonicalParse: { state: 'not-run' },
      resolution: { state: 'not-run' },
      finalClassification: 'provider-error',
    });
    expect(JSON.stringify(result)).not.toContain('provider detail must not escape');
    expect(local.generate).toHaveBeenCalledTimes(1);
  });

  it('runs all profiles once strictly serially in input-major current-experimental-candidate order', async () => {
    let active = 0;
    let maximumActive = 0;
    const order: string[] = [];
    const local = capability((input) => JSON.stringify({
      command: input.includes('dentist') ? 'create todo Call the dentist' : 'create todo Clean the garage',
      unrepresented: [],
    }));
    local.generate.mockImplementation(async (request) => {
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      const profile = request.instructions === VOICE_INTERPRETATION_INSTRUCTIONS
        ? 'current'
        : request.instructions === EXPERIMENTAL_VOICE_INTERPRETATION_INSTRUCTIONS
          ? 'experimental'
          : request.instructions === CANDIDATE_VOICE_INTERPRETATION_INSTRUCTIONS
            ? 'candidate'
            : 'unknown';
      order.push(`${request.input}|${profile}`);
      await Promise.resolve();
      active -= 1;
      return {
        requestId: request.requestId,
        text: JSON.stringify({
          command: request.input.includes('dentist') ? 'create todo Call the dentist' : 'create todo Clean the garage',
          unrepresented: [],
        }),
      };
    });

    const results = await runInterpretationLabBatch({
      inputs: ['Clean the garage', 'Call the dentist'],
      profiles: [
        INTERPRETATION_LAB_CURRENT_PROFILE,
        INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
        INTERPRETATION_LAB_CANDIDATE_PROFILE,
      ],
      capability: local,
      getResolveContext: context,
    });

    expect(maximumActive).toBe(1);
    expect(order).toEqual([
      'Clean the garage|current',
      'Clean the garage|experimental',
      'Clean the garage|candidate',
      'Call the dentist|current',
      'Call the dentist|experimental',
      'Call the dentist|candidate',
    ]);
    expect(results).toHaveLength(6);
    expect(local.generate).toHaveBeenCalledTimes(6);
  });

  it('aborts the active generation and never starts queued items', async () => {
    const controller = new AbortController();
    const local = capability('{"command":null,"unrepresented":[]}');
    local.generate.mockImplementation((request) => new Promise((_resolve, reject) => {
      request.signal?.addEventListener('abort', () => reject(new LocalTextGenerationError('cancelled')), { once: true });
    }));

    const running = runInterpretationLabBatch({
      inputs: ['First', 'Second'],
      profiles: [INTERPRETATION_LAB_CURRENT_PROFILE, INTERPRETATION_LAB_EXPERIMENTAL_PROFILE],
      capability: local,
      signal: controller.signal,
    });
    await vi.waitFor(() => expect(local.generate).toHaveBeenCalledTimes(1));
    controller.abort();

    await expect(running).resolves.toMatchObject([{ finalClassification: 'cancelled' }]);
    expect(local.generate).toHaveBeenCalledTimes(1);
  });
});
