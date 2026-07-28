import type { Board } from '../types.js';

const boardPrefetchPromises = new Map<string, Promise<Board>>();
const resolvedBoardBySlug = new Map<string, Board>();

/** Bumped on clear so in-flight prefetch completions cannot repopulate stale boards. */
let prefetchGeneration = 0;

export function clearBoardPrefetchCache(): void {
  prefetchGeneration += 1;
  boardPrefetchPromises.clear();
  resolvedBoardBySlug.clear();
}

/**
 * Start a hover prefetch for a project board. Completions after
 * {@link clearBoardPrefetchCache} are ignored via a generation token.
 */
export function beginBoardPrefetch(slug: string, fetch: () => Promise<Board>): void {
  if (!slug || boardPrefetchPromises.has(slug)) return;
  const generation = prefetchGeneration;
  const p = fetch();
  boardPrefetchPromises.set(slug, p);
  p.then((board) => {
    if (generation !== prefetchGeneration) return;
    resolvedBoardBySlug.set(slug, board);
  }).catch(() => {});
}

/** Take and remove a resolved prefetched board, if any. */
export function takeResolvedPrefetchedBoard(slug: string): Board | undefined {
  const board = resolvedBoardBySlug.get(slug);
  if (board) resolvedBoardBySlug.delete(slug);
  return board;
}
