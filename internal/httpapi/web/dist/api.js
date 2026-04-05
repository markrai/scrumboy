async function apiFetch(path, options = {}) {
    const res = await fetch(path, {
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
    const res = await fetch(path, {
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
export { apiFetch, apiFetchForm };
