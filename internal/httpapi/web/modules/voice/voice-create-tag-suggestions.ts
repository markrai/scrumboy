import { normalizeLookup } from './normalize.js';
import type { VoiceCreateTag } from './voice-create-tags.js';

export const VOICE_CREATE_TAG_TYPO_MIN_LENGTH = 4;

export type VoiceCreateTagSuggestionMatch = Readonly<{
  matches: readonly string[];
  kind: 'terminal_s' | 'edit_distance_1' | 'none';
}>;

function isSingleInsertion(shorter: string, longer: string): boolean {
  let left = 0;
  let right = 0;
  let skipped = false;
  while (left < shorter.length && right < longer.length) {
    if (shorter[left] === longer[right]) {
      left += 1;
      right += 1;
      continue;
    }
    if (skipped) return false;
    skipped = true;
    right += 1;
  }
  return true;
}

/** Exactly one insertion, deletion, substitution, or adjacent transposition. */
export function isVoiceCreateTagDistanceOne(left: string, right: string): boolean {
  if (left === right || Math.abs(left.length - right.length) > 1) return false;
  if (left.length + 1 === right.length) return isSingleInsertion(left, right);
  if (right.length + 1 === left.length) return isSingleInsertion(right, left);

  const differences: number[] = [];
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) differences.push(index);
    if (differences.length > 2) return false;
  }
  if (differences.length === 1) return true;
  return differences.length === 2
    && differences[1] === differences[0] + 1
    && left[differences[0]] === right[differences[1]]
    && left[differences[1]] === right[differences[0]];
}

/** Close-candidate discovery only; callers must run strict resolution first. */
export function findVoiceCreateTagSuggestions(
  reference: string,
  authoritativeTags: readonly VoiceCreateTag[],
): VoiceCreateTagSuggestionMatch {
  const names = [...new Set(authoritativeTags.map(tag => tag.name))];
  const wanted = normalizeLookup(reference);
  const terminalTarget = wanted.endsWith('s') ? wanted.slice(0, -1) : `${wanted}s`;
  const terminal = terminalTarget
    ? names.filter(name => normalizeLookup(name) === terminalTarget)
    : [];
  if (terminal.length) return Object.freeze({ matches: Object.freeze(terminal), kind: 'terminal_s' });

  if (wanted.length < VOICE_CREATE_TAG_TYPO_MIN_LENGTH) {
    return Object.freeze({ matches: Object.freeze([]), kind: 'none' });
  }
  const typo = names.filter(name => {
    const normalized = normalizeLookup(name);
    return normalized.length >= VOICE_CREATE_TAG_TYPO_MIN_LENGTH
      && isVoiceCreateTagDistanceOne(wanted, normalized);
  });
  return Object.freeze({
    matches: Object.freeze(typo),
    kind: typo.length ? 'edit_distance_1' : 'none',
  });
}
