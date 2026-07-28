import { apiFetch } from '../api.js';
import { getBoard, getUser } from '../state/selectors.js';
import { getBoardColumns } from '../views/board-rendering.js';
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
export function shouldWrapBoardLanes(laneCount) {
    return getWrapLanesPreference() && laneCount > 5;
}
export function applyWrapLanesClass(boardEl, laneCount) {
    boardEl.classList.toggle('board--wrapped', shouldWrapBoardLanes(laneCount));
}
export function syncOpenBoardWrapLanesClass() {
    const board = getBoard();
    const laneCount = board ? getBoardColumns(board).length : 0;
    const boardEl = document.querySelector('.board');
    if (boardEl)
        applyWrapLanesClass(boardEl, laneCount);
}
