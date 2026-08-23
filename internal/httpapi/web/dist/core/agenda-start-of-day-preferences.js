import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';
export const AGENDA_START_OF_DAY_DEFAULT = '08:00';
export const AGENDA_START_OF_DAY_STORAGE_KEY = 'scrumboy.agendaStartOfDay';
export const AGENDA_START_OF_DAY_OWNER_KEY = 'scrumboy.agendaStartOfDay.userId';
export const AGENDA_START_OF_DAY_PREFERENCE_KEY = 'agendaStartOfDay';
export const AGENDA_START_OF_DAY_MAX_MINUTE = 1439;
let saveEpoch = 0;
let ownerGeneration = 0;
let inFlightSaves = 0;
let lastSuccessEpoch = 0;
let lastConfirmed = AGENDA_START_OF_DAY_DEFAULT;
const TIME_PATTERN = /^([01]?\d|2[0-3]):([0-5]\d)(?::[0-5]\d)?$/;
export function normalizeAgendaStartOfDay(value) {
    const raw = typeof value === 'string' ? value.trim() : '';
    const match = TIME_PATTERN.exec(raw);
    if (!match)
        return AGENDA_START_OF_DAY_DEFAULT;
    return `${match[1].padStart(2, '0')}:${match[2]}`;
}
export function agendaStartOfDayMinutes(value) {
    const hhmm = normalizeAgendaStartOfDay(value ?? getAgendaStartOfDayPreference());
    const [hours, minutes] = hhmm.split(':').map((part) => Number.parseInt(part, 10));
    const total = hours * 60 + minutes;
    return Math.min(AGENDA_START_OF_DAY_MAX_MINUTE, Math.max(0, total));
}
function readStoredOwnerId() {
    try {
        const raw = localStorage.getItem(AGENDA_START_OF_DAY_OWNER_KEY);
        if (!raw)
            return null;
        const id = Number.parseInt(raw, 10);
        return Number.isFinite(id) ? id : null;
    }
    catch {
        return null;
    }
}
function writeLocalPreference(hhmm, ownerUserId) {
    const serialized = normalizeAgendaStartOfDay(hhmm);
    try {
        localStorage.setItem(AGENDA_START_OF_DAY_STORAGE_KEY, serialized);
        if (ownerUserId == null) {
            localStorage.removeItem(AGENDA_START_OF_DAY_OWNER_KEY);
        }
        else {
            localStorage.setItem(AGENDA_START_OF_DAY_OWNER_KEY, String(ownerUserId));
        }
    }
    catch {
    }
}
export function getAgendaStartOfDayPreference() {
    try {
        const user = getUser();
        const ownerId = readStoredOwnerId();
        if (user && ownerId !== user.id) {
            return AGENDA_START_OF_DAY_DEFAULT;
        }
        return normalizeAgendaStartOfDay(localStorage.getItem(AGENDA_START_OF_DAY_STORAGE_KEY));
    }
    catch {
        return AGENDA_START_OF_DAY_DEFAULT;
    }
}
/** Local-only write. Remote persistence goes through saveAgendaStartOfDayPreference. */
export function setAgendaStartOfDayPreference(hhmm) {
    const next = normalizeAgendaStartOfDay(hhmm);
    writeLocalPreference(next, getUser()?.id ?? null);
    if (inFlightSaves === 0) {
        lastConfirmed = next;
    }
}
export async function saveAgendaStartOfDayPreference(hhmm) {
    const next = normalizeAgendaStartOfDay(hhmm);
    const user = getUser();
    const userId = user?.id ?? null;
    if (inFlightSaves === 0) {
        lastConfirmed = getAgendaStartOfDayPreference();
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
            body: JSON.stringify({ key: AGENDA_START_OF_DAY_PREFERENCE_KEY, value: next }),
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
export function hydrateAgendaStartOfDayFromServer(value) {
    const next = normalizeAgendaStartOfDay(value);
    writeLocalPreference(next, getUser()?.id ?? null);
    if (inFlightSaves === 0) {
        lastConfirmed = next;
    }
}
export async function loadAgendaStartOfDayPreferenceFromServer(fetchPreference) {
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
        hydrateAgendaStartOfDayFromServer(response?.value ?? AGENDA_START_OF_DAY_DEFAULT);
    }
    catch {
        // Keep this user's existing local preference on transient GET failure.
    }
}
/**
 * Reconcile the local cache with the authenticated identity.
 * Uses the persisted owner ID so a cold reload (runtime user starts empty)
 * does not wipe the same user's cached preference.
 */
export function onAgendaStartOfDayAuthUserChanged(userId) {
    const storedOwner = readStoredOwnerId();
    if (userId != null && storedOwner === userId) {
        lastConfirmed = getAgendaStartOfDayPreference();
        return;
    }
    ownerGeneration += 1;
    saveEpoch += 1;
    inFlightSaves = 0;
    lastSuccessEpoch = 0;
    lastConfirmed = AGENDA_START_OF_DAY_DEFAULT;
    writeLocalPreference(AGENDA_START_OF_DAY_DEFAULT, userId);
}
