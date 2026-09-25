import { getAppRuntime } from './platform/runtime.js';
async function apiFetch(path, options = {}) {
    const res = await getAppRuntime().transport().request(path, {
        headers: { "Content-Type": "application/json", "X-Scrumboy": "1", ...(options.headers || {}) },
        ...options,
    });
    if (res.status === 204)
        return null;
    const data = await res.json().catch(() => null);
    if (!res.ok) {
        const msg = data?.error?.message || `HTTP ${res.status}`;
        const err = new Error(msg);
        err.status = res.status;
        err.data = data;
        throw err;
    }
    return data;
}
/** POST multipart (no JSON Content-Type; browser sets boundary). */
async function apiFetchForm(path, form) {
    const res = await getAppRuntime().transport().request(path, {
        method: "POST",
        headers: { "X-Scrumboy": "1" },
        body: form,
    });
    const data = await res.json().catch(() => null);
    if (!res.ok) {
        const msg = data?.error?.message || `HTTP ${res.status}`;
        const err = new Error(msg);
        err.status = res.status;
        err.data = data;
        throw err;
    }
    return data;
}
export function listArchivedTodos(slug, options = {}) {
    const params = new URLSearchParams();
    params.set('limit', String(options.limit ?? 50));
    if (options.afterCursor)
        params.set('afterCursor', options.afterCursor);
    return apiFetch(`/api/board/${encodeURIComponent(slug)}/archive?${params.toString()}`);
}
function transitionTodos(slug, action, localIds) {
    return apiFetch(`/api/board/${encodeURIComponent(slug)}/todos/${action}`, {
        method: 'POST',
        body: JSON.stringify({ localIds }),
    });
}
export function archiveTodos(slug, localIds) {
    return transitionTodos(slug, 'archive', localIds);
}
export function restoreTodos(slug, localIds) {
    return transitionTodos(slug, 'restore', localIds);
}
export { apiFetch, apiFetchForm };
