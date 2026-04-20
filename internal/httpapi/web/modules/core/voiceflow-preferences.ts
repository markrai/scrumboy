import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';

export type VoiceFlowMode = "safe" | "hands-free";
export type VoiceFlowHandsFreeConfirmation = "deletes" | "mutations";

export const VOICE_FLOW_ENABLED_DEFAULT = true;
export const VOICE_FLOW_MODE_SAFE: VoiceFlowMode = "safe";
export const VOICE_FLOW_MODE_HANDS_FREE: VoiceFlowMode = "hands-free";
export const VOICE_FLOW_CONFIRM_DELETES: VoiceFlowHandsFreeConfirmation = "deletes";
export const VOICE_FLOW_CONFIRM_MUTATIONS: VoiceFlowHandsFreeConfirmation = "mutations";
export const VOICE_FLOW_ENABLED_STORAGE_KEY = "scrumboy.voiceFlowEnabled";
export const VOICE_FLOW_ENABLED_PREFERENCE_KEY = "voiceFlowEnabled";
export const VOICE_FLOW_MODE_STORAGE_KEY = "scrumboy.voiceFlowMode";
export const VOICE_FLOW_MODE_PREFERENCE_KEY = "voiceFlowMode";
export const VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY = "scrumboy.voiceFlowHandsFreeConfirmation";
export const VOICE_FLOW_HANDS_FREE_CONFIRMATION_PREFERENCE_KEY = "voiceFlowHandsFreeConfirmation";

export function normalizeVoiceFlowEnabled(value: unknown): boolean {
  return value === false || value === "false" || value === "0" || value === "off"
    ? false
    : VOICE_FLOW_ENABLED_DEFAULT;
}

export function normalizeVoiceFlowMode(value: unknown): VoiceFlowMode {
  return value === VOICE_FLOW_MODE_HANDS_FREE ? VOICE_FLOW_MODE_HANDS_FREE : VOICE_FLOW_MODE_SAFE;
}

export function normalizeVoiceFlowHandsFreeConfirmation(value: unknown): VoiceFlowHandsFreeConfirmation {
  return value === VOICE_FLOW_CONFIRM_MUTATIONS ? VOICE_FLOW_CONFIRM_MUTATIONS : VOICE_FLOW_CONFIRM_DELETES;
}

export function getVoiceFlowModePreference(): VoiceFlowMode {
  try {
    return normalizeVoiceFlowMode(localStorage.getItem(VOICE_FLOW_MODE_STORAGE_KEY));
  } catch {
    return VOICE_FLOW_MODE_SAFE;
  }
}

export function getVoiceFlowEnabledPreference(): boolean {
  try {
    return normalizeVoiceFlowEnabled(localStorage.getItem(VOICE_FLOW_ENABLED_STORAGE_KEY));
  } catch {
    return VOICE_FLOW_ENABLED_DEFAULT;
  }
}

export function getVoiceFlowHandsFreeConfirmationPreference(): VoiceFlowHandsFreeConfirmation {
  try {
    return normalizeVoiceFlowHandsFreeConfirmation(localStorage.getItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY));
  } catch {
    return VOICE_FLOW_CONFIRM_DELETES;
  }
}

export function setVoiceFlowEnabledPreference(enabled: boolean, opts?: { skipRemote?: boolean }): void {
  const next = normalizeVoiceFlowEnabled(enabled);
  const serialized = String(next);
  try {
    localStorage.setItem(VOICE_FLOW_ENABLED_STORAGE_KEY, serialized);
  } catch {
  }
  if (opts?.skipRemote || !getUser()) return;
  void apiFetch('/api/user/preferences', {
    method: 'PUT',
    body: JSON.stringify({ key: VOICE_FLOW_ENABLED_PREFERENCE_KEY, value: serialized }),
  }).catch(() => {});
}

export function setVoiceFlowModePreference(mode: VoiceFlowMode, opts?: { skipRemote?: boolean }): void {
  const next = normalizeVoiceFlowMode(mode);
  try {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, next);
  } catch {
  }
  if (opts?.skipRemote || !getUser()) return;
  void apiFetch('/api/user/preferences', {
    method: 'PUT',
    body: JSON.stringify({ key: VOICE_FLOW_MODE_PREFERENCE_KEY, value: next }),
  }).catch(() => {});
}

export function setVoiceFlowHandsFreeConfirmationPreference(
  value: VoiceFlowHandsFreeConfirmation,
  opts?: { skipRemote?: boolean },
): void {
  const next = normalizeVoiceFlowHandsFreeConfirmation(value);
  try {
    localStorage.setItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY, next);
  } catch {
  }
  if (opts?.skipRemote || !getUser()) return;
  void apiFetch('/api/user/preferences', {
    method: 'PUT',
    body: JSON.stringify({ key: VOICE_FLOW_HANDS_FREE_CONFIRMATION_PREFERENCE_KEY, value: next }),
  }).catch(() => {});
}

export function hydrateVoiceFlowModeFromServer(value: unknown): void {
  setVoiceFlowModePreference(normalizeVoiceFlowMode(value), { skipRemote: true });
}

export function hydrateVoiceFlowEnabledFromServer(value: unknown): void {
  setVoiceFlowEnabledPreference(normalizeVoiceFlowEnabled(value), { skipRemote: true });
}

export function hydrateVoiceFlowHandsFreeConfirmationFromServer(value: unknown): void {
  setVoiceFlowHandsFreeConfirmationPreference(normalizeVoiceFlowHandsFreeConfirmation(value), { skipRemote: true });
}
