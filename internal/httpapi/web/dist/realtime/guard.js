let lastLocalMutationTimestamp = 0;
let lastBoardInteractionTimestamp = 0;
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
