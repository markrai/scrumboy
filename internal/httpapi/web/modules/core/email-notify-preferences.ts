import { apiFetch } from '../api.js';
import { setEmailNotifyPreferenceState } from '../state/mutations.js';
import { getEmailNotifyPreferenceState, getUser } from '../state/selectors.js';
import type { EmailNotifyPref, EmailNotifyPreferenceState } from '../types.js';

export const EMAIL_NOTIFY_PREF_KEY = 'emailNotifications';
const EMAIL_NOTIFY_STORAGE_KEY = 'scrumboy_emailNotifications';
const EMAIL_NOTIFY_PREF_VERSION = 1;
const EMAIL_NOTIFY_FIELDS = new Set([
  'v',
  'enabled',
  'assigned',
  'cardActivity',
  'sprintActivity',
  'projectActivity',
  'addedToProject',
]);

export type { EmailNotifyPref } from '../types.js';
export type EmailNotifyCategory = Exclude<keyof EmailNotifyPref, 'v' | 'enabled'>;

export function defaultEmailNotifyPref(): EmailNotifyPref {
  return {
    v: EMAIL_NOTIFY_PREF_VERSION,
    enabled: false,
    assigned: true,
    cardActivity: false,
    sprintActivity: false,
    projectActivity: false,
    addedToProject: true,
  };
}

export function parseEmailNotifyPref(raw: string | null | undefined): EmailNotifyPref {
  const value = (raw || '').trim();
  if (!value) return defaultEmailNotifyPref();
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error('invalid email notification preference');
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('invalid email notification preference');
  }
  const fields = parsed as Record<string, unknown>;
  for (const key of Object.keys(fields)) {
    if (!EMAIL_NOTIFY_FIELDS.has(key)) {
      throw new Error('invalid email notification preference');
    }
  }
  if ('v' in fields && fields.v !== EMAIL_NOTIFY_PREF_VERSION) {
    throw new Error('invalid email notification preference');
  }
  const pref = defaultEmailNotifyPref();
  for (const key of ['enabled', 'assigned', 'cardActivity', 'sprintActivity', 'projectActivity', 'addedToProject'] as const) {
    if (!(key in fields)) continue;
    if (typeof fields[key] !== 'boolean') {
      throw new Error('invalid email notification preference');
    }
    pref[key] = fields[key];
  }
  return pref;
}

function canonicalEmailNotifyPref(pref: EmailNotifyPref): EmailNotifyPref {
  return {
    v: EMAIL_NOTIFY_PREF_VERSION,
    enabled: pref.enabled,
    assigned: pref.assigned,
    cardActivity: pref.cardActivity,
    sprintActivity: pref.sprintActivity,
    projectActivity: pref.projectActivity,
    addedToProject: pref.addedToProject,
  };
}

function serializeEmailNotifyPref(pref: EmailNotifyPref): string {
  return JSON.stringify(canonicalEmailNotifyPref(pref));
}

function getAnonymousEmailNotifyPref(): EmailNotifyPref {
  try {
    return parseEmailNotifyPref(localStorage.getItem(EMAIL_NOTIFY_STORAGE_KEY));
  } catch {
    return defaultEmailNotifyPref();
  }
}

function setAnonymousEmailNotifyPref(pref: EmailNotifyPref): void {
  try {
    localStorage.setItem(EMAIL_NOTIFY_STORAGE_KEY, serializeEmailNotifyPref(pref));
  } catch {
  }
}

export function getEmailNotifyViewState(): EmailNotifyPreferenceState {
  const user = getUser();
  if (!user) {
    return { userId: null, status: 'ready', value: getAnonymousEmailNotifyPref() };
  }
  const state = getEmailNotifyPreferenceState();
  if (state.userId !== user.id) {
    return { userId: user.id, status: 'idle', value: null };
  }
  return state;
}

export function getStoredEmailNotifyPref(): EmailNotifyPref | null {
  return getEmailNotifyViewState().value;
}

export function hydrateEmailNotifyFromServer(value: unknown, userId?: number): EmailNotifyPref {
  if (typeof value !== 'string') {
    throw new Error('invalid email notification preference');
  }
  const pref = parseEmailNotifyPref(value);
  const user = getUser();
  const ownerUserId = userId ?? user?.id;
  if (ownerUserId === undefined || user?.id !== ownerUserId) {
    throw new Error('email notification preference user changed');
  }
  setEmailNotifyPreferenceState({ userId: ownerUserId, status: 'ready', value: pref });
  return pref;
}

export async function loadUserEmailNotifyPref(): Promise<boolean> {
  const user = getUser();
  if (!user) return false;
  const userId = user.id;
  setEmailNotifyPreferenceState({ userId, status: 'loading', value: null });
  try {
    const response = await apiFetch<{ value: string }>(
      `/api/user/preferences?key=${encodeURIComponent(EMAIL_NOTIFY_PREF_KEY)}`
    );
    if (getUser()?.id !== userId) return false;
    hydrateEmailNotifyFromServer(response?.value, userId);
    return true;
  } catch {
    if (getUser()?.id === userId) {
      setEmailNotifyPreferenceState({ userId, status: 'error', value: null });
    }
    return false;
  }
}

export async function setEmailNotifyPref(pref: EmailNotifyPref): Promise<void> {
  const next = canonicalEmailNotifyPref(pref);
  const user = getUser();
  if (!user) {
    setAnonymousEmailNotifyPref(next);
    return;
  }
  const userId = user.id;
  const state = getEmailNotifyPreferenceState();
  if (state.userId !== userId || state.status !== 'ready' || !state.value) {
    throw new Error('email notification preference is not ready');
  }
  const previous = state.value;
  setEmailNotifyPreferenceState({ userId, status: 'saving', value: previous });
  try {
    await apiFetch('/api/user/preferences', {
      method: 'PUT',
      body: JSON.stringify({ key: EMAIL_NOTIFY_PREF_KEY, value: serializeEmailNotifyPref(next) }),
    });
  } catch (error) {
    if (getUser()?.id === userId) {
      setEmailNotifyPreferenceState({ userId, status: 'ready', value: previous });
    }
    throw error;
  }
  if (getUser()?.id === userId) {
    setEmailNotifyPreferenceState({ userId, status: 'ready', value: next });
  }
}
