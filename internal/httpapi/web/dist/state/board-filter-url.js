export const MAX_BOARD_TAG_FILTERS = 20;
/**
 * Normalize the URL-facing tag list without importing durable tag identity.
 * The store remains authoritative for canonical aliases such as spaces versus
 * hyphens; the browser only trims and deduplicates case-insensitively.
 */
export function normalizeBoardTagFilters(tags) {
    const out = [];
    const seen = new Set();
    for (const raw of tags) {
        const tag = raw.trim();
        if (!tag)
            continue;
        const key = tag.toLocaleLowerCase();
        if (seen.has(key))
            continue;
        seen.add(key);
        out.push(tag);
        if (out.length >= MAX_BOARD_TAG_FILTERS)
            break;
    }
    return out;
}
export function getTagsFromUrl() {
    if (typeof window === 'undefined')
        return [];
    return normalizeBoardTagFilters(new URL(window.location.href).searchParams.getAll('tag'));
}
export function setTagParams(tags) {
    const normalized = normalizeBoardTagFilters(tags);
    const url = new URL(window.location.href);
    url.searchParams.delete('tag');
    for (const tag of normalized)
        url.searchParams.append('tag', tag);
    history.replaceState({}, '', url.pathname + url.search + url.hash);
    return normalized;
}
export function appendTagParams(params, tags) {
    for (const tag of normalizeBoardTagFilters(tags))
        params.append('tag', tag);
}
export function sameOrderedTags(left, right) {
    return left.length === right.length && left.every((tag, index) => tag === right[index]);
}
