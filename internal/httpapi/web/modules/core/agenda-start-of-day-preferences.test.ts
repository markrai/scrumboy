// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  AGENDA_START_OF_DAY_DEFAULT,
  AGENDA_START_OF_DAY_OWNER_KEY,
  AGENDA_START_OF_DAY_STORAGE_KEY,
  agendaStartOfDayMinutes,
  getAgendaStartOfDayPreference,
  hydrateAgendaStartOfDayFromServer,
  loadAgendaStartOfDayPreferenceFromServer,
  normalizeAgendaStartOfDay,
  onAgendaStartOfDayAuthUserChanged,
  saveAgendaStartOfDayPreference,
  setAgendaStartOfDayPreference,
} from './agenda-start-of-day-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void } {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('agenda start of day preferences', () => {
  it('defaults to 08:00 when no preference is stored', () => {
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(agendaStartOfDayMinutes()).toBe(480);
  });

  it('persists a wall-clock start locally', () => {
    setAgendaStartOfDayPreference('07:30');
    expect(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY)).toBe('07:30');
    expect(getAgendaStartOfDayPreference()).toBe('07:30');
    expect(agendaStartOfDayMinutes()).toBe(450);
  });

  it('normalizes start-of-day values', () => {
    expect(normalizeAgendaStartOfDay('8:00')).toBe('08:00');
    expect(normalizeAgendaStartOfDay('07:30')).toBe('07:30');
    expect(normalizeAgendaStartOfDay('07:30:00')).toBe('07:30');
    expect(normalizeAgendaStartOfDay('23:59')).toBe('23:59');
    expect(normalizeAgendaStartOfDay('24:00')).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(normalizeAgendaStartOfDay('unexpected')).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(normalizeAgendaStartOfDay('')).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(normalizeAgendaStartOfDay(true)).toBe(AGENDA_START_OF_DAY_DEFAULT);
  });

  it('hydrates invalid server values back to 08:00', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    hydrateAgendaStartOfDayFromServer('unexpected');
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
  });

  it('loadAgendaStartOfDayPreferenceFromServer resets stale local value when server preference is missing', async () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    await loadAgendaStartOfDayPreferenceFromServer(async () => ({ value: '' }));
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
  });

  it('preserves the same user local start when hydration fetch fails', async () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    await loadAgendaStartOfDayPreferenceFromServer(async () => {
      throw new Error('network');
    });
    expect(getAgendaStartOfDayPreference()).toBe('06:00');
  });

  it('loadAgendaStartOfDayPreferenceFromServer applies server HH:MM', async () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');
    await loadAgendaStartOfDayPreferenceFromServer(async () => ({ value: '07:30' }));
    expect(getAgendaStartOfDayPreference()).toBe('07:30');
  });

  it('saves the preference through the existing user preference endpoint when signed in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    await saveAgendaStartOfDayPreference('06:00');

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'agendaStartOfDay', value: '06:00' }),
    }));
    expect(getAgendaStartOfDayPreference()).toBe('06:00');
  });

  it('restores the previous local value when remote save fails', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('network'));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');

    await expect(saveAgendaStartOfDayPreference('06:00')).rejects.toThrow('network');
    expect(getAgendaStartOfDayPreference()).toBe('08:00');
  });

  it('does not remote-save when user is not signed in', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    setUser(null);

    await saveAgendaStartOfDayPreference('06:00');

    expect(fetchMock).not.toHaveBeenCalled();
    expect(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY)).toBe('06:00');
  });

  it('does not leak user A start of day to user B', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    expect(getAgendaStartOfDayPreference()).toBe('06:00');

    setUser({ id: 2, name: 'Bob' });
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBe('1');
  });

  it('clears cached start of day when auth identity changes', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    setUser({ id: 2, name: 'Bob' });
    onAgendaStartOfDayAuthUserChanged(2);
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY)).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBe('2');
  });

  it('preserves same-user cached start when runtime auth starts empty', async () => {
    localStorage.setItem(AGENDA_START_OF_DAY_STORAGE_KEY, '06:00');
    localStorage.setItem(AGENDA_START_OF_DAY_OWNER_KEY, '1');
    setUser({ id: 1, name: 'Ada' });
    onAgendaStartOfDayAuthUserChanged(1);
    await loadAgendaStartOfDayPreferenceFromServer(async () => {
      throw new Error('network');
    });
    expect(getAgendaStartOfDayPreference()).toBe('06:00');
    expect(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY)).toBe('06:00');
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBe('1');
  });

  it('resets to 08:00 on logout so a previous owner cannot leak', () => {
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('06:00');
    setUser(null);
    onAgendaStartOfDayAuthUserChanged(null);
    expect(getAgendaStartOfDayPreference()).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY)).toBe(AGENDA_START_OF_DAY_DEFAULT);
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBeNull();
  });

  it('restores last confirmed start when overlapping saves both fail', async () => {
    const first = deferred<Response>();
    const second = deferred<Response>();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');

    const saveEarly = saveAgendaStartOfDayPreference('06:00');
    const saveDefault = saveAgendaStartOfDayPreference('08:00');
    first.reject(new Error('network'));
    second.reject(new Error('network'));
    await Promise.allSettled([saveEarly, saveDefault]);

    expect(getAgendaStartOfDayPreference()).toBe('08:00');
  });

  it('keeps the newer successful value when an older save later fails', async () => {
    const first = deferred<Response>();
    const second = deferred<Response>();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');

    const saveEarly = saveAgendaStartOfDayPreference('06:00');
    const saveDefault = saveAgendaStartOfDayPreference('08:00');
    second.resolve(new Response(null, { status: 204 }));
    await saveDefault;
    first.reject(new Error('network'));
    await expect(saveEarly).rejects.toThrow('network');

    expect(getAgendaStartOfDayPreference()).toBe('08:00');
  });

  it('does not let an older failure roll back a newer successful save', async () => {
    const first = deferred<Response>();
    const second = deferred<Response>();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');

    const saveDefault = saveAgendaStartOfDayPreference('08:00');
    const saveEarly = saveAgendaStartOfDayPreference('06:00');
    second.resolve(new Response(null, { status: 204 }));
    await saveEarly;
    first.reject(new Error('network'));
    await expect(saveDefault).rejects.toThrow('network');

    expect(getAgendaStartOfDayPreference()).toBe('06:00');
  });

  it('does not let an in-flight stale hydrate overwrite a newly saved start', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');

    const hydrate = deferred<{ value: string }>();
    const hydration = loadAgendaStartOfDayPreferenceFromServer(() => hydrate.promise);
    await saveAgendaStartOfDayPreference('06:00');
    hydrate.resolve({ value: '' });
    await hydration;

    expect(getAgendaStartOfDayPreference()).toBe('06:00');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('does not let user A in-flight save suppress or overwrite user B hydration', async () => {
    const save = deferred<Response>();
    const fetchMock = vi.fn().mockImplementation(() => save.promise);
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });
    setAgendaStartOfDayPreference('08:00');
    const saveA = saveAgendaStartOfDayPreference('06:00');

    setUser({ id: 2, name: 'Bob' });
    onAgendaStartOfDayAuthUserChanged(2);
    await loadAgendaStartOfDayPreferenceFromServer(async () => ({ value: '14:00' }));
    expect(getAgendaStartOfDayPreference()).toBe('14:00');
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBe('2');

    save.resolve(new Response(null, { status: 204 }));
    await saveA;
    expect(getAgendaStartOfDayPreference()).toBe('14:00');
    expect(localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY)).toBe('2');
  });
});
