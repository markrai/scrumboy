import type { Board } from '../types.js';
import { renderVoiceMessage, type VoiceMessageDescriptor, type VoiceMessageValues } from './i18n.js';

export type CommandIntent = "todos.create" | "todos.move" | "todos.delete" | "todos.assign" | "todos.update_title" | "open_todo";

export type TodoTargetReference =
  | { kind: "id"; localId: number; ambiguousId?: boolean; display: string }
  | { kind: "title"; phrase: string; display: string };

export type TodoTargetCandidate = {
  localId: number;
  title: string;
};

export type CommandIR =
  | {
      intent: "todos.create";
      projectId: number;
      projectSlug: string;
      entities: { title: string; columnKey: string };
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
      intent: "todos.update_title";
      projectId: number;
      projectSlug: string;
      entities: { localId: number; title: string };
    }
  | {
      intent: "open_todo";
      projectId: number;
      projectSlug: string;
      entities: { localId: number };
    };

export type ParsedCommandDraft =
  | { intent: "todos.create"; title: string; display: string }
  | { intent: "todos.move"; target: TodoTargetReference; rawStatus: string; display: string }
  | { intent: "todos.delete"; target: TodoTargetReference; display: string }
  | { intent: "todos.assign"; target: TodoTargetReference; rawUser: string; display: string }
  | { intent: "open_todo"; target: TodoTargetReference; display: string };

export type CommandFailureCode =
  | "unsupported"
  | "invalid_id"
  | "invalid_title"
  | "project_scope"
  | "unknown_status"
  | "ambiguous_status"
  | "unknown_story"
  | "ambiguous_story"
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
  candidates?: TodoTargetCandidate[];
  draft?: ParsedCommandDraft;
  transcript?: string;
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

const COMMAND_FAILURE_MESSAGE = Symbol("voiceCommandFailureMessage");

type LocalizedCommandFailure = CommandFailure & {
  [COMMAND_FAILURE_MESSAGE]?: VoiceMessageDescriptor;
};

function attachFailureMessage(failure: CommandFailure, descriptor: VoiceMessageDescriptor): CommandFailure {
  Object.defineProperty(failure, COMMAND_FAILURE_MESSAGE, {
    configurable: true,
    enumerable: false,
    value: descriptor,
  });
  return failure;
}

function fail(
  code: CommandFailureCode,
  message: string,
  extra: Partial<CommandFailure> = {},
  descriptor?: VoiceMessageDescriptor,
): CommandFailure {
  const failure = { ok: false, code, message, ...extra } as CommandFailure;
  return descriptor ? attachFailureMessage(failure, descriptor) : failure;
}

