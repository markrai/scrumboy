import type { Board, Todo } from '../types.js';
import type { AgentEnvelope, AgentStateKind, SkillCall, SkillClarification } from './agent-protocol.js';
import { unwrapTodoReference } from './agent-skills.js';
import { normalizeLookup, parseSpokenNumber, stripWrappingQuotes } from './normalize.js';
import { isCommandFailure } from './schema.js';
import { resolveVoiceLane } from './resolve.js';
import { rankTitleCandidates, selectUniqueTitleCandidate } from './target-resolver.js';

const MOVE_VERB = /^(?:please\s+)?(?:move|mark|set|change(?:\s+the\s+status\s+of)?)\s+(.+)$/i;
const ENTITY_PREFIX = /^(?:the\s+)?(?:story|stories|todo|todos|to[-\s]?do|card|task|item)(?:\s+(?:called|named|titled|number))?\s+/i;
const GENERIC_REFERENCE = /^(?:(?:the|a|an|some|any)\s+)?(?:story|stories|todo|todos|to[-\s]?do|card|task|item)s?$/i;
const WHICH_LANE = /\bwhich\s+(?:lane|column|status)\b/i;
const WHICH_STORY = /\bwhich\s+(?:story|todo|to[-\s]?do|card|task|item)\b/i;
const MISSING_LANE_TEXT = 'Which lane?';
const MISSING_STORY_TEXT = 'Which story?';

export type MoveSlotEvidence = Readonly<{
  reference: string | null;
  numeric: boolean;
  lane: string | null;
}>;

export type MoveEnvelopeRecovery =
  | 'present_move_slots'
  | 'ask_user_to_clarify_skill'
  | 'model_parse_failure';

/** Compound cues that must still reach the model so additional requested work is not dropped. */
export function hasCompoundRequestCue(goal: string): boolean {
  return /[&,;\n]/.test(goal) || /\b(?:and|then|also|plus)\b/.test(normalizeLookup(goal));
}

export function isCompleteStandaloneMove(utterance: string, board: Board): boolean {
  if (hasCompoundRequestCue(utterance)) return false;
  const slots = extractMoveSlots(utterance, board);
  return !!slots.reference && !!slots.lane;
}

function boardTodos(board: Board): Todo[] {
  return Object.values(board.columns ?? {}).flat();
}

function strippedReferenceIsStronger(reference: string, stripped: string, todo: Todo): boolean {
  const candidate = [{ localId: todo.localId, title: todo.title }];
  const originalScore = rankTitleCandidates(reference, candidate)[0]?.score ?? 0;
  const strippedScore = rankTitleCandidates(stripped, candidate)[0]?.score ?? 0;
  return strippedScore > originalScore;
}

/** Local-board uniqueness only: exact title first, then suffix-wrapper safety, then fail-closed scoring. */
export function uniqueDirectMoveTitle(reference: string, board: Board): boolean {
  const todos = boardTodos(board);
  const candidates = todos.map(todo => ({ localId: todo.localId, title: todo.title }));
  const originalExact = rankTitleCandidates(reference, candidates).filter(candidate => candidate.score === 100);
  if (originalExact.length === 1) return true;
  if (originalExact.length > 1) return false;
  const unwrapped = unwrapTodoReference(reference);
  if (unwrapped) {
    const unique = selectUniqueTitleCandidate(unwrapped, candidates);
    if (!unique) return false;
    const todo = todos.find(item => item.localId === unique.localId);
    return !!todo && strippedReferenceIsStronger(reference, unwrapped, todo);
  }
  return selectUniqueTitleCandidate(reference, candidates) != null;
}

function resolvedLaneName(raw: string, board: Board): string | null {
  const resolved = resolveVoiceLane(raw, board);
  return isCommandFailure(resolved) ? null : resolved.value.name;
}

function splitDestination(rest: string, board: Board): { target: string; laneRaw: string } | null {
  const leading = rest.match(/^(?:to|as|into)\s+(.+)$/i);
  if (leading) {
    const raw = leading[1].trim();
    const lane = resolvedLaneName(raw, board);
    if (lane) return { target: '', laneRaw: lane };
  }
  const matches = [...rest.matchAll(/\s+(to|as|into)\s+/gi)];
  for (let index = matches.length - 1; index >= 0; index--) {
    const match = matches[index];
    if (match.index == null) continue;
    const target = rest.slice(0, match.index).trim();
    const destination = rest.slice(match.index + match[0].length).trim();
    const lane = resolvedLaneName(destination, board);
    if (lane) return { target, laneRaw: lane };
    if (index === matches.length - 1 && numericReference(target) && destination) {
      return { target, laneRaw: destination.replace(/[.!?]+$/, '').trim() };
    }
  }
  return null;
}

function numericReference(target: string): string | null {
  const trimmed = stripWrappingQuotes(target.trim());
  if (!trimmed) return null;
  const stripped = trimmed.replace(ENTITY_PREFIX, '').trim();
  const parsed = parseSpokenNumber(stripped) ?? parseSpokenNumber(trimmed);
  if (!parsed || parsed.ambiguous) return null;
  return `#${parsed.value}`;
}

