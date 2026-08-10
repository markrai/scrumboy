const ALLOWED_ESTIMATION_POINTS = new Set([1, 2, 3, 5, 8, 13, 20, 40]);
export function shouldSubmitSprintAssignment(sprintControlsEnabled, currentSprintId, nextSprintId) {
    return sprintControlsEnabled && (currentSprintId ?? null) !== nextSprintId;
}
function appendOptionalFields(payload, input) {
    const estimationRaw = input.estimationRaw ?? "";
    const estimationParsed = estimationRaw === "" ? null : Number.parseInt(estimationRaw, 10);
    if (estimationParsed != null &&
        ALLOWED_ESTIMATION_POINTS.has(estimationParsed)) {
        payload.estimationPoints = estimationParsed;
    }
    if (input.sprintEnabled) {
        payload.sprintId = input.sprintId ?? null;
    }
    if (input.assigneeEnabled) {
        payload.assigneeUserId = input.assigneeUserId ?? null;
    }
    return payload;
}
export function buildTodoCreatePayload(input) {
    return appendOptionalFields({
        title: input.title,
        body: input.body,
        tags: input.tags,
        columnKey: input.columnKey,
    }, input);
}
export function buildTodoPatchPayload(input) {
    return appendOptionalFields({
        title: input.title,
        body: input.body,
        tags: input.tags,
    }, input);
}
