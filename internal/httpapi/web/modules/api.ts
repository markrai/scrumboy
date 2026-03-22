async function apiFetch<T = unknown>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json", "X-Scrumboy": "1", ...(options.headers || {}) },
    ...options,
  });
  if (res.status === 204) return null as T;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const msg = data?.error?.message || `HTTP ${res.status}`;
    const err = new Error(msg);
    (err as Error & { status?: number; data?: unknown }).status = res.status;
    (err as Error & { status?: number; data?: unknown }).data = data;
    throw err;
  }
  return data as T;
}

export { apiFetch };
