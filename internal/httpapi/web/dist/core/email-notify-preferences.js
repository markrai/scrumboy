import { apiFetch } from '../api.js';
import { setEmailNotifyPreferenceState } from '../state/mutations.js';
import { getEmailNotifyPreferenceState, getUser } from '../state/selectors.js';
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
export function defaultEmailNotifyPref() {
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
    const value = (raw || '').trim();
    if (!value)
        return defaultEmailNotifyPref();
    let parsed;
    try {
        parsed = JSON.parse(value);
    }
    catch {
        throw new Error('invalid email notification preference');
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('invalid email notification preference');
    }
    const fields = parsed;
    for (const key of Object.keys(fields)) {
        if (!EMAIL_NOTIFY_FIELDS.has(key)) {
            throw new Error('invalid email notification preference');
        }
    }
    if ('v' in fields && fields.v !== EMAIL_NOTIFY_PREF_VERSION) {
        throw new Error('invalid email notification preference');
    }
    const pref = defaultEmailNotifyPref();
    for (const key of ['enabled', 'assigned', 'cardActivity', 'sprintActivity', 'projectActivity', 'addedToProject']) {
        if (!(key in fields))
            continue;
        if (typeof fields[key] !== 'boolean') {
            throw new Error('invalid email notification preference');
        }
        pref[key] = fields[key];
    }
    return pref;
}
function canonicalEmailNotifyPref(pref) {
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
function serializeEmailNotifyPref(pref) {
    return JSON.stringify(canonicalEmailNotifyPref(pref));
}
function getAnonymousEmailNotifyPref() {
    try {
        return parseEmailNotifyPref(localStorage.getItem(EMAIL_NOTIFY_STORAGE_KEY));
    }
    catch {
        return defaultEmailNotifyPref();
    }
}
function setAnonymousEmailNotifyPref(pref) {
    try {
        localStorage.setItem(EMAIL_NOTIFY_STORAGE_KEY, serializeEmailNotifyPref(pref));
    }
    catch {
    }
}
export function getEmailNotifyViewState() {
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
export function getStoredEmailNotifyPref() {
    return getEmailNotifyViewState().value;
}
export function hydrateEmailNotifyFromServer(value, userId) {
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
export async function loadUserEmailNotifyPref() {
    const user = getUser();
    if (!user)
        return false;
    const userId = user.id;
    setEmailNotifyPreferenceState({ userId, status: 'loading', value: null });
    try {
        const response = await apiFetch(`/api/user/preferences?key=${encodeURIComponent(EMAIL_NOTIFY_PREF_KEY)}`);
        if (getUser()?.id !== userId)
            return false;
        hydrateEmailNotifyFromServer(response?.value, userId);
        return true;
    }
    catch {
        if (getUser()?.id === userId) {
            setEmailNotifyPreferenceState({ userId, status: 'error', value: null });
        }
        return false;
    }
}
export async function setEmailNotifyPref(pref) {
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
    }
    catch (error) {
        if (getUser()?.id === userId) {
            setEmailNotifyPreferenceState({ userId, status: 'ready', value: previous });
        }
        throw error;
    }
    if (getUser()?.id === userId) {
        setEmailNotifyPreferenceState({ userId, status: 'ready', value: next });
    }
}
