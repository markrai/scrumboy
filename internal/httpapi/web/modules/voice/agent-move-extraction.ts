import type { Board } from '../types.js';
import type { SkillCall, SkillClarification } from './agent-protocol.js';
import { normalizeLookup, parseSpokenNumber, stripWrappingQuotes } from './normalize.js';
import { resolveVoiceLane } from './resolve.js';
import { isCommandFailure } from './schema.js';

const MOVE_VERB = /^(?:please\s+)?(move|mark|set|change(?:\s+the\s+status\s+of)?)\s+(.+)$/i;
const ENTITY_PREFIX = /^(?:the\s+)?(?:story|stories|todo|todos|to[-\s]?do|card|task|item)(?:\s+(?:called|named|titled|number))?\s+/i;
const GENERIC_REFERENCE = /^(?:(?:the|a|an|some|any)\s+)?(?:story|stories|todo|todos|to[-\s]?do|card|task|item)s?$/i;
const SECOND_OPERATION = /(?:\b(?:and(?:\s+(?:then|after\s+that))?|then|also|plus|afterwards?)\b|[&,;\n])\s*(?:please\s+)?(?:move|mark|set|change|open|find|search|look|read|inspect|show|create|add|remove|delete|archive|rename|tag|assign|unassign|append|replace|count)\b/gi;
const POSSIBLE_CONTINUATION = /[&,;\n]|\b(?:and|then|also|plus|after(?:\s+that|wards?)?|before)\b/i;
const MISSING_LANE_TEXT = 'Which lane?';
const MISSING_STORY_TEXT = 'Which story?';

export type MoveSlotEvidence = Readonly<{
  reference: string | null;
  numeric: boolean;
  lane: string | null;
}>;

export type RecognizedMoveCommand = Readonly<{
  envelope: SkillCall | SkillClarification;
  completion: 'finish_after_effect';
}>;

type MovePartition = Readonly<{
  slots: MoveSlotEvidence;
  separatorStart: number;
}>;

type ParsedMoveSyntax = Readonly<{
  rest: string;
  partitions: readonly MovePartition[];
  missingDestination: MoveSlotEvidence;
}>;

function trimSlot(value: string): string {
  return stripWrappingQuotes(value.trim().replace(/[.!?]+$/, '').trim());
}

function numericReference(target: string): string | null {
  const trimmed = stripWrappingQuotes(target.trim());
  if (!trimmed) return null;
  const stripped = trimmed.replace(ENTITY_PREFIX, '').trim();
  const parsed = parseSpokenNumber(stripped) ?? parseSpokenNumber(trimmed);
  if (!parsed || parsed.ambiguous) return null;
  return `#${parsed.value}`;
}

function slotsFor(targetRaw: string, laneRaw: string | null): MoveSlotEvidence {
  const numeric = numericReference(targetRaw);
  const title = trimSlot(targetRaw);
  const generic = !numeric && GENERIC_REFERENCE.test(normalizeLookup(title));
  return {
    reference: numeric ?? (title && !generic ? title : null),
    numeric: !!numeric,
    lane: laneRaw == null ? null : trimSlot(laneRaw) || null,
  };
}

function parseMoveSyntax(utterance: string): ParsedMoveSyntax | null {
  const match = MOVE_VERB.exec(utterance.trim());
  if (!match) return null;
  const verb = normalizeLookup(match[1]);
  const rest = match[2].trim();
  const leading = rest.match(/^(?:to|as|into)\s+(.+)$/i);
  if (leading) {
    const slots = slotsFor('', leading[1]);
    return {
      rest,
      partitions: slots.lane ? [{ slots, separatorStart: 0 }] : [],
      missingDestination: { reference: null, numeric: false, lane: null },
    };
  }

  const partitions = [...rest.matchAll(/\s+(?:to|as|into)\s+/gi)]
    .flatMap(separator => {
      if (separator.index == null) return [];
      const slots = slotsFor(rest.slice(0, separator.index), rest.slice(separator.index + separator[0].length));
      return slots.lane ? [{ slots, separatorStart: separator.index } satisfies MovePartition] : [];
    });
  if (partitions.length) {
    return { rest, partitions, missingDestination: { reference: null, numeric: false, lane: null } };
  }

  // Mark/set/change without an explicit destination is natural-language territory
  // (for example, "mark it done"), not a structurally missing-lane command.
  const missingDestination = verb === 'move'
    ? slotsFor(rest, null)
    : { reference: null, numeric: false, lane: null };
  return { rest, partitions: [], missingDestination };
}

function laneExists(lane: string | null, board: Board | undefined): boolean {
  return !!lane && !!board && !isCommandFailure(resolveVoiceLane(lane, board));
}

function exactTitleExists(reference: string | null, board: Board | undefined): boolean {
  if (!reference || !board) return false;
  const wanted = normalizeLookup(stripWrappingQuotes(reference).replace(ENTITY_PREFIX, '').trim());
  return !!wanted && Object.values(board.columns).flat().some(todo => normalizeLookup(todo.title) === wanted);
}

