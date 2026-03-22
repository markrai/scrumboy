export function normalizeSprints(resp) {
    if (!resp || !Array.isArray(resp.sprints))
        return [];
    return resp.sprints;
}
