let lastLocalMutationTimestamp = 0;
let lastBoardInteractionTimestamp = 0;

export function recordLocalMutation(): void {
  const now = Date.now();
  lastLocalMutationTimestamp = now;
  lastBoardInteractionTimestamp = now;
}

export function getLastLocalMutationTimestamp(): number {
  return lastLocalMutationTimestamp;
}

export function recordBoardInteraction(): void {
  lastBoardInteractionTimestamp = Date.now();
}

export function getLastBoardInteractionTimestamp(): number {
  return lastBoardInteractionTimestamp;
}
