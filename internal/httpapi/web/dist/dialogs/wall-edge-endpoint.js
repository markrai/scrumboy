import { measureStoryCanvasSize } from "./wall-story-geometry.js";
const STORY_ENDPOINT_PREFIX = "story:";
export function formatWallStoryEndpoint(storyLocalId) {
    return `${STORY_ENDPOINT_PREFIX}${storyLocalId}`;
}
export function parseWallEdgeEndpoint(raw) {
    const trimmed = raw.trim();
    if (!trimmed)
        return null;
    if (trimmed.startsWith(STORY_ENDPOINT_PREFIX)) {
        const rest = trimmed.slice(STORY_ENDPOINT_PREFIX.length);
        if (!/^\d+$/.test(rest))
            return null;
        const storyLocalId = Number(rest);
        if (!Number.isSafeInteger(storyLocalId) || storyLocalId <= 0)
            return null;
        return {
            kind: "story",
            noteId: null,
            storyLocalId,
            canonical: formatWallStoryEndpoint(storyLocalId),
        };
    }
    return { kind: "note", noteId: trimmed, storyLocalId: null, canonical: trimmed };
}
export function resolveWallEdgeElement(surface, endpoint) {
    if (endpoint.kind === "story") {
        return surface.querySelector(`.wall-story[data-story-local-id="${endpoint.storyLocalId}"]`);
    }
    return surface.querySelector(`.wall-note[data-note-id="${CSS.escape(endpoint.noteId ?? "")}"]`);
}
export function wallEdgeEndpointCenter(surface, endpoint, zoom) {
    const el = resolveWallEdgeElement(surface, endpoint);
    if (!el)
        return null;
    if (endpoint.kind === "story") {
        const size = measureStoryCanvasSize(el, zoom);
        return { cx: el.offsetLeft + size.width / 2, cy: el.offsetTop + size.height / 2 };
    }
    return { cx: el.offsetLeft + el.offsetWidth / 2, cy: el.offsetTop + el.offsetHeight / 2 };
}
