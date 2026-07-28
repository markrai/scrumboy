// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  applyWrapLanesClass,
  getWrapLanesPreference,
  hydrateWrapLanesFromServer,
  loadWrapLanesPreferenceFromServer,
  normalizeWrapLanes,
  setWrapLanesPreference,
  shouldWrapBoardLanes,
  WRAP_LANES_STORAGE_KEY,
} from './wrap-lanes-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('wrap lanes preferences', () => {
  it('defaults wrap lanes to off and persists enabled locally', () => {
    expect(getWrapLanesPreference()).toBe(false);
    setWrapLanesPreference(true, { skipRemote: true });
    expect(localStorage.getItem(WRAP_LANES_STORAGE_KEY)).toBe('true');
    expect(getWrapLanesPreference()).toBe(true);
  });

  it('normalizes wrap lanes values', () => {
    expect(normalizeWrapLanes(true)).toBe(true);
    expect(normalizeWrapLanes('true')).toBe(true);
    expect(normalizeWrapLanes('1')).toBe(true);
    expect(normalizeWrapLanes('on')).toBe(true);
    expect(normalizeWrapLanes(false)).toBe(false);
    expect(normalizeWrapLanes('false')).toBe(false);
    expect(normalizeWrapLanes('unexpected')).toBe(false);
  });

  it('hydrates invalid server values back to off', () => {
    hydrateWrapLanesFromServer('unexpected');
    expect(getWrapLanesPreference()).toBe(false);
  });

  it('loadWrapLanesPreferenceFromServer resets stale local true when server preference is missing', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    await loadWrapLanesPreferenceFromServer(async () => ({ value: '' }));
    expect(getWrapLanesPreference()).toBe(false);
  });

  it('loadWrapLanesPreferenceFromServer keeps false when fetch fails after reset', async () => {
    setWrapLanesPreference(true, { skipRemote: true });
    await loadWrapLanesPreferenceFromServer(async () => {
      throw new Error('network');
    });
    expect(getWrapLanesPreference()).toBe(false);
  });

  it('loadWrapLanesPreferenceFromServer applies server true after reset', async () => {
    setWrapLanesPreference(false, { skipRemote: true });
    await loadWrapLanesPreferenceFromServer(async () => ({ value: 'true' }));
    expect(getWrapLanesPreference()).toBe(true);
  });

  it('saves the wrap lanes preference through the existing user preference endpoint when signed in', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setWrapLanesPreference(true);

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'wrapLanes', value: 'true' }),
    }));
  });

  it('does not PUT when skipRemote is set', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setWrapLanesPreference(true, { skipRemote: true });

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('does not remote-save when user is not signed in', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    setUser(null);

    setWrapLanesPreference(true);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(localStorage.getItem(WRAP_LANES_STORAGE_KEY)).toBe('true');
  });
});

describe('shouldWrapBoardLanes', () => {
  it('returns false when preference is off regardless of lane count', () => {
    setWrapLanesPreference(false, { skipRemote: true });
    expect(shouldWrapBoardLanes(6)).toBe(false);
    expect(shouldWrapBoardLanes(7)).toBe(false);
  });

  it('returns false when preference is on but lane count is five or fewer', () => {
    setWrapLanesPreference(true, { skipRemote: true });
    expect(shouldWrapBoardLanes(5)).toBe(false);
    expect(shouldWrapBoardLanes(4)).toBe(false);
  });

  it('returns true when preference is on and lane count exceeds five', () => {
    setWrapLanesPreference(true, { skipRemote: true });
    expect(shouldWrapBoardLanes(6)).toBe(true);
    expect(shouldWrapBoardLanes(7)).toBe(true);
  });
});

describe('applyWrapLanesClass', () => {
  it('adds board--wrapped when wrapping should apply', () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const boardEl = document.createElement('div');
    boardEl.className = 'board';
    applyWrapLanesClass(boardEl, 6);
    expect(boardEl.classList.contains('board--wrapped')).toBe(true);
  });

  it('removes board--wrapped when wrapping should not apply', () => {
    setWrapLanesPreference(true, { skipRemote: true });
    const boardEl = document.createElement('div');
    boardEl.className = 'board board--wrapped';
    applyWrapLanesClass(boardEl, 4);
    expect(boardEl.classList.contains('board--wrapped')).toBe(false);
  });
});
