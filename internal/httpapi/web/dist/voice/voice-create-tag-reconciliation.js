import { spokenReferenceIdentity } from './normalize.js';
import { matchVoiceTagsDetailed } from './resolve.js';
import { VOICE_CREATE_LIMITS, VoiceCreatePlanError } from './voice-create-plan.js';
/** Planner output cannot contain a longer adjacent tag run than this. */
export const VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN = VOICE_CREATE_LIMITS.tags;
function failTagMatch(match) {
    throw new VoiceCreatePlanError('tag', {
        entityType: 'tag',
        result: match.matches.length === 0 ? 'unavailable' : 'ambiguous',
        candidateCount: match.matches.length,
        referenceNormalizationApplied: match.kind === 'spoken_identity',
    });
}
/**
 * Resolves independent references first. Only a maximal adjacent run in which
 * every member has zero matches may be coalesced through the existing spoken
 * identity grammar, and only when that whole run has exactly one match.
 */
export function reconcileVoiceCreateTagReferences(references, authoritativeTags) {
    const individual = references.map(reference => matchVoiceTagsDetailed(reference, authoritativeTags));
    const tags = [];
    let referenceNormalizationApplied = false;
    for (let index = 0; index < references.length;) {
        const match = individual[index];
        if (match.matches.length > 1)
            failTagMatch(match);
        if (match.matches.length === 1) {
            tags.push(match.matches[0]);
            referenceNormalizationApplied || (referenceNormalizationApplied = match.kind === 'spoken_identity');
            index += 1;
            continue;
        }
        let end = index + 1;
        while (end < references.length
            && end - index < VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN
            && individual[end].matches.length === 0)
            end += 1;
        const run = references.slice(index, end);
        if (run.length < 2 || spokenReferenceIdentity(run.join(' ')).spelled === null)
            failTagMatch(match);
        const joined = matchVoiceTagsDetailed(run.join(' '), authoritativeTags);
        if (joined.matches.length !== 1)
            failTagMatch(joined);
        tags.push(joined.matches[0]);
        referenceNormalizationApplied = true;
        index = end;
    }
    return Object.freeze({ tags: Object.freeze(tags), referenceNormalizationApplied });
}
