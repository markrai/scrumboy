// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetUserScopedState, setUser } from '../state/mutations.js';
import { getEmailNotifyPreferenceState } from '../state/selectors.js';
import {
  EMAIL_NOTIFY_PREF_KEY,
  defaultEmailNotifyPref,
  getStoredEmailNotifyPref,
  hydrateEmailNotifyFromServer,
  loadUserEmailNotifyPref,
  parseEmailNotifyPref,
  setEmailNotifyPref,
} from './email-notify-preferences.js';

const enabledPref = {
  v: 1 as const,
  enabled: true,
  assigned: true,
  cardActivity: true,
  sprintActivity: false,
  projectActivity: false,
  addedToProject: true,
};

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  resetUserScopedState();
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  resetUserScopedState();
  vi.unstubAllGlobals();
});

describe('Email notification preference JSON', () => {
  it.each([
    ['unset', undefined, defaultEmailNotifyPref()],
    ['empty', '', defaultEmailNotifyPref()],
    ['empty object', '{}', defaultEmailNotifyPref()],
    ['missing version', '{"enabled":true}', { ...defaultEmailNotifyPref(), enabled: true }],
    ['partial v1', '{"v":1,"cardActivity":true}', { ...defaultEmailNotifyPref(), cardActivity: true }],
    ['numeric v1', '{"v":1.0,"enabled":true}', { ...defaultEmailNotifyPref(), enabled: true }],
    ['explicit false', '{"assigned":false,"addedToProject":false}', { ...defaultEmailNotifyPref(), assigned: false, addedToProject: false }],
    ['complete v1', JSON.stringify(enabledPref), enabledPref],
  ])('parses %s with canonical defaults', (_name, raw, expected) => {
    expect(parseEmailNotifyPref(raw)).toEqual(expected);
  });

  it.each([
    ['null', 'null'],
    ['array', '[]'],
    ['malformed', '{'],
    ['unsupported version', '{"v":2}'],
    ['null version', '{"v":null}'],
    ['string version', '{"v":"1"}'],
    ['fractional version', '{"v":1.5}'],
    ['invalid boolean', '{"enabled":"true"}'],
    ['null boolean', '{"assigned":null}'],
    ['unknown field', '{"future":true}'],
  ])('rejects %s', (_name, raw) => {
    expect(() => parseEmailNotifyPref(raw)).toThrow('invalid email notification preference');
  });
});

describe('Authenticated email notification preferences', () => {
  it('hydrates canonical defaults from the route no-preference representation', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ value: '' }), { status: 200 })));
    setUser({ id: 1, name: 'Ada' });

    await expect(loadUserEmailNotifyPref()).resolves.toBe(true);
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: 1, status: 'ready', value: defaultEmailNotifyPref() });
  });

  it.each([
    ['null', { value: null }],
    ['omitted', {}],
  ])('does not normalize a %s server value to defaults', async (_name, responseBody) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(responseBody), { status: 200 })));
    setUser({ id: 1, name: 'Ada' });

    await expect(loadUserEmailNotifyPref()).resolves.toBe(false);
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: 1, status: 'error', value: null });
  });

  it('ignores stale localStorage and represents a rejected initial GET as an error with no value', async () => {
    localStorage.setItem('scrumboy_emailNotifications', JSON.stringify(enabledPref));
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('failed', { status: 500 })));
    setUser({ id: 2, name: 'Bob' });

    await expect(loadUserEmailNotifyPref()).resolves.toBe(false);
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: 2, status: 'error', value: null });
    expect(getStoredEmailNotifyPref()).toBeNull();
  });

  it('clears account A state before account B and does not expose A when B hydration fails', async () => {
    setUser({ id: 1, name: 'Ada' });
    hydrateEmailNotifyFromServer(JSON.stringify(enabledPref));
    expect(getStoredEmailNotifyPref()).toEqual(enabledPref);

    setUser(null);
    resetUserScopedState();
    setUser({ id: 2, name: 'Bob' });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('failed', { status: 500 })));

    await expect(loadUserEmailNotifyPref()).resolves.toBe(false);
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: 2, status: 'error', value: null });
    expect(getStoredEmailNotifyPref()).toBeNull();
  });

  it('keeps the previous visible value after a rejected PUT and rethrows the persistence error', async () => {
    setUser({ id: 1, name: 'Ada' });
    const previous = defaultEmailNotifyPref();
    hydrateEmailNotifyFromServer(JSON.stringify(previous));
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('failed', { status: 500 })));

    await expect(setEmailNotifyPref(enabledPref)).rejects.toThrow();
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: 1, status: 'ready', value: previous });
  });

  it('saves successfully on retry and emits a complete canonical v1 object', async () => {
    setUser({ id: 1, name: 'Ada' });
    hydrateEmailNotifyFromServer('{}');
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('failed', { status: 500 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(setEmailNotifyPref(enabledPref)).rejects.toThrow();
    await expect(setEmailNotifyPref(enabledPref)).resolves.toBeUndefined();

    const request = JSON.parse(fetchMock.mock.calls[1][1].body as string);
    expect(request).toEqual({
      key: EMAIL_NOTIFY_PREF_KEY,
      value: '{"v":1,"enabled":true,"assigned":true,"cardActivity":true,"sprintActivity":false,"projectActivity":false,"addedToProject":true}',
    });
    expect(getStoredEmailNotifyPref()).toEqual(enabledPref);
  });

  it('does not apply a stale hydration after the active account changes', () => {
    setUser({ id: 1, name: 'Ada' });
    resetUserScopedState();
    setUser({ id: 2, name: 'Bob' });

    expect(() => hydrateEmailNotifyFromServer(JSON.stringify(enabledPref), 1)).toThrow('user changed');
    expect(getEmailNotifyPreferenceState()).toEqual({ userId: null, status: 'idle', value: null });
  });
});

describe('Anonymous email notification preferences', () => {
  it('uses localStorage without calling the network', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    await setEmailNotifyPref(enabledPref);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(getStoredEmailNotifyPref()).toEqual(enabledPref);
  });
});
