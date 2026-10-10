import { getAppRuntime } from './platform/runtime.js';
async function apiFetch(path, options = {}) {
    const isMultipart = typeof FormData !== 'undefined' && options.body instanceof FormData;
    const headers = { 'X-Scrumboy': '1' };
    if (!isMultipart)
        headers['Content-Type'] = 'application/json';
    new Headers(options.headers).forEach((value, name) => {
        if (name.toLowerCase() === 'content-type') {
            headers['Content-Type'] = value;
        }
        else if (name.toLowerCase() === 'x-scrumboy') {
            headers['X-Scrumboy'] = value;
        }
        else {
            headers[name] = value;
        }
    });
    const res = await getAppRuntime().transport().request(path, {
        ...options,
        headers,
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