function selectMovePartition(partitions: readonly MovePartition[], board?: Board): MovePartition | null {
  if (partitions.length === 1) return partitions[0];
  if (!partitions.length) return null;

  // A structural numeric target is stronger than an alternate partition that
  // would absorb a destination word into the target ("#239 to Ready").
  const numeric = partitions.filter(partition => partition.slots.numeric);
  if (numeric.length === 1) return numeric[0];

  // Syntax extraction deliberately returns every partition. Board truth is used
  // only here to select a unique valid destination; ambiguity remains model work.
  const valid = partitions.filter(partition => laneExists(partition.slots.lane, board));
  return valid.length === 1 ? valid[0] : null;
}

function operationCues(rest: string): RegExpMatchArray[] {
  return [...rest.matchAll(SECOND_OPERATION)];
}

function partitionHasCompoundCue(parsed: ParsedMoveSyntax, partition: MovePartition | null, board?: Board): boolean {
  const cues = operationCues(parsed.rest);
  if (!partition) {
    if (!cues.length && !POSSIBLE_CONTINUATION.test(parsed.rest)) return false;
    return !exactTitleExists(parsed.missingDestination.reference, board);
  }
  if (!cues.length) return false;

  const targetIsLiteral = exactTitleExists(partition.slots.reference, board);
  const laneIsLiteral = laneExists(partition.slots.lane, board);
  return cues.some(cue => {
    if (cue.index == null) return true;
    return cue.index < partition.separatorStart ? !targetIsLiteral : !laneIsLiteral;
  });
}

/** Returns every syntax-only destination partition without consulting board state. */
export function extractMoveSlotCandidates(utterance: string): readonly MoveSlotEvidence[] {
  const parsed = parseMoveSyntax(utterance);
  if (!parsed) return [];
  if (parsed.partitions.length) return parsed.partitions.map(partition => partition.slots);
  return [parsed.missingDestination];
}

/**
 * Returns the uniquely selected syntactic interpretation. Multiple destination
 * separators require either structural numeric evidence or one authoritative lane match.
 */
export function extractMoveSlots(utterance: string, board?: Board): MoveSlotEvidence {
  const parsed = parseMoveSyntax(utterance);
  if (!parsed) return { reference: null, numeric: false, lane: null };
  if (!parsed.partitions.length) return parsed.missingDestination;
  return selectMovePartition(parsed.partitions, board)?.slots
    ?? { reference: null, numeric: false, lane: null };
}

/** A compound cue is contextual: operation-like words in exact titles or lanes are literals. */
export function hasCompoundRequestCue(goal: string, board?: Board): boolean {
  const parsed = parseMoveSyntax(goal);
  if (!parsed) return operationCues(goal).length > 0;
  const partition = selectMovePartition(parsed.partitions, board);
  return partitionHasCompoundCue(parsed, partition, board);
}

function completeMoveCall(reference: string, lane: string): SkillCall {
  return { kind: 'skill_call', skill: 'todos.move', arguments: { reference, lane } };
}

function moveClarification(slots: MoveSlotEvidence): SkillClarification | null {
  if (slots.reference && !slots.lane) {
    return { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: slots.reference }, missing: 'lane', text: MISSING_LANE_TEXT };
  }
  if (!slots.reference && slots.lane) {
    return { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: slots.lane }, missing: 'reference', text: MISSING_STORY_TEXT };
  }
  return null;
}

/** Conservatively recognizes only standalone move grammar with all or exactly one slot present. */
export function recognizeMoveCommand(utterance: string, board?: Board): RecognizedMoveCommand | null {
  const parsed = parseMoveSyntax(utterance);
  if (!parsed) return null;
  const partition = selectMovePartition(parsed.partitions, board);
  if (parsed.partitions.length && !partition) return null;
  if (partitionHasCompoundCue(parsed, partition, board)) return null;
  const slots = partition?.slots ?? parsed.missingDestination;
  const envelope = slots.reference && slots.lane
    ? completeMoveCall(slots.reference, slots.lane)
    : moveClarification(slots);
  return envelope ? { envelope, completion: 'finish_after_effect' } : null;
}

export function isCompleteStandaloneMove(utterance: string, board?: Board): boolean {
  return recognizeMoveCommand(utterance, board)?.envelope.kind === 'skill_call';
}

export function extractDeterministicMoveCall(utterance: string, board?: Board): SkillCall | null {
  const recognized = recognizeMoveCommand(utterance, board)?.envelope;
  return recognized?.kind === 'skill_call' ? recognized : null;
}

export function extractMissingMoveSlotClarification(utterance: string, board?: Board): SkillClarification | null {
  const recognized = recognizeMoveCommand(utterance, board)?.envelope;
  return recognized?.kind === 'clarify_skill' ? recognized : null;
}
