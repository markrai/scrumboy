// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  AGENDA_NOW_LINE_DEFAULT,
  AGENDA_NOW_LINE_OWNER_KEY,
  AGENDA_NOW_LINE_PREFERENCE_KEY,
  AGENDA_NOW_LINE_PROMINENT,
  AGENDA_NOW_LINE_STORAGE_KEY,
  AGENDA_NOW_LINE_SUBTLE,
  getAgendaNowLinePreference,
  hydrateAgendaNowLineFromServer,
  isAgendaNowLineProminent,
  loadAgendaNowLinePreferenceFromServer,
  normalizeAgendaNowLine,
  onAgendaNowLineAuthUserChanged,
  saveAgendaNowLinePreference,
  setAgendaNowLinePreference,
} from './agenda-now-line-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('agenda now-line preferences', () => {
  it('defaults to the subtle dotted line', () => {
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_DEFAULT);
    expect(isAgendaNowLineProminent()).toBe(false);
  });

  it('persists a prominent line locally', () => {
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    expect(localStorage.getItem(AGENDA_NOW_LINE_STORAGE_KEY)).toBe(AGENDA_NOW_LINE_PROMINENT);
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_PROMINENT);
    expect(isAgendaNowLineProminent()).toBe(true);
  });

  it('normalizes unknown values to subtle', () => {
    expect(normalizeAgendaNowLine('prominent')).toBe(AGENDA_NOW_LINE_PROMINENT);
    expect(normalizeAgendaNowLine('true')).toBe(AGENDA_NOW_LINE_PROMINENT);
    expect(normalizeAgendaNowLine('subtle')).toBe(AGENDA_NOW_LINE_SUBTLE);
    expect(normalizeAgendaNowLine('unexpected')).toBe(AGENDA_NOW_LINE_DEFAULT);
    expect(normalizeAgendaNowLine('')).toBe(AGENDA_NOW_LINE_DEFAULT);
  });

  it('hydrates invalid server values to the default', () => {
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    hydrateAgendaNowLineFromServer('unexpected');
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_DEFAULT);
  });

  it('loadAgendaNowLinePreferenceFromServer resets stale local value when server preference is missing', async () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    await loadAgendaNowLinePreferenceFromServer(async () => ({ value: '' }));
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_DEFAULT);
  });

  it('saves the preference through the existing user preference endpoint when signed in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    await saveAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: AGENDA_NOW_LINE_PREFERENCE_KEY, value: AGENDA_NOW_LINE_PROMINENT }),
    }));
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_PROMINENT);
  });

  it('restores the previous local value when remote save fails', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('network'));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaNowLinePreference(AGENDA_NOW_LINE_SUBTLE);

    await expect(saveAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT)).rejects.toThrow('network');
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_SUBTLE);
  });

  it('does not leak user A now-line style to user B', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_PROMINENT);
    setUser({ id: 2, name: 'Bob' });
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_DEFAULT);
  });

  it('clears cached now-line style when auth identity changes', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    onAgendaNowLineAuthUserChanged(2);
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_DEFAULT);
  });

  it('keeps the same user now-line style when the stored owner matches', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaNowLinePreference(AGENDA_NOW_LINE_PROMINENT);
    onAgendaNowLineAuthUserChanged(1);
    expect(getAgendaNowLinePreference()).toBe(AGENDA_NOW_LINE_PROMINENT);
    expect(localStorage.getItem(AGENDA_NOW_LINE_OWNER_KEY)).toBe('1');
  });
});
