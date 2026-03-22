export type SprintListResponse<T> = { sprints?: T[] } | null;

export function normalizeSprints<T>(resp: SprintListResponse<T>): T[] {
  if (!resp || !Array.isArray(resp.sprints)) return [];
  return resp.sprints;
}
