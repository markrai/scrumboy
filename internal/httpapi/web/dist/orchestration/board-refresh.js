let refreshBoard = null;
let refreshSprintsOnly = null;
export const CARDS_PER_LANE_PREFERENCE_KEY = 'cardsPerLane';
export const CARDS_PER_LANE_ALLOWED = [20, 50, 75, 100];
export const CARDS_PER_LANE_DEFAULT = 20;
export function normalizeCardsPerLane(n) {
    if (!Number.isFinite(n))
        return CARDS_PER_LANE_DEFAULT;
    const floored = Math.floor(n);
    return CARDS_PER_LANE_ALLOWED.includes(floored)
        ? floored
        : CARDS_PER_LANE_DEFAULT;
}
// User's preferred default lane page size (from the "cardsPerLane" preference).
// Falls back to CARDS_PER_LANE_DEFAULT until the preference has loaded (or for
// anonymous/logged-out sessions, which never load one).
let defaultCardsPerLane = CARDS_PER_LANE_DEFAULT;
let boardLimitPerLaneFloor = defaultCardsPerLane;
/** Board that raised the floor; cross-board loads must ignore a stale elevated floor. */
let boardLimitPerLaneFloorSlug = null;
/** When true, the next board fetch uses the preference baseline (ignores on-screen DOM size). */
let forcePreferenceLimitOnce = false;
/** Coalesce rapid invalidates (e.g. resume resync + SSE-driven refresh) to reduce duplicate fetches. */
const INVALIDATE_COALESCE_MS = 700;
let lastInvalidate = null;
function invalidateCoalesceKey(slug, tag, search, sprintId, assignee, sort, priority) {
    return `${slug}\t${tag ?? ''}\t${search ?? ''}\t${sprintId ?? ''}\t${assignee ?? ''}\t${sort ?? ''}\t${priority ?? ''}`;
}
export function registerBoardRefresher(fn) {
    refreshBoard = fn;
}
export function registerSprintsRefresher(fn) {
    refreshSprintsOnly = fn;
}
/**
 * Maintained full-board reload entrypoint used by realtime, resume resync, and
 * explicit UI follow-up refreshes after board-affecting mutations. Exact
 * duplicate slug/tag/search/sprintId/assignee/sort/priority invalidates are coalesced within
 * INVALIDATE_COALESCE_MS.
 */
export async function invalidateBoard(slug, tag, search, sprintId, assignee, sort, priority, force = false) {
    if (!refreshBoard)
        return;
    const now = Date.now();
    const key = invalidateCoalesceKey(slug, tag, search, sprintId, assignee, sort, priority);
    if (!force && lastInvalidate && lastInvalidate.key === key && now - lastInvalidate.at < INVALIDATE_COALESCE_MS) {
        return;
    }
    lastInvalidate = { key, at: now };
    await refreshBoard(slug, tag, search, sprintId, assignee, sort, priority);
}
export function setBoardLimitPerLaneFloor(limit, slug) {
    if (!slug)
        return;
    if (boardLimitPerLaneFloorSlug !== slug) {
        boardLimitPerLaneFloor = defaultCardsPerLane;
        boardLimitPerLaneFloorSlug = slug;
    }
    if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
        boardLimitPerLaneFloor = Math.max(defaultCardsPerLane, Math.floor(limit));
    }
}
export function getBoardLimitPerLaneFloor(forSlug) {
    if (!boardLimitPerLaneFloorSlug || boardLimitPerLaneFloorSlug !== forSlug)
        return defaultCardsPerLane;
    return boardLimitPerLaneFloor;
}
export function resetBoardLimitPerLaneFloor() {
    boardLimitPerLaneFloor = defaultCardsPerLane;
    boardLimitPerLaneFloorSlug = null;
}
/** Set the user's preferred default lane page size (must be one of CARDS_PER_LANE_ALLOWED). */
export function setDefaultCardsPerLane(n) {
    if (!Number.isFinite(n))
        return;
    defaultCardsPerLane = normalizeCardsPerLane(n);
    // Reset floor to the new default so subsequent loads/prefetches use it immediately
    // (do not keep a prior board's elevated floor after the preference changes).
    boardLimitPerLaneFloor = defaultCardsPerLane;
    boardLimitPerLaneFloorSlug = null;
}
/**
 * Make the next board request use {@link getDefaultCardsPerLane} as the limit,
 * ignoring on-screen "Load more" DOM size. Used after the cards-per-lane preference
 * changes so decreasing the default can shrink the visible page again.
 */
export function usePreferenceLimitOnNextBoardRequest() {
    forcePreferenceLimitOnce = true;
}
/** Consume and return whether the next board fetch should force the preference baseline. */
export function consumeForcePreferenceLimit() {
    if (!forcePreferenceLimitOnce)
        return false;
    forcePreferenceLimitOnce = false;
    return true;
}
export function getDefaultCardsPerLane() {
    return defaultCardsPerLane;
}
/**
 * Refresh sprint chips only (fetch sprints API, update chip UI).
 * Use when sprint list changes (create/update/delete) but board payload is unchanged.
 * This intentionally does not behave like invalidateBoard(): no full board
 * reload, no member refetch, and no todo reload.
 */
export async function refreshSprintsAndChips(slug) {
    if (!refreshSprintsOnly)
        return;
    await refreshSprintsOnly(slug);
}
