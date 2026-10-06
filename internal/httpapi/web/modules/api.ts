import { getAppRuntime } from './platform/runtime.js';
import type { ArchivePageResponse, TodoArchiveBatchResult } from './types.js';

async function apiFetch<T = unknown>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await getAppRuntime().transport().request(path, {
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

/** POST multipart (no JSON Content-Type; browser sets boundary). */
async function apiFetchForm<T = unknown>(path: string, form: FormData): Promise<T> {
  const res = await getAppRuntime().transport().request(path, {
    method: "POST",
    headers: { "X-Scrumboy": "1" },
    body: form,
  });
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

export function listArchivedTodos(
  slug: string,
  options: { limit?: number; afterCursor?: string | null } = {},
): Promise<ArchivePageResponse> {
  const params = new URLSearchParams();
  params.set('limit', String(options.limit ?? 50));
  if (options.afterCursor) params.set('afterCursor', options.afterCursor);
  return apiFetch<ArchivePageResponse>(`/api/board/${encodeURIComponent(slug)}/archive?${params.toString()}`);
}

function transitionTodos(
  slug: string,
  action: 'archive' | 'restore',
  localIds: number[],
): Promise<TodoArchiveBatchResult> {
  return apiFetch<TodoArchiveBatchResult>(`/api/board/${encodeURIComponent(slug)}/todos/${action}`, {
    method: 'POST',
    body: JSON.stringify({ localIds }),
  });
}

export function archiveTodos(slug: string, localIds: number[]): Promise<TodoArchiveBatchResult> {
  return transitionTodos(slug, 'archive', localIds);
}

export function restoreTodos(slug: string, localIds: number[]): Promise<TodoArchiveBatchResult> {
  return transitionTodos(slug, 'restore', localIds);
}

export { apiFetch, apiFetchForm };
