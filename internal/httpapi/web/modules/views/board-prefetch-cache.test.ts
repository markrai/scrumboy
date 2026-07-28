import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';

describe('board-prefetch-cache', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('stores a resolved prefetch and allows taking it once', async () => {
    const mod = await import('./board-prefetch-cache.js');
    const board = { project: { slug: 'alpha' } } as Board;

    mod.beginBoardPrefetch('alpha', () => Promise.resolve(board));
    await Promise.resolve();

    expect(mod.takeResolvedPrefetchedBoard('alpha')).toBe(board);
    expect(mod.takeResolvedPrefetchedBoard('alpha')).toBeUndefined();
  });

  it('clearBoardPrefetchCache drops resolved boards and ignores late in-flight completions', async () => {
    const mod = await import('./board-prefetch-cache.js');
    let resolvePrefetch!: (board: Board) => void;
    const pending = new Promise<Board>((resolve) => {
      resolvePrefetch = resolve;
    });
    const stale = { project: { slug: 'alpha' }, limit: 20 } as unknown as Board;
    const fresh = { project: { slug: 'alpha' }, limit: 100 } as unknown as Board;

    mod.beginBoardPrefetch('alpha', () => pending);
    mod.clearBoardPrefetchCache();

    resolvePrefetch(stale);
    await Promise.resolve();
    await Promise.resolve();

    expect(mod.takeResolvedPrefetchedBoard('alpha')).toBeUndefined();

    // After clear, a new prefetch for the same slug is allowed and can resolve.
    mod.beginBoardPrefetch('alpha', () => Promise.resolve(fresh));
    await Promise.resolve();
    expect(mod.takeResolvedPrefetchedBoard('alpha')).toBe(fresh);
  });
});
