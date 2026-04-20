// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setUser } from '../state/mutations.js';
import {
  getVoiceFlowEnabledPreference,
  getVoiceFlowHandsFreeConfirmationPreference,
  getVoiceFlowModePreference,
  hydrateVoiceFlowEnabledFromServer,
  hydrateVoiceFlowHandsFreeConfirmationFromServer,
  hydrateVoiceFlowModeFromServer,
  normalizeVoiceFlowEnabled,
  normalizeVoiceFlowHandsFreeConfirmation,
  setVoiceFlowEnabledPreference,
  setVoiceFlowHandsFreeConfirmationPreference,
  setVoiceFlowModePreference,
  VOICE_FLOW_ENABLED_STORAGE_KEY,
  VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY,
  VOICE_FLOW_MODE_STORAGE_KEY,
} from './voiceflow-preferences.js';

beforeEach(() => {
  localStorage.clear();
  setUser(null);
  vi.unstubAllGlobals();
});

afterEach(() => {
  setUser(null);
  vi.unstubAllGlobals();
});

describe('VoiceFlow preferences', () => {
  it('defaults voice commands to enabled and persists disabled locally', () => {
    expect(getVoiceFlowEnabledPreference()).toBe(true);
    setVoiceFlowEnabledPreference(false, { skipRemote: true });
    expect(localStorage.getItem(VOICE_FLOW_ENABLED_STORAGE_KEY)).toBe('false');
    expect(getVoiceFlowEnabledPreference()).toBe(false);
  });

  it('hydrates invalid voice command enabled values back to enabled', () => {
    hydrateVoiceFlowEnabledFromServer('unexpected');
    expect(getVoiceFlowEnabledPreference()).toBe(true);
  });

  it('normalizes voice command enabled values', () => {
    expect(normalizeVoiceFlowEnabled(true)).toBe(true);
    expect(normalizeVoiceFlowEnabled('true')).toBe(true);
    expect(normalizeVoiceFlowEnabled(false)).toBe(false);
    expect(normalizeVoiceFlowEnabled('false')).toBe(false);
    expect(normalizeVoiceFlowEnabled('0')).toBe(false);
    expect(normalizeVoiceFlowEnabled('off')).toBe(false);
    expect(normalizeVoiceFlowEnabled('unexpected')).toBe(true);
  });

  it('saves the voice command enabled preference through the existing user preference endpoint when signed in', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setVoiceFlowEnabledPreference(false);

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'voiceFlowEnabled', value: 'false' }),
    }));
  });

  it('defaults to Safe-Mode and persists Hands-Free locally', () => {
    expect(getVoiceFlowModePreference()).toBe('safe');
    setVoiceFlowModePreference('hands-free', { skipRemote: true });
    expect(localStorage.getItem(VOICE_FLOW_MODE_STORAGE_KEY)).toBe('hands-free');
    expect(getVoiceFlowModePreference()).toBe('hands-free');
  });

  it('hydrates invalid server values back to Safe-Mode', () => {
    hydrateVoiceFlowModeFromServer('unexpected');
    expect(getVoiceFlowModePreference()).toBe('safe');
  });

  it('saves the mode through the existing user preference endpoint when signed in', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setVoiceFlowModePreference('hands-free');

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'voiceFlowMode', value: 'hands-free' }),
    }));
  });

  it('defaults to confirming deletes only and persists mutating confirmation locally', () => {
    expect(getVoiceFlowHandsFreeConfirmationPreference()).toBe('deletes');
    setVoiceFlowHandsFreeConfirmationPreference('mutations', { skipRemote: true });
    expect(localStorage.getItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY)).toBe('mutations');
    expect(getVoiceFlowHandsFreeConfirmationPreference()).toBe('mutations');
  });

  it('hydrates invalid confirmation values back to delete confirmations only', () => {
    hydrateVoiceFlowHandsFreeConfirmationFromServer('unexpected');
    expect(getVoiceFlowHandsFreeConfirmationPreference()).toBe('deletes');
  });

  it('normalizes confirmation preference values', () => {
    expect(normalizeVoiceFlowHandsFreeConfirmation('deletes')).toBe('deletes');
    expect(normalizeVoiceFlowHandsFreeConfirmation('mutations')).toBe('mutations');
    expect(normalizeVoiceFlowHandsFreeConfirmation('unexpected')).toBe('deletes');
  });

  it('saves the confirmation policy through the existing user preference endpoint when signed in', () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    setUser({ id: 1, name: 'Ada' });

    setVoiceFlowHandsFreeConfirmationPreference('mutations');

    expect(fetchMock).toHaveBeenCalledWith('/api/user/preferences', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ key: 'voiceFlowHandsFreeConfirmation', value: 'mutations' }),
    }));
  });
});
