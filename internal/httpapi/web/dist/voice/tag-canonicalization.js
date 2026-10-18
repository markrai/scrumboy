const MAX_TAG_CODE_POINTS = 32;
function normalizeSeparators(input) {
    return String(input ?? '')
        .trim()
        .normalize('NFC')
        .replace(/\p{White_Space}+/gu, '-')
        .replace(/-+/g, '-')
        .replace(/^-+|-+$/g, '');
}
function hasValidTagStructure(value) {
    if (!value || [...value].length > MAX_TAG_CODE_POINTS)
        return false;
    let hasBaseInSegment = false;
    for (const character of value) {
        if (character === '-') {
            hasBaseInSegment = false;
        }
        else if (/^[\p{L}\p{N}]$/u.test(character)) {
            hasBaseInSegment = true;
        }
        else if (/^\p{M}$/u.test(character) && hasBaseInSegment) {
            // A mark may extend a base (or a preceding mark on that same base).
        }
        else {
            return false;
        }
    }
    return hasBaseInSegment;
}
/**
 * Client preflight for the server-owned tag contract. It intentionally validates
 * before JavaScript lowercasing: JS can expand a character while Go uses simple,
 * one-code-point Unicode lowercase mappings (for example U+0130). The server is
 * authoritative and must receive every value this predicate accepts.
 */
export function isTagNameCandidate(input) {
    return hasValidTagStructure(normalizeSeparators(input));
}
/**
 * Best-effort display preview of CanonicalizeTag. Never use its output as a
 * persistent identity key or as the reason to reject a command; Go owns both.
 */
export function canonicalizeTagName(input) {
    const candidate = normalizeSeparators(input);
    if (!hasValidTagStructure(candidate))
        return null;
    return candidate.toLowerCase().normalize('NFC');
}
