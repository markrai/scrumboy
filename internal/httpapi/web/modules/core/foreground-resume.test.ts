// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { hydrateNotificationsMock, invalidateBoardMock } = vi.hoisted(() => ({
  hydrateNotificationsMock: vi.fn(),
  invalidateBoardMock: vi.fn(async () => undefined),
}));

vi.mock('../orchestration/board-refresh.js', () => ({ invalidateBoard: invalidateBoardMock }));
vi.mock('../state/selectors.js', () => ({
  getAssigneeFromUrl: () => '42',
  getPriorityFromUrl: () => 'high',
  getSortFromUrl: () => 'newest',
  getAuthStatusAvailable: () => true,
  getSlug: () => 'alpha',
  getTag: () => 'bug',
  getSearch: () => 'login',
  getSprintIdFromUrl: () => '7',
  getUser: () => ({ id: 11 }),
}));
vi.mock('./notifications.js', () => ({ hydrateNotificationsForUser: hydrateNotificationsMock }));

import { scheduleResumeResync } from './foreground-resume.js';

describe('foreground resume convergence', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-28T12:00:00Z'));
    invalidateBoardMock.mockClear();
    hydrateNotificationsMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('debounces adjacent native and browser resume signals into one resync', async () => {
    scheduleResumeResync('native-foreground');
    scheduleResumeResync('visibility');
    scheduleResumeResync('pageshow-bfcache');

    await vi.advanceTimersByTimeAsync(399);
    expect(invalidateBoardMock).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(invalidateBoardMock).toHaveBeenCalledOnce();
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'login', '7', '42', 'newest', 'high');
    expect(hydrateNotificationsMock).toHaveBeenCalledOnce();
    expect(hydrateNotificationsMock).toHaveBeenCalledWith(11);
  });
});
