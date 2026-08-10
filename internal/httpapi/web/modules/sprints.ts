import { Board } from './types.js';

export type SprintListResponse<T> = { sprints?: T[] } | null;

export function normalizeSprints<T>(resp: SprintListResponse<T>): T[] {
  if (!resp || !Array.isArray(resp.sprints)) return [];
  return resp.sprints;
}

/** Sprints default to enabled; only an explicit `false` on the board's project turns them off. */
export function boardSprintsEnabled(board: Board | null | undefined): boolean {
  return board?.project?.sprintsEnabled !== false;
}
