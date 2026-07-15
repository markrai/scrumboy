// Email notification preferences — JSON preference blob, mirrors wallpaper.ts's pattern.
import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';

export const EMAIL_NOTIFY_PREF_KEY = 'emailNotifications';
const EMAIL_NOTIFY_STORAGE_KEY = 'scrumboy_emailNotifications';
const EMAIL_NOTIFY_PREF_VERSION = 1;

export interface EmailNotifyPref {
  v: 1;
  enabled: boolean;
  assigned: boolean;
  cardActivity: boolean;
  sprintActivity: boolean;
  projectActivity: boolean;
  addedToProject: boolean;
}

export type EmailNotifyCategory = Exclude<keyof EmailNotifyPref, 'v' | 'enabled'>;

function defaultEmailNotifyPref(): EmailNotifyPref {
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
  const s = (raw || '').trim();
  if (!s) return defaultEmailNotifyPref();
  try {
    const o = JSON.parse(s) as Partial<EmailNotifyPref>;
    if (!o || typeof o !== 'object') return defaultEmailNotifyPref();
    const d = defaultEmailNotifyPref();
    return {
      v: EMAIL_NOTIFY_PREF_VERSION,
      enabled: !!o.enabled,
      assigned: o.assigned === undefined ? d.assigned : !!o.assigned,
      cardActivity: !!o.cardActivity,
      sprintActivity: !!o.sprintActivity,
      projectActivity: !!o.projectActivity,
      addedToProject: o.addedToProject === undefined ? d.addedToProject : !!o.addedToProject,
    };
  } catch {
    return defaultEmailNotifyPref();
  }
}

function serializeEmailNotifyPref(p: EmailNotifyPref): string {
  return JSON.stringify(p);
}

let cachedJSON: string | null = null;

export function getStoredEmailNotifyPref(): EmailNotifyPref {
  if (cachedJSON !== null) return parseEmailNotifyPref(cachedJSON);
  try {
    return parseEmailNotifyPref(localStorage.getItem(EMAIL_NOTIFY_STORAGE_KEY));
  } catch {
    return defaultEmailNotifyPref();
  }
}

function setStoredEmailNotifyPref(p: EmailNotifyPref): void {
  const json = serializeEmailNotifyPref(p);
  cachedJSON = json;
  try {
    localStorage.setItem(EMAIL_NOTIFY_STORAGE_KEY, json);
  } catch {
    // ignore
  }
}

async function savePrefToBackend(json: string): Promise<void> {
  if (!getUser()) return;
  try {
    await apiFetch('/api/user/preferences', {
      method: 'PUT',
      body: JSON.stringify({ key: EMAIL_NOTIFY_PREF_KEY, value: json }),
    });
  } catch {
    // ignore
  }
}

export async function setEmailNotifyPref(p: EmailNotifyPref): Promise<void> {
  setStoredEmailNotifyPref(p);
  await savePrefToBackend(serializeEmailNotifyPref(p));
}

export function hydrateEmailNotifyFromServer(value: unknown): void {
  const p = parseEmailNotifyPref(typeof value === 'string' ? value : null);
  setStoredEmailNotifyPref(p);
}

export async function loadUserEmailNotifyPref(): Promise<void> {
  if (!getUser()) return;
  try {
    const resp = await apiFetch<{ value: string }>(
      `/api/user/preferences?key=${encodeURIComponent(EMAIL_NOTIFY_PREF_KEY)}`
    );
    if (resp) hydrateEmailNotifyFromServer(resp.value);
  } catch {
    // ignore
  }
}
