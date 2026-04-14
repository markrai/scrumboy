let refreshBoard = null;
let refreshSprintsOnly = null;
let boardLimitPerLaneFloor = 20;
/** Coalesce rapid invalidates (e.g. resume resync + SSE-driven refresh) to reduce duplicate fetches. */
const INVALIDATE_COALESCE_MS = 700;
let lastInvalidate = null;
function invalidateCoalesceKey(slug, tag, search, sprintId) {
    return `${slug}\t${tag ?? ''}\t${search ?? ''}\t${sprintId ?? ''}`;
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
 * duplicate slug/tag/search/sprintId invalidates are coalesced within
 * INVALIDATE_COALESCE_MS.
 */
export async function invalidateBoard(slug, tag, search, sprintId) {
    if (!refreshBoard)
        return;
    const now = Date.now();
    const key = invalidateCoalesceKey(slug, tag, search, sprintId);
    if (lastInvalidate && lastInvalidate.key === key && now - lastInvalidate.at < INVALIDATE_COALESCE_MS) {
        return;
    }
    lastInvalidate = { key, at: now };
    await refreshBoard(slug, tag, search, sprintId);
}
export function setBoardLimitPerLaneFloor(limit) {
    if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
        boardLimitPerLaneFloor = Math.max(20, Math.floor(limit));
    }
}
export function getBoardLimitPerLaneFloor() {
    return boardLimitPerLaneFloor;
}
export function resetBoardLimitPerLaneFloor() {
    boardLimitPerLaneFloor = 20;
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