export function extractMoveSlots(utterance: string, board: Board): MoveSlotEvidence {
  const match = MOVE_VERB.exec(utterance.trim());
  if (!match) return { reference: null, numeric: false, lane: null };
  const split = splitDestination(match[1].trim(), board);
  if (!split) {
    const numeric = numericReference(match[1]);
    const title = match[1].trim();
    const generic = !numeric && GENERIC_REFERENCE.test(normalizeLookup(title));
    return { reference: numeric ?? (title && !generic ? title : null), numeric: !!numeric, lane: null };
  }
  const numeric = numericReference(split.target);
  const title = split.target.trim();
  const generic = !numeric && GENERIC_REFERENCE.test(normalizeLookup(title));
  return {
    reference: numeric ?? (title && !generic ? title : null),
    numeric: !!numeric,
    lane: split.laneRaw,
  };
}

function completeMoveCall(reference: string, lane: string): SkillCall {
  return { kind: 'skill_call', skill: 'todos.move', arguments: { reference, lane } };
}

function moveClarification(slots: MoveSlotEvidence, missing: 'lane' | 'reference', text: string): SkillClarification | null {
  if (missing === 'lane') {
    if (!slots.reference) return null;
    return { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: slots.reference }, missing: 'lane', text };
  }
  if (!slots.lane) return null;
  return { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: slots.lane }, missing: 'reference', text };
}

function confidentTarget(slots: MoveSlotEvidence, board: Board): boolean {
  return !!slots.reference && (slots.numeric || uniqueDirectMoveTitle(slots.reference, board));
}

function extractedCompleteMoveCall(slots: MoveSlotEvidence): SkillCall | null {
  if (!slots.reference || !slots.lane) return null;
  return completeMoveCall(slots.reference, slots.lane);
}

function uniqueCompleteMoveCall(slots: MoveSlotEvidence, board: Board): SkillCall | null {
  if (!extractedCompleteMoveCall(slots) || !confidentTarget(slots, board)) return null;
  return completeMoveCall(slots.reference!, slots.lane!);
}

/** High-confidence direct moves: numeric+lane, or a unique local title plus an unambiguous board lane. */
export function extractHighConfidenceMoveCall(utterance: string, board: Board): SkillCall | null {
  if (hasCompoundRequestCue(utterance)) return null;
  return uniqueCompleteMoveCall(extractMoveSlots(utterance, board), board);
}

/**
 * Missing-slot draft owned by the application: move intent plus exactly one present slot.
 * Title-only drafts still require a unique local match so generic ask_user is not a picker.
 */
export function extractMissingMoveSlotClarification(
  utterance: string,
  board: Board,
  text?: string,
): SkillClarification | null {
  if (hasCompoundRequestCue(utterance)) return null;
  const slots = extractMoveSlots(utterance, board);
  if (slots.reference && slots.lane) return null;
  if (slots.reference && !slots.lane && confidentTarget(slots, board)) {
    return moveClarification(slots, 'lane', text?.trim() || MISSING_LANE_TEXT);
  }
  if (!slots.reference && slots.lane) {
    return moveClarification(slots, 'reference', text?.trim() || MISSING_STORY_TEXT);
  }
  return null;
}

/** After the model returns unparseable JSON, recover a complete extracted move or a one-slot draft. */
export function recoverMoveAfterParseFailure(
  utterance: string,
  board: Board,
  stateKind: AgentStateKind,
): { envelope: SkillCall | SkillClarification; recoveredFrom: 'model_parse_failure' } | null {
  if (stateKind !== 'idle') return null;
  if (hasCompoundRequestCue(utterance)) return null;
  const slots = extractMoveSlots(utterance, board);
  const complete = extractedCompleteMoveCall(slots);
  if (complete) return { envelope: complete, recoveredFrom: 'model_parse_failure' };
  const missing = extractMissingMoveSlotClarification(utterance, board);
  return missing ? { envelope: missing, recoveredFrom: 'model_parse_failure' } : null;
}

/**
 * Application-owned guard: a fresh utterance that already names a target or lane
 * must not be accepted as a clarification claiming that present argument is missing.
 * Semantically equivalent ask_user is rewritten to structured missing-slot state
 * only when this utterance itself establishes move intent and exactly one slot.
 */
export function recoverPresentMoveArguments(
  utterance: string,
  envelope: AgentEnvelope,
  board: Board,
  stateKind: AgentStateKind,
): { envelope: AgentEnvelope; recoveredFrom: MoveEnvelopeRecovery } | null {
  if (stateKind !== 'idle') return null;
  if (hasCompoundRequestCue(utterance)) return null;
  const complete = extractedCompleteMoveCall(extractMoveSlots(utterance, board));
  if (complete) {
    if (envelope.kind === 'skill_call') return null;
    return { envelope: complete, recoveredFrom: 'present_move_slots' };
  }
  if (envelope.kind === 'clarify_skill' && envelope.skill !== 'todos.move') return null;
  if (envelope.kind !== 'clarify_skill' && envelope.kind !== 'ask_user') return null;
  const missing = extractMissingMoveSlotClarification(utterance, board);
  if (!missing) return null;
  if (envelope.kind === 'ask_user') {
    const question = envelope.text.trim();
    const reuse = (missing.missing === 'lane' && WHICH_LANE.test(question))
      || (missing.missing === 'reference' && WHICH_STORY.test(question));
    return {
      envelope: reuse ? { ...missing, text: question } : missing,
      recoveredFrom: 'ask_user_to_clarify_skill',
    };
  }
  if (envelope.missing === missing.missing && JSON.stringify(envelope.arguments) === JSON.stringify(missing.arguments)) {
    return null;
  }
  return { envelope: missing, recoveredFrom: 'present_move_slots' };
}
