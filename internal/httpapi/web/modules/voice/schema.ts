import type { Board } from '../types.js';

export type CommandIntent = "todos.create" | "todos.move" | "todos.delete" | "todos.assign" | "open_todo";

export type CommandIR =
  | {
      intent: "todos.create";
      projectId: number;
      projectSlug: string;
      entities: { title: string };
    }
  | {
      intent: "todos.move";
      projectId: number;
      projectSlug: string;
      entities: { localId: number; toColumnKey: string };
    }
  | {
      intent: "todos.delete";
      projectId: number;
      projectSlug: string;
      entities: { localId: number };
    }
  | {
      intent: "todos.assign";
      projectId: number;
      projectSlug: string;
      entities: { localId: number; assigneeUserId: number };
    }
  | {
      intent: "open_todo";
      projectId: number;
      projectSlug: string;
      entities: { localId: number };
    };

export type ParsedCommandDraft =
  | { intent: "todos.create"; title: string; display: string }
  | { intent: "todos.move"; localId: number; rawStatus: string; ambiguousId?: boolean; display: string }
  | { intent: "todos.delete"; localId: number; ambiguousId?: boolean; display: string }
  | { intent: "todos.assign"; localId: number; rawUser: string; ambiguousId?: boolean; display: string }
  | { intent: "open_todo"; localId: number; ambiguousId?: boolean; display: string };

export type CommandFailureCode =
  | "unsupported"
  | "invalid_id"
  | "invalid_title"
  | "project_scope"
  | "unknown_status"
  | "ambiguous_status"
  | "unknown_story"
  | "unknown_user"
  | "ambiguous_user"
  | "invalid_schema"
  | "unauthorized"
  | "stale_context"
  | "speech_unavailable"
  | "speech_failed"
  | "network";

export type CommandFailure = {
  ok: false;
  code: CommandFailureCode;
  message: string;
};

export type CommandSuccess<T> = {
  ok: true;
  value: T;
};

export type CommandResult<T> = CommandSuccess<T> | CommandFailure;

export type ResolvedCommand = {
  ir: CommandIR;
  summary: string;
  confirmLabel: string;
  danger: boolean;
  requiresConfirmation: boolean;
  storyTitle?: string;
  statusName?: string;
  assigneeName?: string;
};

export type ValidationContext = {
  projectId: number;
  projectSlug: string;
  board: Board;
};

function fail(code: CommandFailureCode, message: string): CommandFailure {
  return { ok: false, code, message };
}

function objectKeys(value: unknown): string[] | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  return Object.keys(value as Record<string, unknown>);
}

function hasExactKeys(value: unknown, keys: string[]): boolean {
  const actual = objectKeys(value);
  if (!actual) return false;
  if (actual.length !== keys.length) return false;
  return keys.every((key) => actual.includes(key));
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value > 0;
}

function laneKeys(board: Board): Set<string> {
  const keys = new Set<string>();
  const order = board.columnOrder ?? [];
  for (const lane of order) keys.add(lane.key);
  for (const key of Object.keys(board.columns ?? {})) keys.add(key);
  return keys;
}

export function validateCommandIR(value: unknown, context: ValidationContext): CommandResult<CommandIR> {
  if (!hasExactKeys(value, ["intent", "projectId", "projectSlug", "entities"])) {
    return fail("invalid_schema", "Command shape is invalid.");
  }

  const ir = value as CommandIR;
  if (ir.projectId !== context.projectId || ir.projectSlug !== context.projectSlug) {
    return fail("stale_context", "The board changed before the command could run.");
  }

  const activeLaneKeys = laneKeys(context.board);

  switch (ir.intent) {
    case "todos.create": {
      if (!hasExactKeys(ir.entities, ["title"])) {
        return fail("invalid_schema", "Create command fields are invalid.");
      }
      const title = ir.entities.title;
      if (typeof title !== "string" || title.trim().length === 0 || title.trim().length > 200) {
        return fail("invalid_title", "Todo title must be between 1 and 200 characters.");
      }
      return { ok: true, value: { ...ir, entities: { title: title.trim() } } };
    }
    case "todos.move": {
      if (!hasExactKeys(ir.entities, ["localId", "toColumnKey"])) {
        return fail("invalid_schema", "Move command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return fail("invalid_schema", "Todo ID must be a positive integer.");
      }
      if (typeof ir.entities.toColumnKey !== "string" || !activeLaneKeys.has(ir.entities.toColumnKey)) {
        return fail("unknown_status", "Status was not found on this board.");
      }
      return { ok: true, value: ir };
    }
    case "todos.delete": {
      if (!hasExactKeys(ir.entities, ["localId"])) {
        return fail("invalid_schema", "Delete command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return fail("invalid_schema", "Todo ID must be a positive integer.");
      }
      return { ok: true, value: ir };
    }
    case "todos.assign": {
      if (!hasExactKeys(ir.entities, ["localId", "assigneeUserId"])) {
        return fail("invalid_schema", "Assign command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId) || !isPositiveInteger(ir.entities.assigneeUserId)) {
        return fail("invalid_schema", "Assignment command IDs must be positive integers.");
      }
      return { ok: true, value: ir };
    }
    case "open_todo": {
      if (!hasExactKeys(ir.entities, ["localId"])) {
        return fail("invalid_schema", "Open command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return fail("invalid_schema", "Todo ID must be a positive integer.");
      }
      return { ok: true, value: ir };
    }
    default:
      return fail("invalid_schema", "Command intent is unsupported.");
  }
}

export function commandFailure(code: CommandFailureCode, message: string): CommandFailure {
  return fail(code, message);
}

export function isCommandFailure<T>(result: CommandResult<T>): result is CommandFailure {
  return result.ok === false;
}
