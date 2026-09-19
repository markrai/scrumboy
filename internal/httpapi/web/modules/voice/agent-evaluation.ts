import type { Board, Todo } from '../types.js';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { interpretAgentEnvelope, type AgentEnvelope, type AgentRecovery, type AgentState } from './agent-protocol.js';
import { createVoiceAgentModel, type VoiceAgentModel } from './agent-model.js';
import {
  extractHighConfidenceMoveCall,
  extractMissingMoveSlotClarification,
  recoverMoveAfterParseFailure,
  recoverPresentMoveArguments,
  extractMoveSlots,
  uniqueDirectMoveTitle,
  type MoveSlotEvidence,
} from './agent-move-extraction.js';
import { unwrapTodoReference } from './agent-skills.js';
import { parseSpokenNumber } from './normalize.js';
import { isCommandFailure } from './schema.js';
import { resolveVoiceLane } from './resolve.js';
import { rankTitleCandidates } from './target-resolver.js';

export const VOICE_AGENT_EVALUATION_VERSION = 1 as const;
export const VOICE_AGENT_EVALUATION_TIMEOUT_MS = 45_000;

/** Application accuracy is valid only on a board that satisfies these corpus fixtures. */
export const VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS = Object.freeze({
  requiredLocalIds: [239],
  requiredUniqueTitles: ['Invalid URLs should redirect to main login', 'Goblins in Washington'],
  requiredAmbiguousTitle: 'Goblin',
  requiredLane: 'done',
  absentTitle: 'TotallyInventedStory',
  absentLane: 'TotallyInventedLane',
});

const EVAL_ENVELOPE_KINDS = new Set(['skill_call', 'clarify_skill', 'ask_user', 'finish', 'confirm', 'decline', 'cancel']);

export type VoiceAgentEvaluationExpected = Readonly<{
  kind: 'skill_call' | 'clarify_skill';
  skill: 'todos.move';
  referenceNumeric?: number;
  referenceText?: string;
  lane?: string;
  missing?: 'lane' | 'reference';
  allowClarification: boolean;
}>;

export type VoiceAgentEvaluationMismatch =
  | 'kind'
  | 'skill'
  | 'clarification_not_allowed'
  | 'reference'
  | 'lane'
  | 'missing';

export type VoiceAgentBoardApplicability = Readonly<{
  applicable: boolean;
  reason: string | null;
}>;

export type VoiceAgentUtteranceEvaluation = Readonly<{
  version: typeof VOICE_AGENT_EVALUATION_VERSION;
  id?: string;
  transcript: string;
  deterministicCall: Extract<AgentEnvelope, { kind: 'skill_call' }> | null;
  rawEnvelope: AgentEnvelope | null;
  protocolEnvelope: AgentEnvelope | null;
  protocolRecoveredFrom: AgentRecovery | null;
  guardedEnvelope: AgentEnvelope | null;
  slots: MoveSlotEvidence;
  boardApplicable: boolean;
  boardApplicabilityReason: string | null;
  rawMatches: boolean | null;
  protocolMatches: boolean | null;
  applicationMatches: boolean | null;
  mismatches: readonly VoiceAgentEvaluationMismatch[];
  mutationExecuted: false;
  providerUsed: boolean;
}>;

function boardTodos(board: Board): Todo[] {
  return Object.values(board.columns ?? {}).flat();
}

function hasLocalId(board: Board, localId: number): boolean {
  return boardTodos(board).some(todo => todo.localId === localId);
}

function hasLane(board: Board, lane: string): boolean {
  return !isCommandFailure(resolveVoiceLane(lane, board));
}

function ambiguousTitle(phrase: string, board: Board): boolean {
  if (uniqueDirectMoveTitle(phrase, board)) return false;
  return rankTitleCandidates(phrase, boardTodos(board).map(todo => ({ localId: todo.localId, title: todo.title }))).length >= 2;
}

/**
 * Evaluator-only: JSON envelope as the model emitted it, including fence stripping.
 * Does not apply production compatibility recoveries such as move_clarification_with_target_and_lane.
 */
