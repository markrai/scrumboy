let refreshBoard = null;
let refreshSprintsOnly = null;
let boardLimitPerLaneFloor = 20;
/** Board that raised the floor; cross-board loads must ignore a stale elevated floor. */
let boardLimitPerLaneFloorSlug = null;
/** Coalesce rapid invalidates (e.g. resume resync + SSE-driven refresh) to reduce duplicate fetches. */
const INVALIDATE_COALESCE_MS = 700;
let lastInvalidate = null;
function invalidateCoalesceKey(slug, tag, search, sprintId, assignee, sort) {
    return `${slug}\t${tag ?? ''}\t${search ?? ''}\t${sprintId ?? ''}\t${assignee ?? ''}\t${sort ?? ''}`;
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
 * duplicate slug/tag/search/sprintId/assignee/sort invalidates are coalesced within
 * INVALIDATE_COALESCE_MS.
 */
export async function invalidateBoard(slug, tag, search, sprintId, assignee, sort) {
    if (!refreshBoard)
        return;
    const now = Date.now();
    const key = invalidateCoalesceKey(slug, tag, search, sprintId, assignee, sort);
    if (lastInvalidate && lastInvalidate.key === key && now - lastInvalidate.at < INVALIDATE_COALESCE_MS) {
        return;
    }
    lastInvalidate = { key, at: now };
    await refreshBoard(slug, tag, search, sprintId, assignee, sort);
}
export function setBoardLimitPerLaneFloor(limit, slug) {
    if (!slug)
        return;
    if (boardLimitPerLaneFloorSlug !== slug) {
        boardLimitPerLaneFloor = 20;
        boardLimitPerLaneFloorSlug = slug;
    }
    if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
        boardLimitPerLaneFloor = Math.max(20, Math.floor(limit));
    }
}
export function getBoardLimitPerLaneFloor(forSlug) {
    if (!boardLimitPerLaneFloorSlug || boardLimitPerLaneFloorSlug !== forSlug)
        return 20;
    return boardLimitPerLaneFloor;
}
export function resetBoardLimitPerLaneFloor() {
    boardLimitPerLaneFloor = 20;
    boardLimitPerLaneFloorSlug = null;
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
