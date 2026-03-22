let refreshBoard = null;
let refreshSprintsOnly = null;
let boardLimitPerLaneFloor = 20;
export function registerBoardRefresher(fn) {
    refreshBoard = fn;
}
export function registerSprintsRefresher(fn) {
    refreshSprintsOnly = fn;
}
export async function invalidateBoard(slug, tag, search, sprintId) {
    if (!refreshBoard)
        return;
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
 * Does not refetch board, members, or todos.
 */
export async function refreshSprintsAndChips(slug) {
    if (!refreshSprintsOnly)
        return;
    await refreshSprintsOnly(slug);
}
