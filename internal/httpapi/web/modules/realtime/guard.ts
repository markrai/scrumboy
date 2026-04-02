let lastLocalMutationTimestamp = 0;
let lastBoardInteractionTimestamp = 0;

/** When true, SSE must not trigger board refetch (bulk edit in progress). */
let bulkUpdating = false;

export function setBulkUpdating(v: boolean): void {
  bulkUpdating = v;
}

export function isBulkUpdating(): boolean {
  return bulkUpdating;
}

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
