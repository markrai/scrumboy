/**
 * Deterministic client-side comparison helpers for server-owned tag names.
 * These keys are only for UI search/deduplication; the Go store remains the
 * authority for persistent identity and canonicalization.
 */
export function tagLookupKey(value: string): string {
  return String(value ?? '').normalize('NFC').toLowerCase();
}

/**
 * A deliberately conservative key for identity-sensitive UI operations.
 * Lowercasing is applied one code point at a time only when it remains one code
 * point, matching Go's simple-casing shape and avoiding JavaScript full-case
 * expansions (U+0130) and contextual mappings (final Greek sigma).
 */
export function tagIdentityKey(value: string): string {
  const lowered = [...String(value ?? '').normalize('NFC')]
    .map((character) => {
      const candidate = character.toLowerCase();
      return [...candidate].length === 1 ? candidate : character;
    })
    .join('');
  return lowered.normalize('NFC');
}

export function compareTagNames(left: string, right: string): number {
  const a = tagLookupKey(left);
  const b = tagLookupKey(right);
  if (a !== b) return a < b ? -1 : 1;
  return left < right ? -1 : left > right ? 1 : 0;
}

