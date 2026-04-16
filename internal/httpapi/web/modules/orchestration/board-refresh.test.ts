import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('board-refresh orchestration', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-13T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it('invalidateBoard calls the registered board refresher with the incoming arguments', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42');

    expect(refreshBoard).toHaveBeenCalledTimes(1);
    expect(refreshBoard).toHaveBeenCalledWith('alpha', 'tag-a', 'query', '42');
  });

  it('coalesces identical invalidates within 700ms', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42');
    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42');

    expect(refreshBoard).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(701);
    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42');

    expect(refreshBoard).toHaveBeenCalledTimes(2);
  });

  it('does not coalesce when the invalidate key changes', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42');
    await mod.invalidateBoard('alpha', 'tag-a', 'other-query', '42');
    await mod.invalidateBoard('alpha', 'tag-a', 'other-query', '43');

    expect(refreshBoard).toHaveBeenCalledTimes(3);
    expect(refreshBoard).toHaveBeenNthCalledWith(1, 'alpha', 'tag-a', 'query', '42');
    expect(refreshBoard).toHaveBeenNthCalledWith(2, 'alpha', 'tag-a', 'other-query', '42');
    expect(refreshBoard).toHaveBeenNthCalledWith(3, 'alpha', 'tag-a', 'other-query', '43');
  });

  it('refreshSprintsAndChips calls only the sprint refresher', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);
    const refreshSprints = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);
    mod.registerSprintsRefresher(refreshSprints);

    await mod.refreshSprintsAndChips('alpha');

    expect(refreshSprints).toHaveBeenCalledTimes(1);
    expect(refreshSprints).toHaveBeenCalledWith('alpha');
    expect(refreshBoard).not.toHaveBeenCalled();
  });
});
