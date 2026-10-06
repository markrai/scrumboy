import type { Board } from '../types.js';
import { apiFetch } from '../api.js';

export type TodoTagSuggestion = { name: string; color?: string };

export function mergeTodoTagSuggestions(
  projectTags: readonly unknown[],
  attachedTags: readonly string[] = [],
  activeOnly = false,
): TodoTagSuggestion[] {
  const byName = new Map<string, TodoTagSuggestion>();
  for (const value of projectTags) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) continue;
    const tag = value as { name?: unknown; color?: unknown; count?: unknown };
    if (typeof tag.name !== 'string' || !tag.name.trim()) continue;
    if (activeOnly && !(typeof tag.count === 'number' && tag.count > 0)) continue;
    const entry: TodoTagSuggestion = { name: tag.name };
    if (typeof tag.color === 'string' && tag.color) entry.color = tag.color;
    byName.set(tag.name.toLocaleLowerCase(), entry);
  }
  for (const name of attachedTags) {
    if (!name?.trim()) continue;
    const key = name.toLocaleLowerCase();
    if (!byName.has(key)) byName.set(key, { name });
  }
  return [...byName.values()];
}

export function defaultTodoTagSuggestions(board: Board | null, attachedTags: readonly string[] = []): TodoTagSuggestion[] {
  return mergeTodoTagSuggestions(board?.tags ?? [], attachedTags, true);
}

export async function loadAllProjectTagSuggestions(
  slug: string,
  attachedTags: readonly string[] = [],
  fetcher: <T = unknown>(path: string) => Promise<T> = apiFetch,
): Promise<TodoTagSuggestion[]> {
  const response = await fetcher<unknown>(`/api/board/${encodeURIComponent(slug)}/tags`);
  if (!Array.isArray(response)) throw new Error('invalid tag catalog');
  return mergeTodoTagSuggestions(response, attachedTags);
}
