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

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42', '7');

    expect(refreshBoard).toHaveBeenCalledTimes(1);
    expect(refreshBoard).toHaveBeenCalledWith('alpha', 'tag-a', 'query', '42', '7', undefined);
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
    expect(refreshBoard).toHaveBeenNthCalledWith(1, 'alpha', 'tag-a', 'query', '42', undefined, undefined);
    expect(refreshBoard).toHaveBeenNthCalledWith(2, 'alpha', 'tag-a', 'other-query', '42', undefined, undefined);
    expect(refreshBoard).toHaveBeenNthCalledWith(3, 'alpha', 'tag-a', 'other-query', '43', undefined, undefined);
  });

  it('does not coalesce when only the assignee filter changes', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42', 'unassigned');
    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42', 'me');

    expect(refreshBoard).toHaveBeenCalledTimes(2);
    expect(refreshBoard).toHaveBeenNthCalledWith(1, 'alpha', 'tag-a', 'query', '42', 'unassigned', undefined);
    expect(refreshBoard).toHaveBeenNthCalledWith(2, 'alpha', 'tag-a', 'query', '42', 'me', undefined);
  });

  it('does not coalesce when only the sort order changes', async () => {
    const mod = await import('./board-refresh.js');
    const refreshBoard = vi.fn().mockResolvedValue(undefined);

    mod.registerBoardRefresher(refreshBoard);

    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42', null, 'newest');
    await mod.invalidateBoard('alpha', 'tag-a', 'query', '42', null, 'oldest');

    expect(refreshBoard).toHaveBeenCalledTimes(2);
    expect(refreshBoard).toHaveBeenNthCalledWith(1, 'alpha', 'tag-a', 'query', '42', null, 'newest');
    expect(refreshBoard).toHaveBeenNthCalledWith(2, 'alpha', 'tag-a', 'query', '42', null, 'oldest');
  });

  it('setDefaultCardsPerLane clears any elevated floor slug so the new default applies immediately', async () => {
    const mod = await import('./board-refresh.js');

    mod.setBoardLimitPerLaneFloor(90, 'alpha');
    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(90);

    mod.setDefaultCardsPerLane(50);
    expect(mod.getDefaultCardsPerLane()).toBe(50);
    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(50);
  });

  it('setDefaultCardsPerLane accepts allowlisted presets and falls back invalid values to 20', async () => {
    const mod = await import('./board-refresh.js');

    mod.setDefaultCardsPerLane(50);
    expect(mod.getDefaultCardsPerLane()).toBe(50);

    mod.setDefaultCardsPerLane(75);
    expect(mod.getDefaultCardsPerLane()).toBe(75);

    mod.setDefaultCardsPerLane(100);
    expect(mod.getDefaultCardsPerLane()).toBe(100);

    mod.setDefaultCardsPerLane(5);
    expect(mod.getDefaultCardsPerLane()).toBe(20);

    mod.setDefaultCardsPerLane(42);
    expect(mod.getDefaultCardsPerLane()).toBe(20);

    mod.setDefaultCardsPerLane(200);
    expect(mod.getDefaultCardsPerLane()).toBe(20);

    mod.setDefaultCardsPerLane(50);
    mod.setDefaultCardsPerLane(NaN);
    expect(mod.getDefaultCardsPerLane()).toBe(50);
  });

  it('setBoardLimitPerLaneFloor only raises the floor, never lowers below the default', async () => {
    const mod = await import('./board-refresh.js');

    mod.setDefaultCardsPerLane(50);
    mod.setBoardLimitPerLaneFloor(10, 'alpha');
    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(50);

    mod.setBoardLimitPerLaneFloor(60, 'alpha');
    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(60);
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

  it('keeps an elevated lane floor for the same board after a filtered drag', async () => {
    const mod = await import('./board-refresh.js');

    mod.setBoardLimitPerLaneFloor(45, 'alpha');

    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(45);
  });

  it('does not apply an elevated lane floor to a different board', async () => {
    const mod = await import('./board-refresh.js');

    mod.setBoardLimitPerLaneFloor(45, 'alpha');

    expect(mod.getBoardLimitPerLaneFloor('beta')).toBe(20);
  });

  it('resets the lane floor after a successful board response', async () => {
    const mod = await import('./board-refresh.js');

    mod.setBoardLimitPerLaneFloor(45, 'alpha');
    mod.resetBoardLimitPerLaneFloor();

    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(20);
  });

  it('replacing the floor slug clears a prior board elevation', async () => {
    const mod = await import('./board-refresh.js');

    mod.setBoardLimitPerLaneFloor(45, 'alpha');
    mod.setBoardLimitPerLaneFloor(25, 'beta');

    expect(mod.getBoardLimitPerLaneFloor('alpha')).toBe(20);
    expect(mod.getBoardLimitPerLaneFloor('beta')).toBe(25);
  });
});
