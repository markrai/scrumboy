import { apiFetch } from '../api.js';
import { canonicalizeTagName } from './tag-canonicalization.js';
import { VoiceCreatePlanError } from './voice-create-plan.js';
function isTagWire(value) {
    return !!value
        && typeof value === 'object'
        && !Array.isArray(value)
        && typeof value.name === 'string'
        && value.name.trim().length > 0;
}
function tagGroupKey(name) {
    return canonicalizeTagName(name) ?? name;
}
/**
 * Mirrors the store's TagGroupKey union semantics. Project labels win only as
 * the representation for a duplicate logical label; identity remains its name.
 */
export function combineVoiceCreateTags(projectTags, personalTags) {
    if (!projectTags.every(isTagWire) || !personalTags.every(isTagWire)) {
        throw new VoiceCreatePlanError('network');
    }
    const byKey = new Map();
    for (const tag of [...projectTags, ...personalTags]) {
        const entry = Object.freeze({ name: tag.name });
        if (!byKey.has(tagGroupKey(entry.name)))
            byKey.set(tagGroupKey(entry.name), entry);
    }
    return Object.freeze([...byKey.values()].sort((a, b) => a.name === b.name ? 0 : a.name < b.name ? -1 : 1));
}
/** Shared production/device adapter for project labels plus the caller's personal library. */
export async function readVoiceCreateTags(projectSlug, signal, fetcher = apiFetch) {
    const [projectTags, personalTags] = await Promise.all([
        fetcher(`/api/board/${encodeURIComponent(projectSlug)}/tags`, { signal }),
        fetcher('/api/tags/mine', { signal }),
    ]);
    if (!Array.isArray(projectTags) || !Array.isArray(personalTags)) {
        throw new VoiceCreatePlanError('network');
    }
    return combineVoiceCreateTags(projectTags, personalTags);
}
