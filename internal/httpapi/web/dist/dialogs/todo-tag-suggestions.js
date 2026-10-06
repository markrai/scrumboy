import { apiFetch } from '../api.js';
export function mergeTodoTagSuggestions(projectTags, attachedTags = [], activeOnly = false) {
    const byName = new Map();
    for (const value of projectTags) {
        if (!value || typeof value !== 'object' || Array.isArray(value))
            continue;
        const tag = value;
        if (typeof tag.name !== 'string' || !tag.name.trim())
            continue;
        if (activeOnly && !(typeof tag.count === 'number' && tag.count > 0))
            continue;
        const entry = { name: tag.name };
        if (typeof tag.color === 'string' && tag.color)
            entry.color = tag.color;
        byName.set(tag.name.toLocaleLowerCase(), entry);
    }
    for (const name of attachedTags) {
        if (!name?.trim())
            continue;
        const key = name.toLocaleLowerCase();
        if (!byName.has(key))
            byName.set(key, { name });
    }
    return [...byName.values()];
}
export function defaultTodoTagSuggestions(board, attachedTags = []) {
    return mergeTodoTagSuggestions(board?.tags ?? [], attachedTags, true);
}
export async function loadAllProjectTagSuggestions(slug, attachedTags = [], fetcher = apiFetch) {
    const response = await fetcher(`/api/board/${encodeURIComponent(slug)}/tags`);
    if (!Array.isArray(response))
        throw new Error('invalid tag catalog');
    return mergeTodoTagSuggestions(response, attachedTags);
}
