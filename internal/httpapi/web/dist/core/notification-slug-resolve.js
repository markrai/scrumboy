/**
 * Pure slug pick for assignment notifications (unit-tested).
 * Order: id→slug map, then optional catalog slug, then event/localStorage slug.
 * Null, undefined, and "" are treated as missing for eventSlug.
 */
export function resolveNotificationProjectSlugCore(eventSlug, mapSlug, catalogSlug) {
    if (typeof mapSlug === 'string' && mapSlug.length > 0) {
        return mapSlug;
    }
    if (typeof catalogSlug === 'string' && catalogSlug.length > 0) {
        return catalogSlug;
    }
    if (typeof eventSlug === 'string' && eventSlug.trim() !== '') {
        return eventSlug;
    }
    return null;
}
