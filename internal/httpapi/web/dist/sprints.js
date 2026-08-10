export function normalizeSprints(resp) {
    if (!resp || !Array.isArray(resp.sprints))
        return [];
    return resp.sprints;
}
/** Sprints default to enabled; only an explicit `false` on the board's project turns them off. */
export function boardSprintsEnabled(board) {
    return board?.project?.sprintsEnabled !== false;
}