export function parseUnrecoveredModelEnvelope(raw: string): AgentEnvelope | null {
  if (typeof raw !== 'string' || raw.length > 8192) return null;
  let source = raw.trim();
  if (source.startsWith('```json\n') && source.endsWith('\n```')) source = source.slice(8, -4);
  else if (source.startsWith('```\n') && source.endsWith('\n```')) source = source.slice(4, -4);
  try {
    const value = JSON.parse(source) as { kind?: unknown };
    if (!value || typeof value !== 'object' || typeof value.kind !== 'string' || !EVAL_ENVELOPE_KINDS.has(value.kind)) {
      return null;
    }
    return value as AgentEnvelope;
  } catch {
    return null;
  }
}

export function voiceAgentEvaluationCaseApplicability(
  testCase: VoiceAgentEvaluationCase,
  board: Board,
): VoiceAgentBoardApplicability {
  const expected = testCase.expected;
  if (expected.referenceNumeric != null && !hasLocalId(board, expected.referenceNumeric)) {
    return { applicable: false, reason: `missing_local_id_${expected.referenceNumeric}` };
  }
  if (expected.lane && expected.lane !== VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS.absentLane && !hasLane(board, expected.lane)) {
    return { applicable: false, reason: `missing_lane_${expected.lane}` };
  }
  if (testCase.id === 'move-nonexistent-lane' && hasLane(board, VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS.absentLane)) {
    return { applicable: false, reason: 'absent_lane_present' };
  }
  if (testCase.id === 'move-nonexistent-story' && uniqueDirectMoveTitle(VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS.absentTitle, board)) {
    return { applicable: false, reason: 'absent_title_uniquely_present' };
  }
  if (testCase.id === 'move-goblin-ambiguous' && !ambiguousTitle(VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS.requiredAmbiguousTitle, board)) {
    return { applicable: false, reason: 'ambiguous_title_not_ambiguous' };
  }
  if (expected.referenceText && !expected.allowClarification && testCase.id !== 'move-nonexistent-story') {
    const slots = extractMoveSlots(testCase.transcript, board);
    if (!slots.reference || !uniqueDirectMoveTitle(slots.reference, board)) {
      return { applicable: false, reason: 'unique_title_unavailable' };
    }
  }
  return { applicable: true, reason: null };
}

function numericFrom(value: string | undefined): number | null {
  if (!value) return null;
  const unwrapped = unwrapTodoReference(value) ?? value;
  return parseSpokenNumber(unwrapped)?.value ?? parseSpokenNumber(value)?.value ?? null;
}

function laneKey(value: string | undefined, board: Board): string | null {
  if (!value) return null;
  const resolved = resolveVoiceLane(value, board);
  return isCommandFailure(resolved) ? value.trim().toLowerCase() : resolved.value.key;
}

