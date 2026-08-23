import { apiFetch } from '../api.js';
import { getBoard, getUser } from '../state/selectors.js';
import { visibleBoardLaneCount } from '../views/board-rendering.js';
export const WRAP_LANES_DEFAULT = false;
export const WRAP_LANES_STORAGE_KEY = 'scrumboy.wrapLanes';
export const WRAP_LANES_PREFERENCE_KEY = 'wrapLanes';
export function normalizeWrapLanes(value) {
    return value === true || value === 'true' || value === '1' || value === 'on';
}
export function getWrapLanesPreference() {
    try {
        return normalizeWrapLanes(localStorage.getItem(WRAP_LANES_STORAGE_KEY));
    }
    catch {
        return WRAP_LANES_DEFAULT;
    }
}
export function setWrapLanesPreference(enabled, opts) {
    const next = normalizeWrapLanes(enabled);
    const serialized = String(next);
    try {
        localStorage.setItem(WRAP_LANES_STORAGE_KEY, serialized);
    }
    catch {
    }
    if (opts?.skipRemote || !getUser())
        return;
    void apiFetch('/api/user/preferences', {
        method: 'PUT',
        body: JSON.stringify({ key: WRAP_LANES_PREFERENCE_KEY, value: serialized }),
    }).catch(() => { });
}
export function hydrateWrapLanesFromServer(value) {
    setWrapLanesPreference(normalizeWrapLanes(value), { skipRemote: true });
}
export async function loadWrapLanesPreferenceFromServer(fetchPreference) {
    hydrateWrapLanesFromServer(false);
    try {
        const response = await fetchPreference();
        hydrateWrapLanesFromServer(response?.value ?? false);
    }
    catch {
        // Keep default false when signed-in hydration fails.
    }
}
export function shouldWrapBoardLanes(laneCount) {
    return getWrapLanesPreference() && laneCount > 5;
}
/** Columns per wrapped row: half the lanes (floor), so even counts form two equal rows. */
export function wrapLanesColumnsPerRow(laneCount) {
    return Math.floor(laneCount / 2);
}
export function applyWrapLanesClass(boardEl, laneCount) {
    const wrap = shouldWrapBoardLanes(laneCount);
    boardEl.classList.toggle('board--wrapped', wrap);
    if (!(boardEl instanceof HTMLElement))
        return;
    if (wrap) {
        boardEl.style.setProperty('--board-wrap-cols', String(wrapLanesColumnsPerRow(laneCount)));
    }
    else {
        boardEl.style.removeProperty('--board-wrap-cols');
    }
}
export function syncOpenBoardWrapLanesClass() {
    const board = getBoard();
    const laneCount = board ? visibleBoardLaneCount(board) : 0;
    const boardEl = document.querySelector('.board');
    if (boardEl)
        applyWrapLanesClass(boardEl, laneCount);
}
