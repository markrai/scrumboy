import { spokenReferenceIdentity } from './normalize.js';
import { matchVoiceTagsDetailed } from './resolve.js';
import { VOICE_CREATE_LIMITS, VoiceCreatePlanError } from './voice-create-plan.js';
/** Planner output cannot contain a longer adjacent tag run than this. */
export const VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN = VOICE_CREATE_LIMITS.tags;
/**
 * Keeps the generic matcher language-neutral. The sole Create-specific recovery
 * strips a leading object-pronoun `it` only after the original reference fails,
 * and only treats the recovery as successful when one authoritative tag wins.
 */
export function matchVoiceCreateTagReferenceDetailed(reference, authoritativeTags) {
    const original = matchVoiceTagsDetailed(reference, authoritativeTags);
    if (original.matches.length > 0) {
        return Object.freeze({
            match: original,
            referenceNormalizationApplied: original.kind === 'spoken_identity',
        });
    }
    const pronoun = /^\s*it\s+(.+?)\s*$/i.exec(reference);
    if (!pronoun)
        return Object.freeze({ match: original, referenceNormalizationApplied: false });
    const stripped = matchVoiceTagsDetailed(pronoun[1], authoritativeTags);
    return Object.freeze({
        match: stripped.matches.length > 0 ? stripped : original,
        referenceNormalizationApplied: stripped.matches.length === 1,
    });
}
function failTagMatch(reference, match, referenceNormalizationApplied = match.kind === 'spoken_identity') {
    throw new VoiceCreatePlanError('tag', {
        entityType: 'tag',
        reference,
        result: match.matches.length === 0 ? 'unavailable' : 'ambiguous',
        candidateCount: match.matches.length,
        referenceNormalizationApplied,
    });
}
/**
 * Resolves independent references first. Only a maximal adjacent run in which
 * every member has zero matches may be coalesced through the existing spoken
 * identity grammar, and only when that whole run has exactly one match.
 */
export function reconcileVoiceCreateTagReferences(references, authoritativeTags) {
    const individual = references.map(reference => matchVoiceCreateTagReferenceDetailed(reference, authoritativeTags));
    const tags = [];
    let referenceNormalizationApplied = false;
    for (let index = 0; index < references.length;) {
        const resolved = individual[index];
        const match = resolved.match;
        if (match.matches.length > 1)
            failTagMatch(references[index], match, resolved.referenceNormalizationApplied);
        if (match.matches.length === 1) {
            tags.push(match.matches[0]);
            referenceNormalizationApplied || (referenceNormalizationApplied = resolved.referenceNormalizationApplied);
            index += 1;
            continue;
        }
        let end = index + 1;
        while (end < references.length
            && end - index < VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN
            && individual[end].match.matches.length === 0)
            end += 1;
        const run = references.slice(index, end);
        if (run.length < 2 || spokenReferenceIdentity(run.join(' ')).spelled === null)
            failTagMatch(references[index], match);
        const joined = matchVoiceTagsDetailed(run.join(' '), authoritativeTags);
        if (joined.matches.length !== 1)
            failTagMatch(run.join(' '), joined);
        tags.push(joined.matches[0]);
        referenceNormalizationApplied = true;
        index = end;
    }
    return Object.freeze({ tags: Object.freeze(tags), referenceNormalizationApplied });
}
