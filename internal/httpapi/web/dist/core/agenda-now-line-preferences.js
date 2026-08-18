import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';
export const AGENDA_NOW_LINE_SUBTLE = 'subtle';
export const AGENDA_NOW_LINE_PROMINENT = 'prominent';
export const AGENDA_NOW_LINE_DEFAULT = AGENDA_NOW_LINE_SUBTLE;
export const AGENDA_NOW_LINE_STORAGE_KEY = 'scrumboy.agendaNowLine';
export const AGENDA_NOW_LINE_OWNER_KEY = 'scrumboy.agendaNowLine.userId';
export const AGENDA_NOW_LINE_PREFERENCE_KEY = 'agendaNowLine';
let saveEpoch = 0;
let ownerGeneration = 0;
let inFlightSaves = 0;
let lastSuccessEpoch = 0;
let lastConfirmed = AGENDA_NOW_LINE_DEFAULT;
export function normalizeAgendaNowLine(value) {
    if (value === true || value === 'true' || value === '1' || value === 'on') {
        return AGENDA_NOW_LINE_PROMINENT;
    }
    const raw = typeof value === 'string' ? value.trim().toLowerCase() : '';
    return raw === AGENDA_NOW_LINE_PROMINENT ? AGENDA_NOW_LINE_PROMINENT : AGENDA_NOW_LINE_DEFAULT;
}
export function isAgendaNowLineProminent(value) {
    return normalizeAgendaNowLine(value ?? getAgendaNowLinePreference()) === AGENDA_NOW_LINE_PROMINENT;
}
function readStoredOwnerId() {
    try {
        const raw = localStorage.getItem(AGENDA_NOW_LINE_OWNER_KEY);
        if (!raw)
            return null;
        const id = Number.parseInt(raw, 10);
        return Number.isFinite(id) ? id : null;
    }
    catch {
        return null;
    }
}
function writeLocalPreference(style, ownerUserId) {
    const serialized = normalizeAgendaNowLine(style);
    try {
        localStorage.setItem(AGENDA_NOW_LINE_STORAGE_KEY, serialized);
        if (ownerUserId == null) {
            localStorage.removeItem(AGENDA_NOW_LINE_OWNER_KEY);
        }
        else {
            localStorage.setItem(AGENDA_NOW_LINE_OWNER_KEY, String(ownerUserId));
        }
    }
    catch {
    }
}
export function getAgendaNowLinePreference() {
    try {
        const user = getUser();
        const ownerId = readStoredOwnerId();
        if (user && ownerId !== user.id) {
            return AGENDA_NOW_LINE_DEFAULT;
        }
        return normalizeAgendaNowLine(localStorage.getItem(AGENDA_NOW_LINE_STORAGE_KEY));
    }
    catch {
        return AGENDA_NOW_LINE_DEFAULT;
    }
}
/** Local-only write. Remote persistence goes through saveAgendaNowLinePreference. */
export function setAgendaNowLinePreference(style) {
    const next = normalizeAgendaNowLine(style);
    writeLocalPreference(next, getUser()?.id ?? null);
    if (inFlightSaves === 0) {
        lastConfirmed = next;
    }
}
export async function saveAgendaNowLinePreference(style) {
    const next = normalizeAgendaNowLine(style);
    const user = getUser();
    const userId = user?.id ?? null;
    if (inFlightSaves === 0) {
        lastConfirmed = getAgendaNowLinePreference();
    }
    const epoch = ++saveEpoch;
    const generation = ownerGeneration;
    writeLocalPreference(next, userId);
    if (!user) {
        lastConfirmed = next;
        return;
    }
    inFlightSaves += 1;
    try {
        await apiFetch('/api/user/preferences', {
            method: 'PUT',
            body: JSON.stringify({ key: AGENDA_NOW_LINE_PREFERENCE_KEY, value: next }),
        });
        if (generation !== ownerGeneration || getUser()?.id !== userId) {
            return;
        }
        if (epoch >= lastSuccessEpoch) {
            lastSuccessEpoch = epoch;
            lastConfirmed = next;
        }
    }
    finally {
        if (generation === ownerGeneration) {
            inFlightSaves -= 1;
            if (inFlightSaves === 0 && getUser()?.id === userId) {
                writeLocalPreference(lastConfirmed, userId);
            }
        }
    }
}
export function hydrateAgendaNowLineFromServer(value) {
    const next = normalizeAgendaNowLine(value);
    writeLocalPreference(next, getUser()?.id ?? null);
    if (inFlightSaves === 0) {
        lastConfirmed = next;
    }
}
export async function loadAgendaNowLinePreferenceFromServer(fetchPreference) {
    const user = getUser();
    if (!user)
        return;
    const userId = user.id;
    const epochAtStart = saveEpoch;
    try {
        const response = await fetchPreference();
        if (getUser()?.id !== userId)
            return;
        if (inFlightSaves > 0 || saveEpoch !== epochAtStart)
            return;
        hydrateAgendaNowLineFromServer(response?.value ?? AGENDA_NOW_LINE_DEFAULT);
    }
    catch {
        // Keep this user's existing local preference on transient GET failure.
    }
}
export function onAgendaNowLineAuthUserChanged(userId) {
    const storedOwner = readStoredOwnerId();
    if (userId != null && storedOwner === userId) {
        lastConfirmed = getAgendaNowLinePreference();
        return;
    }
    ownerGeneration += 1;
    saveEpoch += 1;
    inFlightSaves = 0;
    lastSuccessEpoch = 0;
    lastConfirmed = AGENDA_NOW_LINE_DEFAULT;
    writeLocalPreference(AGENDA_NOW_LINE_DEFAULT, userId);
}
