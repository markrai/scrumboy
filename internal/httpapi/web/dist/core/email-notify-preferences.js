// Email notification preferences — JSON preference blob, mirrors wallpaper.ts's pattern.
import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';
export const EMAIL_NOTIFY_PREF_KEY = 'emailNotifications';
const EMAIL_NOTIFY_STORAGE_KEY = 'scrumboy_emailNotifications';
const EMAIL_NOTIFY_PREF_VERSION = 1;
function defaultEmailNotifyPref() {
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
export function parseEmailNotifyPref(raw) {
    const s = (raw || '').trim();
    if (!s)
        return defaultEmailNotifyPref();
    try {
        const o = JSON.parse(s);
        if (!o || typeof o !== 'object')
            return defaultEmailNotifyPref();
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
    }
    catch {
        return defaultEmailNotifyPref();
    }
}
function serializeEmailNotifyPref(p) {
    return JSON.stringify(p);
}
let cachedJSON = null;
export function getStoredEmailNotifyPref() {
    if (cachedJSON !== null)
        return parseEmailNotifyPref(cachedJSON);
    try {
        return parseEmailNotifyPref(localStorage.getItem(EMAIL_NOTIFY_STORAGE_KEY));
    }
    catch {
        return defaultEmailNotifyPref();
    }
}
function setStoredEmailNotifyPref(p) {
    const json = serializeEmailNotifyPref(p);
    cachedJSON = json;
    try {
        localStorage.setItem(EMAIL_NOTIFY_STORAGE_KEY, json);
    }
    catch {
        // ignore
    }
}
async function savePrefToBackend(json) {
    if (!getUser())
        return;
    try {
        await apiFetch('/api/user/preferences', {
            method: 'PUT',
            body: JSON.stringify({ key: EMAIL_NOTIFY_PREF_KEY, value: json }),
        });
    }
    catch {
        // ignore
    }
}
export async function setEmailNotifyPref(p) {
    setStoredEmailNotifyPref(p);
    await savePrefToBackend(serializeEmailNotifyPref(p));
}
export function hydrateEmailNotifyFromServer(value) {
    const p = parseEmailNotifyPref(typeof value === 'string' ? value : null);
    setStoredEmailNotifyPref(p);
}
export async function loadUserEmailNotifyPref() {
    if (!getUser())
        return;
    try {
        const resp = await apiFetch(`/api/user/preferences?key=${encodeURIComponent(EMAIL_NOTIFY_PREF_KEY)}`);
        if (resp)
            hydrateEmailNotifyFromServer(resp.value);
    }
    catch {
        // ignore
    }
}
