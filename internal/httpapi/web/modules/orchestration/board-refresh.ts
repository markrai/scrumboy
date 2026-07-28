let refreshBoard: ((slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null) => Promise<void>) | null = null;
let refreshSprintsOnly: ((slug: string) => Promise<void>) | null = null;

export const CARDS_PER_LANE_PREFERENCE_KEY = 'cardsPerLane';
export const CARDS_PER_LANE_MIN = 5;
export const CARDS_PER_LANE_MAX = 100;
export const CARDS_PER_LANE_DEFAULT = 20;

export function clampCardsPerLane(n: number): number {
  return Math.min(CARDS_PER_LANE_MAX, Math.max(CARDS_PER_LANE_MIN, Math.floor(n)));
}

// User's preferred default lane page size (from the "cardsPerLane" preference).
// Falls back to CARDS_PER_LANE_DEFAULT until the preference has loaded (or for
// anonymous/logged-out sessions, which never load one).
let defaultCardsPerLane = CARDS_PER_LANE_DEFAULT;
let boardLimitPerLaneFloor = defaultCardsPerLane;
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
    boardLimitPerLaneFloor = defaultCardsPerLane;
    boardLimitPerLaneFloorSlug = slug;
  }
  if (Number.isFinite(limit) && limit > boardLimitPerLaneFloor) {
    boardLimitPerLaneFloor = Math.max(defaultCardsPerLane, Math.floor(limit));
  }
}

export function getBoardLimitPerLaneFloor(forSlug: string): number {
  if (!boardLimitPerLaneFloorSlug || boardLimitPerLaneFloorSlug !== forSlug) return defaultCardsPerLane;
  return boardLimitPerLaneFloor;
}

export function resetBoardLimitPerLaneFloor() {
  boardLimitPerLaneFloor = defaultCardsPerLane;
  boardLimitPerLaneFloorSlug = null;
}

/** Set the user's preferred default lane page size (clamped to [CARDS_PER_LANE_MIN, CARDS_PER_LANE_MAX]). */
export function setDefaultCardsPerLane(n: number): void {
  if (!Number.isFinite(n)) return;
  defaultCardsPerLane = clampCardsPerLane(n);
  boardLimitPerLaneFloor = defaultCardsPerLane;
}

export function getDefaultCardsPerLane(): number {
  return defaultCardsPerLane;
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
