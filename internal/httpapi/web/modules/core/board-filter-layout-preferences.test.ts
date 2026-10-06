// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  BOARD_FILTER_LAYOUT_PREFERENCE_KEY,
  BOARD_FILTER_LAYOUT_STORAGE_KEY,
  getBoardFilterLayoutPreference,
  hydrateBoardFilterLayoutFromServer,
  loadBoardFilterLayoutPreferenceFromServer,
  normalizeBoardFilterLayout,
  setBoardFilterLayoutPreference,
} from './board-filter-layout-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('board filter layout preferences', () => {
  it('defaults missing and invalid values to Omni', () => {
    expect(getBoardFilterLayoutPreference()).toBe('omni');
    localStorage.setItem(BOARD_FILTER_LAYOUT_STORAGE_KEY, 'unexpected');
    expect(getBoardFilterLayoutPreference()).toBe('omni');
    expect(normalizeBoardFilterLayout('legacy')).toBe('legacy');
    expect(normalizeBoardFilterLayout('omni')).toBe('omni');
    expect(normalizeBoardFilterLayout(null)).toBe('omni');
  });

  it('persists both supported layouts locally', () => {
    setBoardFilterLayoutPreference('legacy', { skipRemote: true });
    expect(getBoardFilterLayoutPreference()).toBe('legacy');
    setBoardFilterLayoutPreference('omni', { skipRemote: true });
    expect(getBoardFilterLayoutPreference()).toBe('omni');
  });

  it('hydrates invalid, missing, and failed server values back to Omni', async () => {
    hydrateBoardFilterLayoutFromServer('invalid');
    expect(getBoardFilterLayoutPreference()).toBe('omni');

    setBoardFilterLayoutPreference('legacy', { skipRemote: true });
    await loadBoardFilterLayoutPreferenceFromServer(async () => ({ value: '' }));
    expect(getBoardFilterLayoutPreference()).toBe('omni');

    setBoardFilterLayoutPreference('legacy', { skipRemote: true });
    await loadBoardFilterLayoutPreferenceFromServer(async () => {
      throw new Error('network');
    });
    expect(getBoardFilterLayoutPreference()).toBe('omni');
  });

  it('hydrates a valid Legacy server value', async () => {
    await loadBoardFilterLayoutPreferenceFromServer(async () => ({ value: 'legacy' }));
    expect(getBoardFilterLayoutPreference()).toBe('legacy');
  });

  it('persists remotely only for signed-in users', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    setBoardFilterLayoutPreference('legacy');
    expect(fetchMock).not.toHaveBeenCalled();

    setUser({ id: 1, name: 'Ada' });
    setBoardFilterLayoutPreference('omni');
    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: BOARD_FILTER_LAYOUT_PREFERENCE_KEY, value: 'omni' }),
    }));
  });
});
