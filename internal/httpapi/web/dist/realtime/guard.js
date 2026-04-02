let lastLocalMutationTimestamp = 0;
let lastBoardInteractionTimestamp = 0;
/** When true, SSE must not trigger board refetch (bulk edit in progress). */
let bulkUpdating = false;
export function setBulkUpdating(v) {
    bulkUpdating = v;
}
export function isBulkUpdating() {
    return bulkUpdating;
}
export function recordLocalMutation() {
    const now = Date.now();
    lastLocalMutationTimestamp = now;
    lastBoardInteractionTimestamp = now;
}
export function getLastLocalMutationTimestamp() {
    return lastLocalMutationTimestamp;
}
export function recordBoardInteraction() {
    lastBoardInteractionTimestamp = Date.now();
}
export function getLastBoardInteractionTimestamp() {
    return lastBoardInteractionTimestamp;
}