function localizedFail(
  code: CommandFailureCode,
  key: string,
  fallback: string,
  values: VoiceMessageValues = {},
  extra: Partial<CommandFailure> = {},
): CommandFailure {
  const descriptor = { key, fallback, values };
  return fail(code, renderVoiceMessage(descriptor), extra, descriptor);
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

export function normalizeTodoTitle(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const title = value.trim();
  return title.length > 0 && title.length <= 200 ? title : null;
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
    return localizedFail("invalid_schema", "voice.errors.schema.shapeInvalid", "Command shape is invalid.");
  }

  const ir = value as CommandIR;
  if (ir.projectId !== context.projectId || ir.projectSlug !== context.projectSlug) {
    return localizedFail("stale_context", "voice.errors.staleContext", "The board changed before the command could run.");
  }

  const activeLaneKeys = laneKeys(context.board);

  switch (ir.intent) {
    case "todos.create": {
      if (!hasExactKeys(ir.entities, ["title", "columnKey"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.createFieldsInvalid", "Create command fields are invalid.");
      }
      const title = normalizeTodoTitle(ir.entities.title);
      if (title === null) {
        return localizedFail("invalid_title", "voice.errors.schema.titleLength", "Todo title must be between 1 and 200 characters.");
      }
      if (typeof ir.entities.columnKey !== "string" || !activeLaneKeys.has(ir.entities.columnKey)) {
        return localizedFail("unknown_status", "voice.errors.statusNotFound", "Status was not found on this board.");
      }
      return { ok: true, value: { ...ir, entities: { title, columnKey: ir.entities.columnKey } } };
    }
    case "todos.move": {
      if (!hasExactKeys(ir.entities, ["localId", "toColumnKey"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.moveFieldsInvalid", "Move command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return localizedFail("invalid_schema", "voice.errors.schema.todoIdPositive", "Todo ID must be a positive integer.");
      }
      if (typeof ir.entities.toColumnKey !== "string" || !activeLaneKeys.has(ir.entities.toColumnKey)) {
        return localizedFail("unknown_status", "voice.errors.statusNotFound", "Status was not found on this board.");
      }
      return { ok: true, value: ir };
    }
    case "todos.delete": {
      if (!hasExactKeys(ir.entities, ["localId"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.deleteFieldsInvalid", "Delete command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return localizedFail("invalid_schema", "voice.errors.schema.todoIdPositive", "Todo ID must be a positive integer.");
      }
      return { ok: true, value: ir };
    }
    case "todos.assign": {
      if (!hasExactKeys(ir.entities, ["localId", "assigneeUserId"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.assignFieldsInvalid", "Assign command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId) || !isPositiveInteger(ir.entities.assigneeUserId)) {
        return localizedFail("invalid_schema", "voice.errors.schema.assignmentIdsPositive", "Assignment command IDs must be positive integers.");
      }
      return { ok: true, value: ir };
    }
    case "todos.update_title": {
      if (!hasExactKeys(ir.entities, ["localId", "title"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.updateTitleFieldsInvalid", "Title update command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return localizedFail("invalid_schema", "voice.errors.schema.todoIdPositive", "Todo ID must be a positive integer.");
      }
      const title = normalizeTodoTitle(ir.entities.title);
      if (title === null) {
        return localizedFail("invalid_title", "voice.errors.schema.titleLength", "Todo title must be between 1 and 200 characters.");
      }
      return { ok: true, value: { ...ir, entities: { localId: ir.entities.localId, title } } };
    }
    case "open_todo": {
      if (!hasExactKeys(ir.entities, ["localId"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.openFieldsInvalid", "Open command fields are invalid.");
      }
      if (!isPositiveInteger(ir.entities.localId)) {
        return localizedFail("invalid_schema", "voice.errors.schema.todoIdPositive", "Todo ID must be a positive integer.");
      }
      return { ok: true, value: ir };
    }
    default:
      return localizedFail("invalid_schema", "voice.errors.schema.intentUnsupported", "Command intent is unsupported.");
  }
}

export function commandFailure(code: CommandFailureCode, message: string, extra: Partial<CommandFailure> = {}): CommandFailure {
  return fail(code, message, extra);
}

export function localizedCommandFailure(
  code: CommandFailureCode,
  key: string,
  fallback: string,
  values: VoiceMessageValues = {},
  extra: Partial<CommandFailure> = {},
): CommandFailure {
  return localizedFail(code, key, fallback, values, extra);
}

export function localizeCommandFailure(failure: CommandFailure): string {
  const descriptor = (failure as LocalizedCommandFailure)[COMMAND_FAILURE_MESSAGE];
  return descriptor ? renderVoiceMessage(descriptor) : failure.message;
}

export function cloneCommandFailure(failure: CommandFailure, extra: Partial<CommandFailure> = {}): CommandFailure {
  const descriptor = (failure as LocalizedCommandFailure)[COMMAND_FAILURE_MESSAGE];
  const cloned = {
    ...failure,
    ...extra,
    message: descriptor ? renderVoiceMessage(descriptor) : failure.message,
  } as CommandFailure;
  return descriptor ? attachFailureMessage(cloned, descriptor) : cloned;
}

export function isCommandFailure<T>(result: CommandResult<T>): result is CommandFailure {
  return result.ok === false;
}
