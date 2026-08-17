// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  BOARD_TODO_SORT_STORAGE_KEY,
  boardTodoSortUrlParam,
  getBoardTodoSortPreference,
  hydrateBoardTodoSortFromServer,
  isBoardTodoSortUrlParam,
  loadBoardTodoSortPreferenceFromServer,
  normalizeBoardTodoSort,
  setBoardTodoSortPreference,
} from './board-sort-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('board todo sort preferences', () => {
  it('defaults missing values to default and persists locally', () => {
    expect(getBoardTodoSortPreference()).toBe('default');
    setBoardTodoSortPreference('newest', { skipRemote: true });
    expect(localStorage.getItem(BOARD_TODO_SORT_STORAGE_KEY)).toBe('newest');
    expect(getBoardTodoSortPreference()).toBe('newest');
  });

  it('normalizes missing and invalid values to default', () => {
    expect(normalizeBoardTodoSort(undefined)).toBe('default');
    expect(normalizeBoardTodoSort(null)).toBe('default');
    expect(normalizeBoardTodoSort('')).toBe('default');
    expect(normalizeBoardTodoSort('unexpected')).toBe('default');
    expect(normalizeBoardTodoSort('newest')).toBe('newest');
    expect(normalizeBoardTodoSort('oldest')).toBe('oldest');
    expect(normalizeBoardTodoSort('default')).toBe('default');
  });

  it('hydrates invalid server values back to default', () => {
    setBoardTodoSortPreference('newest', { skipRemote: true });
    hydrateBoardTodoSortFromServer('unexpected');
    expect(getBoardTodoSortPreference()).toBe('default');
  });

  it('loadBoardTodoSortPreferenceFromServer resets stale local newest when server preference is missing', async () => {
    setBoardTodoSortPreference('newest', { skipRemote: true });
    await loadBoardTodoSortPreferenceFromServer(async () => ({ value: '' }));
    expect(getBoardTodoSortPreference()).toBe('default');
  });

  it('loadBoardTodoSortPreferenceFromServer keeps default when fetch fails after reset', async () => {
    setBoardTodoSortPreference('oldest', { skipRemote: true });
    await loadBoardTodoSortPreferenceFromServer(async () => {
      throw new Error('network');
    });
    expect(getBoardTodoSortPreference()).toBe('default');
  });

  it('loadBoardTodoSortPreferenceFromServer applies server newest after reset', async () => {
    setBoardTodoSortPreference('oldest', { skipRemote: true });
    await loadBoardTodoSortPreferenceFromServer(async () => ({ value: 'newest' }));
    expect(getBoardTodoSortPreference()).toBe('newest');
  });

  it('loadBoardTodoSortPreferenceFromServer applies server oldest after reset', async () => {
    await loadBoardTodoSortPreferenceFromServer(async () => ({ value: 'oldest' }));
    expect(getBoardTodoSortPreference()).toBe('oldest');
  });

  it('saves the board sort preference through the existing user preference endpoint when signed in', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setBoardTodoSortPreference('newest');

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'boardTodoSort', value: 'newest' }),
    }));
  });

  it('does not PUT when skipRemote is set', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setBoardTodoSortPreference('oldest', { skipRemote: true });

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not write local or remote preference state when user is not signed in', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    setUser(null);

    setBoardTodoSortPreference('newest');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(localStorage.getItem(BOARD_TODO_SORT_STORAGE_KEY)).toBeNull();
    expect(getBoardTodoSortPreference()).toBe('default');
  });

  it('treats only newest and oldest as valid URL sort overrides', () => {
    expect(isBoardTodoSortUrlParam('newest')).toBe(true);
    expect(isBoardTodoSortUrlParam('oldest')).toBe(true);
    expect(isBoardTodoSortUrlParam('default')).toBe(false);
    expect(isBoardTodoSortUrlParam('')).toBe(false);
    expect(isBoardTodoSortUrlParam(null)).toBe(false);
    expect(isBoardTodoSortUrlParam('bogus')).toBe(false);
  });

  it('maps a saved preference to a URL sort param only for newest and oldest', () => {
    expect(boardTodoSortUrlParam('newest')).toBe('newest');
    expect(boardTodoSortUrlParam('oldest')).toBe('oldest');
    expect(boardTodoSortUrlParam('default')).toBeNull();
  });
});
