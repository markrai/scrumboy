let refreshBoard: ((slug: string, tag?: string, search?: string, sprintId?: string | null) => Promise<void>) | null = null;
let refreshSprintsOnly: ((slug: string) => Promise<void>) | null = null;
let boardLimitPerLaneFloor = 20;

/** Coalesce rapid invalidates (e.g. resume resync + SSE-driven refresh) to reduce duplicate fetches. */
const INVALIDATE_COALESCE_MS = 700;
let lastInvalidate: { key: string; at: number } | null = null;

function invalidateCoalesceKey(slug: string, tag?: string, search?: string, sprintId?: string | null): string {
  return `${slug}\t${tag ?? ''}\t${search ?? ''}\t${sprintId ?? ''}`;
}

export function registerBoardRefresher(
  fn: (slug: string, tag?: string, search?: string, sprintId?: string | null) => Promise<void>
) {
  refreshBoard = fn;
}

export function registerSprintsRefresher(fn: (slug: string) => Promise<void>) {
  refreshSprintsOnly = fn;
}

export async function invalidateBoard(slug: string, tag?: string, search?: string, sprintId?: string | null) {
  if (!refreshBoard) return;
  const now = Date.now();
  const key = invalidateCoalesceKey(slug, tag, search, sprintId);
  if (lastInvalidate && lastInvalidate.key === key && now - lastInvalidate.at < INVALIDATE_COALESCE_MS) {
    return;
  }
  lastInvalidate = { key, at: now };
  await refreshBoard(slug, tag, search, sprintId);
}

export function setBoardLimitPerLaneFloor(limit: number) {
  if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
    boardLimitPerLaneFloor = Math.max(20, Math.floor(limit));
  }
}

export function getBoardLimitPerLaneFloor(): number {
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
export async function refreshSprintsAndChips(slug: string) {
  if (!refreshSprintsOnly) return;
  await refreshSprintsOnly(slug);
}
