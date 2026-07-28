let refreshBoard: ((slug: string, tag?: string, search?: string, sprintId?: string | null, assignee?: string | null, sort?: string | null) => Promise<void>) | null = null;
let refreshSprintsOnly: ((slug: string) => Promise<void>) | null = null;

export const CARDS_PER_LANE_PREFERENCE_KEY = 'cardsPerLane';
export const CARDS_PER_LANE_ALLOWED = [20, 50, 75, 100] as const;
export const CARDS_PER_LANE_DEFAULT = 20;

export function normalizeCardsPerLane(n: number): number {
  if (!Number.isFinite(n)) return CARDS_PER_LANE_DEFAULT;
  const floored = Math.floor(n);
  return (CARDS_PER_LANE_ALLOWED as readonly number[]).includes(floored)
    ? floored
    : CARDS_PER_LANE_DEFAULT;
}

// User's preferred default lane page size (from the "cardsPerLane" preference).
// Falls back to CARDS_PER_LANE_DEFAULT until the preference has loaded (or for
// anonymous/logged-out sessions, which never load one).
let defaultCardsPerLane = CARDS_PER_LANE_DEFAULT;
let boardLimitPerLaneFloor = defaultCardsPerLane;
/** Board that raised the floor; cross-board loads must ignore a stale elevated floor. */
let boardLimitPerLaneFloorSlug: string | null = null;
/** When true, the next board fetch uses the preference baseline (ignores on-screen DOM size). */
let forcePreferenceLimitOnce = false;

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

/** Set the user's preferred default lane page size (must be one of CARDS_PER_LANE_ALLOWED). */
export function setDefaultCardsPerLane(n: number): void {
  if (!Number.isFinite(n)) return;
  defaultCardsPerLane = normalizeCardsPerLane(n);
  // Reset floor to the new default so subsequent loads/prefetches use it immediately
  // (do not keep a prior board's elevated floor after the preference changes).
  boardLimitPerLaneFloor = defaultCardsPerLane;
  boardLimitPerLaneFloorSlug = null;
}

/**
 * Make the next board request use {@link getDefaultCardsPerLane} as the limit,
 * ignoring on-screen "Load more" DOM size. Used after the cards-per-lane preference
 * changes so decreasing the default can shrink the visible page again.
 */
export function usePreferenceLimitOnNextBoardRequest(): void {
  forcePreferenceLimitOnce = true;
}

/** Consume and return whether the next board fetch should force the preference baseline. */
export function consumeForcePreferenceLimit(): boolean {
  if (!forcePreferenceLimitOnce) return false;
  forcePreferenceLimitOnce = false;
  return true;
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
