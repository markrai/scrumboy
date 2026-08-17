import { apiFetch } from '../api.js';
import { getUser } from '../state/selectors.js';
export const BOARD_TODO_SORT_DEFAULT = 'default';
export const BOARD_TODO_SORT_STORAGE_KEY = 'scrumboy.boardTodoSort';
export const BOARD_TODO_SORT_PREFERENCE_KEY = 'boardTodoSort';
export function isBoardTodoSortUrlParam(value) {
    return value === 'newest' || value === 'oldest';
}
export function normalizeBoardTodoSort(value) {
    return isBoardTodoSortUrlParam(value) ? value : BOARD_TODO_SORT_DEFAULT;
}
export function boardTodoSortUrlParam(preference) {
    return isBoardTodoSortUrlParam(preference) ? preference : null;
}
export function getBoardTodoSortPreference() {
    try {
        return normalizeBoardTodoSort(localStorage.getItem(BOARD_TODO_SORT_STORAGE_KEY));
    }
    catch {
        return BOARD_TODO_SORT_DEFAULT;
    }
}
export function setBoardTodoSortPreference(value, opts) {
    const next = normalizeBoardTodoSort(value);
    if (!opts?.skipRemote && !getUser())
        return;
    try {
        localStorage.setItem(BOARD_TODO_SORT_STORAGE_KEY, next);
    }
    catch {
    }
    if (opts?.skipRemote || !getUser())
        return;
    void apiFetch('/api/user/preferences', {
        method: 'PUT',
        body: JSON.stringify({ key: BOARD_TODO_SORT_PREFERENCE_KEY, value: next }),
    }).catch(() => { });
}
export function hydrateBoardTodoSortFromServer(value) {
    setBoardTodoSortPreference(normalizeBoardTodoSort(value), { skipRemote: true });
}
export async function loadBoardTodoSortPreferenceFromServer(fetchPreference) {
    hydrateBoardTodoSortFromServer(BOARD_TODO_SORT_DEFAULT);
    try {
        const response = await fetchPreference();
        hydrateBoardTodoSortFromServer(response?.value ?? BOARD_TODO_SORT_DEFAULT);
    }
    catch {
        // Keep default when signed-in hydration fails.
    }
}
