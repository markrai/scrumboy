// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  EMAIL_NOTIFY_PREF_KEY,
  getStoredEmailNotifyPref,
  hydrateEmailNotifyFromServer,
  parseEmailNotifyPref,
  setEmailNotifyPref,
} from './email-notify-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('Email notification preferences', () => {
  it('defaults to disabled with assigned + addedToProject pre-checked', () => {
    const pref = getStoredEmailNotifyPref();
    expect(pref.enabled).toBe(false);
    expect(pref.assigned).toBe(true);
    expect(pref.addedToProject).toBe(true);
    expect(pref.cardActivity).toBe(false);
    expect(pref.sprintActivity).toBe(false);
    expect(pref.projectActivity).toBe(false);
  });

  it('parses a full preference blob', () => {
    const pref = parseEmailNotifyPref(JSON.stringify({
      v: 1, enabled: true, assigned: false, cardActivity: true, sprintActivity: true, projectActivity: true, addedToProject: false,
    }));
    expect(pref).toEqual({
      v: 1, enabled: true, assigned: false, cardActivity: true, sprintActivity: true, projectActivity: true, addedToProject: false,
    });
  });

  it('falls back to defaults on malformed JSON', () => {
    const pref = parseEmailNotifyPref('not json');
    expect(pref.enabled).toBe(false);
    expect(pref.assigned).toBe(true);
  });

  it('hydrates from server value and persists locally', () => {
    hydrateEmailNotifyFromServer(JSON.stringify({ v: 1, enabled: true, cardActivity: true }));
    const pref = getStoredEmailNotifyPref();
    expect(pref.enabled).toBe(true);
    expect(pref.cardActivity).toBe(true);
  });

  it('hydrates undefined/non-string server values back to defaults', () => {
    hydrateEmailNotifyFromServer(undefined);
    const pref = getStoredEmailNotifyPref();
    expect(pref.enabled).toBe(false);
  });

  it('saves the preference through the existing user preference endpoint when signed in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    const next = { v: 1 as const, enabled: true, assigned: true, cardActivity: false, sprintActivity: false, projectActivity: false, addedToProject: true };
    await setEmailNotifyPref(next);

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: EMAIL_NOTIFY_PREF_KEY, value: JSON.stringify(next) }),
    }));
    expect(getStoredEmailNotifyPref()).toEqual(next);
  });

  it('does not call the network when signed out', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    const next = { v: 1 as const, enabled: true, assigned: true, cardActivity: false, sprintActivity: false, projectActivity: false, addedToProject: true };
    await setEmailNotifyPref(next);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(getStoredEmailNotifyPref()).toEqual(next);
  });
});
