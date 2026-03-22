import { apiFetch } from './api.js';
const membersPromiseByProject = new Map();
/**
 * Fetch project members with deduplication. Returns a cached promise per project
 * so multiple callers share the same request.
 */
export function fetchProjectMembers(projectId) {
    if (!membersPromiseByProject.has(projectId)) {
        membersPromiseByProject.set(projectId, apiFetch(`/api/projects/${projectId}/members`).then((m) => Array.isArray(m) ? m : []));
    }
    return membersPromiseByProject.get(projectId);
}
/**
 * Invalidate the members cache for a project. Call after add/remove member mutations
 * so the next fetch returns fresh data.
 */
export function invalidateMembersCache(projectId) {
    membersPromiseByProject.delete(projectId);
}
