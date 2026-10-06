import { normalizeLookup } from './normalize.js';
export const VOICE_CREATE_TAG_REPAIR_VERSION = 'voice-create-tag-repair-v1';
export const VOICE_CREATE_TAG_REPAIR_MAX_CANDIDATES = 5;
export const VOICE_CREATE_TAG_REPAIR_PROMPT = `Select only authoritative existing tags explicitly intended by the original utterance.
Input is JSON containing the original utterance and failed planner tag references. Each reference lists a small closed set of candidate {id,name} objects.
Words such as "it", "this", and "that" may refer to the Todo being created rather than being tag names. Use the complete original utterance to decide.
Return exactly {"version":1,"selections":[{"referenceIndex":0,"tagId":"candidate-1"}]} with at most one candidate for each reference.
Every tagId must be copied exactly from that reference's supplied candidates. Never produce a tag name, new tag, explanation, markdown, or extra field.
If any reference is uncertain, ambiguous, unsupported, or has no explicitly intended candidate, return {"version":1,"selections":[]}.
A single planner reference must never be split into multiple tags.`;
function tokens(value) {
    return normalizeLookup(value).split(' ').filter(Boolean);
}
function containsSequence(haystack, needle) {
    if (!needle.length || needle.length > haystack.length)
        return false;
    for (let start = 0; start <= haystack.length - needle.length; start += 1) {
        if (needle.every((token, offset) => haystack[start + offset] === token))
            return true;
    }
    return false;
}
/** Lexical retrieval only. It never resolves or authorizes a tag. */
export function createVoiceCreateTagRepairRequest(transcript, references, unresolvedReferenceIndexes, authoritativeTags) {
    const names = [...new Set(authoritativeTags.map(tag => tag.name))];
    const candidateNamesByReference = unresolvedReferenceIndexes.map(referenceIndex => {
        const referenceTokens = tokens(references[referenceIndex] ?? '');
        const candidates = names.filter(name => containsSequence(referenceTokens, tokens(name)));
        return { referenceIndex, candidates };
    });
    if (!candidateNamesByReference.length || candidateNamesByReference.some(group => group.candidates.length === 0))
        return null;
    const candidateNames = [...new Set(candidateNamesByReference.flatMap(group => group.candidates))];
    if (!candidateNames.length || candidateNames.length > VOICE_CREATE_TAG_REPAIR_MAX_CANDIDATES)
        return null;
    if (new Set(candidateNames.map(name => normalizeLookup(name))).size !== candidateNames.length)
        return null;
    const ids = new Map(candidateNames.map((name, index) => [name, `candidate-${index + 1}`]));
    const repairReferences = candidateNamesByReference.map(group => Object.freeze({
        referenceIndex: group.referenceIndex,
        reference: references[group.referenceIndex],
        candidates: Object.freeze(group.candidates.map(name => Object.freeze({ id: ids.get(name), name }))),
    }));
    return Object.freeze({ transcript, references: Object.freeze(repairReferences) });
}
function result(value, request, bindings = []) {
    const candidateCount = new Set(request.references.flatMap(reference => reference.candidates.map(candidate => candidate.id))).size;
    return Object.freeze({ result: value, candidateCount, bindings: Object.freeze([...bindings]) });
}
/** Strict closed-world validation. The model never returns an executable tag name. */
export function parseVoiceCreateTagRepairResult(raw, request) {
    if (typeof raw !== 'string' || raw.length > 2048 || !/^\s*\{[\s\S]*\}\s*$/.test(raw))
        return result('invalid', request);
    let value;
    try {
        value = JSON.parse(raw);
    }
    catch {
        return result('invalid', request);
    }
    if (!value || typeof value !== 'object' || Array.isArray(value))
        return result('invalid', request);
    const object = value;
    if (Object.keys(object).some(key => key !== 'version' && key !== 'selections')
        || object.version !== 1
        || !Array.isArray(object.selections))
        return result('invalid', request);
    if (object.selections.length === 0)
        return result('none', request);
    if (object.selections.length !== request.references.length)
        return result('ambiguous', request);
    const references = new Map(request.references.map(reference => [reference.referenceIndex, reference]));
    const seen = new Set();
    const bindings = [];
    for (const item of object.selections) {
        if (!item || typeof item !== 'object' || Array.isArray(item))
            return result('invalid', request);
        const selection = item;
        if (Object.keys(selection).some(key => key !== 'referenceIndex' && key !== 'tagId')
            || !Number.isInteger(selection.referenceIndex)
            || typeof selection.tagId !== 'string')
            return result('invalid', request);
        const referenceIndex = selection.referenceIndex;
        const reference = references.get(referenceIndex);
        if (!reference || seen.has(referenceIndex))
            return result('invalid', request);
        const candidate = reference.candidates.find(value => value.id === selection.tagId);
        if (!candidate)
            return result('invalid', request);
        seen.add(referenceIndex);
        bindings.push(Object.freeze({ referenceIndex, tag: candidate.name }));
    }
    return result('selected', request, bindings);
}
let nextRequest = 0;
/** One optional local-model call over a bounded closed candidate set; no retries. */
export function createVoiceCreateTagSemanticRepair(capability) {
    return async (request, signal) => {
        if (signal.aborted)
            return result('invalid', request);
        const requestId = `${VOICE_CREATE_TAG_REPAIR_VERSION}-${++nextRequest}`;
        try {
            const response = await capability.generate({
                requestId,
                input: JSON.stringify({ originalUtterance: request.transcript, failedReferences: request.references }),
                instructions: VOICE_CREATE_TAG_REPAIR_PROMPT,
                maximumOutputTokens: 128,
                signal,
            });
            if (signal.aborted || response.requestId !== requestId)
                return result('invalid', request);
            return parseVoiceCreateTagRepairResult(response.text, request);
        }
        catch {
            return result('invalid', request);
        }
    };
}
