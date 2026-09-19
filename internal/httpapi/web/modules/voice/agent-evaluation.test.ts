// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Board, Todo } from '../types.js';
import { VOICE_AGENT_EVALUATION_CASES, VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS } from './agent-evaluation-corpus.js';
import {
  evaluateVoiceAgentCorpus,
  evaluateVoiceAgentUtterance,
  parseUnrecoveredModelEnvelope,
  scoreVoiceAgentEnvelope,
  voiceAgentEvaluationCaseApplicability,
  type VoiceAgentEvaluationExpected,
} from './agent-evaluation.js';
import { extractHighConfidenceMoveCall } from './agent-move-extraction.js';

const corpus = JSON.parse(readFileSync(resolve('scripts/voice-agent-evaluation.json'), 'utf8')) as {
  boardRequirements: typeof VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS;
  cases: ReadonlyArray<{ id: string; transcript: string; expected: VoiceAgentEvaluationExpected }>;
};

function todo(localId: number, title: string): Todo {
  return { id: localId, localId, title, status: 'backlog', columnKey: 'backlog' };
}

function board(): Board {
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', creatorUserId: 7, dominantColor: '#123456' },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: {
      backlog: [
        todo(239, 'Invalid URLs should redirect to main login'),
        todo(369, 'Goblins in Washington'),
        todo(370, 'Goblins in Burtonsville'),
        todo(371, 'Goblins on the way'),
      ],
      done: [],
    },
  };
}

