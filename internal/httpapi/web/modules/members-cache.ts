import { apiFetch } from './api.js';
import type { BoardMember } from './state/state.js';

const membersPromiseByProject = new Map<number, Promise<BoardMember[]>>();

/**
 * Fetch project members with deduplication. Returns a cached promise per project
 * so multiple callers share the same request.
 */
export function fetchProjectMembers(projectId: number): Promise<BoardMember[]> {
  if (!membersPromiseByProject.has(projectId)) {
    membersPromiseByProject.set(
      projectId,
      apiFetch<BoardMember[]>(`/api/projects/${projectId}/members`).then((m) =>
        Array.isArray(m) ? m : []
      )
    );
  }
  return membersPromiseByProject.get(projectId)!;
}

/**
 * Invalidate the members cache for a project. Call after add/remove member mutations
 * so the next fetch returns fresh data.
 */
export function invalidateMembersCache(projectId: number): void {
  membersPromiseByProject.delete(projectId);
}
