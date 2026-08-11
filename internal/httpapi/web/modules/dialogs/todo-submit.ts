const ALLOWED_ESTIMATION_POINTS = new Set([1, 2, 3, 5, 8, 13, 20, 40]);

export type TodoBasePayloadInput = {
  title: string;
  body: string;
  tags: string[];
  estimationRaw?: string | null;
  sprintEnabled?: boolean;
  sprintId?: number | null;
  assigneeEnabled?: boolean;
  assigneeUserId?: number | null;
  priorityKey?: string | null;
};

export type TodoCreatePayloadInput = TodoBasePayloadInput & {
  columnKey: string;
};

export function shouldSubmitSprintAssignment(
  sprintControlsEnabled: boolean,
  currentSprintId: number | null | undefined,
  nextSprintId: number | null,
): boolean {
  return sprintControlsEnabled && (currentSprintId ?? null) !== nextSprintId;
}

function appendOptionalFields(
  payload: Record<string, unknown>,
  input: TodoBasePayloadInput,
): Record<string, unknown> {
  const estimationRaw = input.estimationRaw ?? "";
  const estimationParsed =
    estimationRaw === "" ? null : Number.parseInt(estimationRaw, 10);
  if (
    estimationParsed != null &&
    ALLOWED_ESTIMATION_POINTS.has(estimationParsed)
  ) {
    payload.estimationPoints = estimationParsed;
  }
  if (input.sprintEnabled) {
    payload.sprintId = input.sprintId ?? null;
  }
  if (input.assigneeEnabled) {
    payload.assigneeUserId = input.assigneeUserId ?? null;
  }
  payload.priorityKey = input.priorityKey || null;
  return payload;
}

export function buildTodoCreatePayload(
  input: TodoCreatePayloadInput,
): Record<string, unknown> {
  return appendOptionalFields(
    {
      title: input.title,
      body: input.body,
      tags: input.tags,
      columnKey: input.columnKey,
    },
    input,
  );
}

export function buildTodoPatchPayload(
  input: TodoBasePayloadInput,
): Record<string, unknown> {
  return appendOptionalFields(
    {
      title: input.title,
      body: input.body,
      tags: input.tags,
    },
    input,
  );
}