describe('VoiceFlow agent evaluation harness', () => {
  it('keeps the evaluator transport-minimal and the device adapter off the startup graph', () => {
    const evaluationSource = readFileSync(resolve('modules/voice/agent-evaluation.ts'), 'utf8');
    const deviceSource = readFileSync(resolve('modules/voice/agent-device-evaluation.ts'), 'utf8');
    expect(evaluationSource).not.toMatch(/mcp-client|callMcpTool|apiFetch|node:fs/);
    expect(deviceSource).toMatch(/LOCAL_TEXT_GENERATION_CAPABILITY/);
    expect(deviceSource).toMatch(/evaluateVoiceAgentUtterance/);
    expect(deviceSource).toMatch(/evaluateVoiceAgentCorpus/);
  });

  it('keeps the JSON corpus aligned with the bundled cases, including transcripts and expected semantics', () => {
    expect(corpus.boardRequirements).toEqual(VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS);
    expect(corpus.cases).toEqual(VOICE_AGENT_EVALUATION_CASES.map(testCase => ({
      id: testCase.id,
      transcript: testCase.transcript,
      expected: testCase.expected,
    })));
  });

  it('scores complete unique goldens as application matches without a provider', async () => {
    const complete = VOICE_AGENT_EVALUATION_CASES.filter(testCase => !testCase.expected.allowClarification
      && (testCase.expected.referenceNumeric || testCase.expected.referenceText)
      && testCase.id !== 'move-nonexistent-story');
    expect(complete.length).toBeGreaterThanOrEqual(6);
    for (const testCase of complete) {
      const result = await evaluateVoiceAgentUtterance(testCase.transcript, { board: board(), expected: testCase.expected });
      expect(result.mutationExecuted).toBe(false);
      expect(result.providerUsed).toBe(false);
      expect(result.applicationMatches).toBe(true);
      expect(extractHighConfidenceMoveCall(testCase.transcript, board())).not.toBeNull();
    }
  });

  it('does not count protocol compatibility recovery as unrecovered model accuracy', async () => {
    const recovered = {
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239', lane: 'Done' }, missing: 'lane', text: 'Which lane?',
    };
    expect(parseUnrecoveredModelEnvelope(JSON.stringify(recovered))?.kind).toBe('clarify_skill');
    const model = vi.fn(async () => JSON.stringify(recovered));
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-to-done')!;
    const result = await evaluateVoiceAgentUtterance(testCase.transcript, {
      model, board: board(), expected: testCase.expected,
    });
    expect(result.rawEnvelope).toMatchObject({ kind: 'clarify_skill' });
    expect(result.protocolEnvelope).toMatchObject({ kind: 'skill_call', skill: 'todos.move' });
    expect(result.protocolRecoveredFrom).toBe('move_clarification_with_target_and_lane');
    expect(result.rawMatches).toBe(false);
    expect(result.protocolMatches).toBe(true);
    expect(result.applicationMatches).toBe(true);
  });

  it('does not count a mocked model envelope as physical accuracy, but still measures the guard', async () => {
    const model = vi.fn(async () => JSON.stringify({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?',
    }));
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-to-done')!;
    const result = await evaluateVoiceAgentUtterance(testCase.transcript, {
      model, board: board(), expected: testCase.expected,
    });
    expect(result.rawMatches).toBe(false);
    expect(result.applicationMatches).toBe(true);
    expect(result.guardedEnvelope).toMatchObject({ kind: 'skill_call', skill: 'todos.move' });
    expect(result.mismatches).toEqual([]);
  });

  it('rewrites missing-slot ask_user into application-owned clarification', async () => {
    const model = vi.fn(async () => JSON.stringify({ kind: 'ask_user', text: 'What should I move?' }));
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-to-done-missing-reference')!;
    const result = await evaluateVoiceAgentUtterance(testCase.transcript, {
      model, board: board(), expected: testCase.expected,
    });
    expect(result.rawMatches).toBe(false);
    expect(result.applicationMatches).toBe(true);
    expect(result.guardedEnvelope).toMatchObject({
      kind: 'clarify_skill', skill: 'todos.move', missing: 'reference',
    });
  });

  it('does not treat a model-invented Done as completing Move #239', async () => {
    const model = vi.fn(async () => JSON.stringify({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?',
    }));
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-missing-lane')!;
    const result = await evaluateVoiceAgentUtterance(testCase.transcript, {
      model, board: board(), expected: testCase.expected,
    });
    expect(result.guardedEnvelope).toMatchObject({
      kind: 'clarify_skill', skill: 'todos.move', missing: 'lane',
    });
    expect(result.applicationMatches).toBe(true);
  });

  it('does not rewrite a compound command into a complete move', async () => {
    const model = vi.fn(async () => JSON.stringify({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?',
    }));
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-compound')!;
    const result = await evaluateVoiceAgentUtterance(testCase.transcript, {
      model, board: board(), expected: testCase.expected,
    });
    expect(result.deterministicCall).toBeNull();
    expect(result.guardedEnvelope).toMatchObject({ kind: 'clarify_skill', missing: 'lane' });
    expect(result.applicationMatches).toBe(false);
  });

  it('marks a complete command that stays in clarification as a corpus miss', () => {
    const expected = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-to-done')!.expected;
    const scored = scoreVoiceAgentEnvelope({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?',
    }, expected, board());
    expect(scored.matches).toBe(false);
    expect(scored.mismatches).toContain('clarification_not_allowed');
  });

  it('scores the application-owned corpus without a provider and leaves ambiguous titles unguessed', async () => {
    const summary = await evaluateVoiceAgentCorpus(VOICE_AGENT_EVALUATION_CASES, { board: board() });
    expect(summary.mutationExecuted).toBe(false);
    expect(summary.providerUsed).toBe(false);
    expect(summary.applicableCount).toBe(VOICE_AGENT_EVALUATION_CASES.length);
    expect(summary.inapplicableCount).toBe(0);
    expect(summary.results.find(result => result.id === 'move-invalid-url-story')?.applicationMatches).toBe(true);
    expect(summary.results.find(result => result.id === 'move-goblin-ambiguous')?.deterministicCall).toBeNull();
    expect(extractHighConfidenceMoveCall('Move Goblin to Done.', board())).toBeNull();
  });

  it('keeps malformed-but-JSON envelopes visible as unrecovered raw output and scores them as misses', async () => {
    const expected = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-to-done')!.expected;
    const malformed = [
      '{"kind":"skill_call","skill":"todos.move"}',
      '{"kind":"clarify_skill","skill":"todos.move"}',
      '{"kind":"skill_call","skill":"todos.move","arguments":"not-an-object"}',
      '{"kind":"skill_call","skill":"todos.move","arguments":{"reference":239,"lane":true}}',
      '{"kind":"skill_call","arguments":{"reference":"#239","lane":"Done"}}',
      '{"kind":"skill_call","skill":"todos.explode","arguments":{"reference":"#239","lane":"Done"}}',
    ];
    for (const raw of malformed) {
      const envelope = parseUnrecoveredModelEnvelope(raw);
      expect(envelope).not.toBeNull();
      expect(envelope).toMatchObject({ kind: expect.any(String) });
      expect(() => scoreVoiceAgentEnvelope(envelope, expected, board())).not.toThrow();
      expect(scoreVoiceAgentEnvelope(envelope, expected, board()).matches).toBe(false);
    }
  });

  it('does not abort utterance or corpus evaluation when Nano returns incomplete JSON envelopes', async () => {
    const payloads = [
      '{"kind":"skill_call","skill":"todos.move"}',
      '{"kind":"clarify_skill","skill":"todos.move"}',
      '{"kind":"skill_call","skill":"todos.move","arguments":{"reference":239,"lane":true}}',
      '{"kind":"skill_call","skill":"todos.explode","arguments":{}}',
    ];
    let index = 0;
    const model = vi.fn(async () => payloads[index++] ?? payloads.at(-1)!);
    const cases = [
      VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-to-done')!,
      VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-hash-missing-lane')!,
      VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-to-done-missing-reference')!,
      VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-compound')!,
    ];
    const first = await evaluateVoiceAgentUtterance(cases[0].transcript, {
      model, board: board(), expected: cases[0].expected,
    });
    expect(first.rawEnvelope).toMatchObject({ kind: 'skill_call', skill: 'todos.move' });
    expect(first.rawMatches).toBe(false);
    expect(first.protocolMatches).toBe(false);
    expect(first.applicationMatches).toBe(true);
    const summary = await evaluateVoiceAgentCorpus(cases, { model, board: board() });
    expect(summary.caseCount).toBe(cases.length);
    expect(summary.results).toHaveLength(cases.length);
    expect(summary.results.every(result => result.rawMatches === false)).toBe(true);
    expect(summary.rawAccuracy).toBe(0);
  });

  it('reports unique-title cases as inapplicable instead of semantic misses when the board lacks the fixture', async () => {
    const incomplete = board();
    incomplete.columns.backlog = [todo(239, 'Invalid URLs should redirect to main login')];
    const goblins = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.id === 'move-goblins-in-washington')!;
    expect(voiceAgentEvaluationCaseApplicability(goblins, incomplete)).toEqual({
      applicable: false, reason: 'unique_title_unavailable',
    });
    const summary = await evaluateVoiceAgentCorpus([goblins], { board: incomplete });
    expect(summary.inapplicableCount).toBe(1);
    expect(summary.applicationMatched).toBe(0);
    expect(summary.applicationAccuracy).toBeNull();
    expect(summary.results[0].boardApplicable).toBe(false);
    expect(summary.results[0].applicationMatches).toBeNull();
  });
});
