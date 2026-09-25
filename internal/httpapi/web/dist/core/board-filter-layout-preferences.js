import { apiFetch } from '../api.js';
import { emit } from '../events.js';
import { getUser } from '../state/selectors.js';
export const BOARD_FILTER_LAYOUT_DEFAULT = 'omni';
export const BOARD_FILTER_LAYOUT_STORAGE_KEY = 'scrumboy.boardFilterLayout';
export const BOARD_FILTER_LAYOUT_PREFERENCE_KEY = 'boardFilterLayout';
export const BOARD_FILTER_LAYOUT_CHANGED_EVENT = 'board-filter-layout-changed';
export function normalizeBoardFilterLayout(value) {
    return value === 'legacy' ? 'legacy' : BOARD_FILTER_LAYOUT_DEFAULT;
}
export function getBoardFilterLayoutPreference() {
    try {
        return normalizeBoardFilterLayout(localStorage.getItem(BOARD_FILTER_LAYOUT_STORAGE_KEY));
    }
    catch {
        return BOARD_FILTER_LAYOUT_DEFAULT;
    }
}
export function setBoardFilterLayoutPreference(layout, opts) {
    const next = normalizeBoardFilterLayout(layout);
    const previous = getBoardFilterLayoutPreference();
    try {
        localStorage.setItem(BOARD_FILTER_LAYOUT_STORAGE_KEY, next);
    }
    catch {
    }
    if (previous !== next)
        emit(BOARD_FILTER_LAYOUT_CHANGED_EVENT, next);
    if (opts?.skipRemote || !getUser())
        return;
    void apiFetch('/api/user/preferences', {
        method: 'PUT',
        body: JSON.stringify({ key: BOARD_FILTER_LAYOUT_PREFERENCE_KEY, value: next }),
    }).catch(() => { });
}
export function hydrateBoardFilterLayoutFromServer(value) {
    setBoardFilterLayoutPreference(normalizeBoardFilterLayout(value), { skipRemote: true });
}
export async function loadBoardFilterLayoutPreferenceFromServer(fetchPreference) {
    hydrateBoardFilterLayoutFromServer(BOARD_FILTER_LAYOUT_DEFAULT);
    try {
        const response = await fetchPreference();
        hydrateBoardFilterLayoutFromServer(response?.value);
    }
    catch {
        // Keep Omni when signed-in hydration fails.
    }
}
