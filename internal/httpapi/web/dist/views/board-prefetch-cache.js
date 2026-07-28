const boardPrefetchPromises = new Map();
const resolvedBoardBySlug = new Map();
/** Bumped on clear so in-flight prefetch completions cannot repopulate stale boards. */
let prefetchGeneration = 0;
export function clearBoardPrefetchCache() {
    prefetchGeneration += 1;
    boardPrefetchPromises.clear();
    resolvedBoardBySlug.clear();
}
/**
 * Start a hover prefetch for a project board. Completions after
 * {@link clearBoardPrefetchCache} are ignored via a generation token.
 */
export function beginBoardPrefetch(slug, fetch) {
    if (!slug || boardPrefetchPromises.has(slug))
        return;
    const generation = prefetchGeneration;
    const p = fetch();
    boardPrefetchPromises.set(slug, p);
    p.then((board) => {
        if (generation !== prefetchGeneration)
            return;
        resolvedBoardBySlug.set(slug, board);
    }).catch(() => { });
}
/** Take and remove a resolved prefetched board, if any. */
export function takeResolvedPrefetchedBoard(slug) {
    const board = resolvedBoardBySlug.get(slug);
    if (board)
        resolvedBoardBySlug.delete(slug);
    return board;
}