function recordObject(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function stringField(record: Record<string, unknown> | null, key: string): string | undefined {
  if (!record || !(key in record)) return undefined;
  const value = record[key];
  return typeof value === 'string' ? value : undefined;
}

const SCORE_MISS = Object.freeze({ matches: false, mismatches: Object.freeze(['kind'] as VoiceAgentEvaluationMismatch[]) });

export function scoreVoiceAgentEnvelope(
  envelope: AgentEnvelope | null,
  expected: VoiceAgentEvaluationExpected,
  board: Board,
): { matches: boolean; mismatches: VoiceAgentEvaluationMismatch[] } {
  try {
    if (!envelope) return { matches: false, mismatches: ['kind'] };
    const record = recordObject(envelope);
    if (!record) return { matches: false, mismatches: ['kind'] };
    const kind = stringField(record, 'kind');
    const skill = stringField(record, 'skill');
    const args = recordObject(record.arguments);
    const mismatches: VoiceAgentEvaluationMismatch[] = [];
    if (!expected.allowClarification && (kind === 'clarify_skill' || kind === 'ask_user')) {
      mismatches.push('clarification_not_allowed');
    }
    if (expected.kind === 'skill_call' && kind !== 'skill_call') mismatches.push('kind');
    if (expected.kind === 'clarify_skill' && kind !== 'clarify_skill' && kind !== 'skill_call') {
      mismatches.push('kind');
    }
    if (kind === 'skill_call' || kind === 'clarify_skill') {
      if (skill !== expected.skill) mismatches.push('skill');
    }
    if (kind === 'skill_call' && skill === 'todos.move') {
      const reference = stringField(args, 'reference');
      if (expected.referenceNumeric != null && numericFrom(reference) !== expected.referenceNumeric) mismatches.push('reference');
      if (expected.referenceText) {
        const normalized = (unwrapTodoReference(reference ?? '') ?? reference ?? '').toLowerCase();
        if (!normalized.includes(expected.referenceText.toLowerCase())) mismatches.push('reference');
      }
      if (expected.lane && laneKey(stringField(args, 'lane'), board) !== laneKey(expected.lane, board)) mismatches.push('lane');
    }
    if (kind === 'clarify_skill' && skill === 'todos.move') {
      if (expected.missing && stringField(record, 'missing') !== expected.missing) mismatches.push('missing');
      if (expected.referenceNumeric != null && numericFrom(stringField(args, 'reference')) !== expected.referenceNumeric) {
        mismatches.push('reference');
      }
      if (expected.lane && args && 'lane' in args && laneKey(stringField(args, 'lane'), board) !== laneKey(expected.lane, board)) {
        mismatches.push('lane');
      }
    }
    return { matches: mismatches.length === 0, mismatches: [...new Set(mismatches)] };
  } catch {
    return { matches: SCORE_MISS.matches, mismatches: [...SCORE_MISS.mismatches] };
  }
}

function scoreEvaluationLayers(
  rawEnvelope: AgentEnvelope | null,
  protocolEnvelope: AgentEnvelope | null,
  guardedEnvelope: AgentEnvelope | null,
  expected: VoiceAgentEvaluationExpected,
  board: Board,
): { raw: ReturnType<typeof scoreVoiceAgentEnvelope>; protocol: ReturnType<typeof scoreVoiceAgentEnvelope>; application: ReturnType<typeof scoreVoiceAgentEnvelope> } {
  try {
    return {
      raw: scoreVoiceAgentEnvelope(rawEnvelope, expected, board),
      protocol: scoreVoiceAgentEnvelope(protocolEnvelope, expected, board),
      application: scoreVoiceAgentEnvelope(guardedEnvelope, expected, board),
    };
  } catch {
    const miss = { matches: SCORE_MISS.matches, mismatches: [...SCORE_MISS.mismatches] };
    return { raw: miss, protocol: miss, application: miss };
  }
}

export async function evaluateVoiceAgentUtterance(
  transcript: string,
  options: Readonly<{
    model?: VoiceAgentModel | null;
    board: Board;
    expected?: VoiceAgentEvaluationExpected;
    applicability?: VoiceAgentBoardApplicability;
    signal?: AbortSignal;
  }>,
): Promise<VoiceAgentUtteranceEvaluation> {
  const slots = extractMoveSlots(transcript, options.board);
  const deterministicCall = extractHighConfidenceMoveCall(transcript, options.board);
  const deterministicClarification = extractMissingMoveSlotClarification(transcript, options.board);
  const applicability = options.applicability ?? { applicable: true, reason: null };
  let rawEnvelope: AgentEnvelope | null = null;
  let protocolEnvelope: AgentEnvelope | null = null;
  let protocolRecoveredFrom: AgentRecovery | null = null;
  let providerUsed = false;
  if (options.model) {
    providerUsed = true;
    try {
      const state: AgentState = { kind: 'idle' };
      const input = JSON.stringify({
        goal: transcript,
        activeTodoAvailable: false,
        trace: [{ user: transcript }],
        pending: state,
      });
      const raw = await options.model(input, options.signal ?? new AbortController().signal);
      rawEnvelope = parseUnrecoveredModelEnvelope(raw);
      try {
        const interpreted = interpretAgentEnvelope(raw, state);
        protocolEnvelope = interpreted.envelope;
        protocolRecoveredFrom = interpreted.recoveredFrom ?? null;
      } catch {
        protocolEnvelope = null;
      }
    } catch {
      rawEnvelope = null;
      protocolEnvelope = null;
    }
  }
  const recovered = protocolEnvelope
    ? recoverPresentMoveArguments(transcript, protocolEnvelope, options.board, 'idle')
    : (providerUsed ? recoverMoveAfterParseFailure(transcript, options.board, 'idle') : null);
  const guardedEnvelope = deterministicCall
    ?? recovered?.envelope
    ?? protocolEnvelope
    ?? deterministicClarification;
  const scored = options.expected && applicability.applicable
    ? scoreEvaluationLayers(rawEnvelope, protocolEnvelope, guardedEnvelope, options.expected, options.board)
    : null;
  return Object.freeze({
    version: VOICE_AGENT_EVALUATION_VERSION,
    transcript,
    deterministicCall,
    rawEnvelope,
    protocolEnvelope,
    protocolRecoveredFrom,
    guardedEnvelope,
    slots,
    boardApplicable: applicability.applicable,
    boardApplicabilityReason: applicability.reason,
    rawMatches: scored && providerUsed ? scored.raw.matches : null,
    protocolMatches: scored && providerUsed ? scored.protocol.matches : null,
    applicationMatches: scored ? scored.application.matches : null,
    mismatches: scored?.application.mismatches ?? [],
    mutationExecuted: false,
    providerUsed,
  });
}

export function createVoiceAgentEvaluationModel(capability: LocalTextGenerationCapability, locale = 'en'): VoiceAgentModel {
  return createVoiceAgentModel(capability, locale);
}

export type VoiceAgentEvaluationCase = Readonly<{
  id: string;
  transcript: string;
  expected: VoiceAgentEvaluationExpected;
}>;

export type VoiceAgentCorpusEvaluation = Readonly<{
  version: typeof VOICE_AGENT_EVALUATION_VERSION;
  mutationExecuted: false;
  providerUsed: boolean;
  boardRequirements: typeof VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS;
  caseCount: number;
  applicableCount: number;
  inapplicableCount: number;
  rawMatched: number;
  protocolMatched: number;
  applicationMatched: number;
  rawAccuracy: number | null;
  protocolAccuracy: number | null;
  applicationAccuracy: number | null;
  results: readonly VoiceAgentUtteranceEvaluation[];
}>;

export async function evaluateVoiceAgentCorpus(
  cases: readonly VoiceAgentEvaluationCase[],
  options: Readonly<{
    model?: VoiceAgentModel | null;
    board: Board;
    signal?: AbortSignal;
  }>,
): Promise<VoiceAgentCorpusEvaluation> {
  const results: VoiceAgentUtteranceEvaluation[] = [];
  for (const testCase of cases) {
    const applicability = voiceAgentEvaluationCaseApplicability(testCase, options.board);
    try {
      results.push(Object.freeze({
        ...(await evaluateVoiceAgentUtterance(testCase.transcript, {
          model: options.model,
          board: options.board,
          expected: testCase.expected,
          applicability,
          signal: options.signal,
        })),
        id: testCase.id,
      }));
    } catch {
      results.push(Object.freeze({
        version: VOICE_AGENT_EVALUATION_VERSION,
        id: testCase.id,
        transcript: testCase.transcript,
        deterministicCall: null,
        rawEnvelope: null,
        protocolEnvelope: null,
        protocolRecoveredFrom: null,
        guardedEnvelope: null,
        slots: { reference: null, numeric: false, lane: null },
        boardApplicable: applicability.applicable,
        boardApplicabilityReason: applicability.reason,
        rawMatches: options.model && applicability.applicable ? false : null,
        protocolMatches: options.model && applicability.applicable ? false : null,
        applicationMatches: applicability.applicable ? false : null,
        mismatches: ['kind'] as const,
        mutationExecuted: false,
        providerUsed: !!options.model,
      }));
    }
  }
  const applicable = results.filter(result => result.boardApplicable);
  const rawScored = applicable.filter(result => result.rawMatches != null);
  const protocolScored = applicable.filter(result => result.protocolMatches != null);
  const applicationScored = applicable.filter(result => result.applicationMatches != null);
  const rawMatched = rawScored.filter(result => result.rawMatches).length;
  const protocolMatched = protocolScored.filter(result => result.protocolMatches).length;
  const applicationMatched = applicationScored.filter(result => result.applicationMatches).length;
  return Object.freeze({
    version: VOICE_AGENT_EVALUATION_VERSION,
    mutationExecuted: false,
    providerUsed: results.some(result => result.providerUsed),
    boardRequirements: VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS,
    caseCount: results.length,
    applicableCount: applicable.length,
    inapplicableCount: results.length - applicable.length,
    rawMatched,
    protocolMatched,
    applicationMatched,
    rawAccuracy: rawScored.length ? rawMatched / rawScored.length : null,
    protocolAccuracy: protocolScored.length ? protocolMatched / protocolScored.length : null,
    applicationAccuracy: applicationScored.length ? applicationMatched / applicationScored.length : null,
    results,
  });
}
