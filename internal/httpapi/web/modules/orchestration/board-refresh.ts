let refreshBoard: ((slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null) => Promise<void>) | null = null;
let refreshSprintsOnly: ((slug: string) => Promise<void>) | null = null;
let boardLimitPerLaneFloor = 20;
/** Board that raised the floor; cross-board loads must ignore a stale elevated floor. */
let boardLimitPerLaneFloorSlug: string | null = null;

/** Coalesce rapid invalidates (e.g. resume resync + SSE-driven refresh) to reduce duplicate fetches. */
const INVALIDATE_COALESCE_MS = 700;
let lastInvalidate: { key: string; at: number } | null = null;

function invalidateCoalesceKey(slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null): string {
  return `${slug}\t${tag ?? ''}\t${search ?? ''}\t${sprintId ?? ''}\t${assignee ?? ''}\t${sort ?? ''}`;
}

export function registerBoardRefresher(
  fn: (slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null) => Promise<void>
) {
  refreshBoard = fn;
}

export function registerSprintsRefresher(fn: (slug: string) => Promise<void>) {
  refreshSprintsOnly = fn;
}

/**
 * Maintained full-board reload entrypoint used by realtime, resume resync, and
 * explicit UI follow-up refreshes after board-affecting mutations. Exact
 * duplicate slug/tag/search/sprintId/assignee/sort invalidates are coalesced within
 * INVALIDATE_COALESCE_MS.
 */
export async function invalidateBoard(slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null) {
  if (!refreshBoard) return;
  const now = Date.now();
  const key = invalidateCoalesceKey(slug, tag, search, sprintId, assignee, sort);
  if (lastInvalidate && lastInvalidate.key === key && now - lastInvalidate.at < INVALIDATE_COALESCE_MS) {
    return;
  }
  lastInvalidate = { key, at: now };
  await refreshBoard(slug, tag, search, sprintId, assignee, sort);
}

export function setBoardLimitPerLaneFloor(limit: number, slug: string) {
  if (!slug) return;
  if (boardLimitPerLaneFloorSlug !== slug) {
    boardLimitPerLaneFloor = 20;
    boardLimitPerLaneFloorSlug = slug;
  }
  if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
    boardLimitPerLaneFloor = Math.max(20, Math.floor(limit));
  }
}

export function getBoardLimitPerLaneFloor(forSlug: string): number {
  if (!boardLimitPerLaneFloorSlug || boardLimitPerLaneFloorSlug !== forSlug) return 20;
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
export async function refreshSprintsAndChips(slug: string) {
  if (!refreshSprintsOnly) return;
  await refreshSprintsOnly(slug);
}
