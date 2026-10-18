import { VoiceCreatePlanError } from './voice-create-plan.js';
import { getBoard, getSlug } from '../state/selectors.js';
function isTagWire(value) {
    return !!value
        && typeof value === 'object'
        && !Array.isArray(value)
        && typeof value.name === 'string'
        && value.name.trim().length > 0
        && (!('canonicalName' in value)
            || typeof value.canonicalName === 'string');
}
/**
 * canonicalName is computed by the Go server and is the name accepted by name-based
 * writes. This collapses temporary-board legacy rows without asking JavaScript to
 * reproduce Unicode casing. Older projections fall back to their stored name.
 */
export function combineVoiceCreateTags(projectTags) {
    if (!projectTags.every(isTagWire)) {
        throw new VoiceCreatePlanError('network');
    }
    const byKey = new Map();
    for (const tag of projectTags) {
        const writeName = tag.canonicalName === undefined ? tag.name : tag.canonicalName;
        if (!writeName)
            continue;
        const entry = Object.freeze({ name: writeName });
        if (!byKey.has(entry.name))
            byKey.set(entry.name, entry);
    }
    return Object.freeze([...byKey.values()].sort((a, b) => a.name === b.name ? 0 : a.name < b.name ? -1 : 1));
}
/** Shared production/device adapter for the loaded board's active tag projection. */
export async function readVoiceCreateTags(projectSlug, signal) {
    const board = getBoard();
    if (signal.aborted || getSlug() !== projectSlug || !board)
        throw new VoiceCreatePlanError('network');
    return combineVoiceCreateTags(board.tags.filter((tag) => tag.count > 0));
}
