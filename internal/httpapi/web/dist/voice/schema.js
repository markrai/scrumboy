import { renderVoiceMessage } from './i18n.js';
const COMMAND_FAILURE_MESSAGE = Symbol("voiceCommandFailureMessage");
function attachFailureMessage(failure, descriptor) {
    Object.defineProperty(failure, COMMAND_FAILURE_MESSAGE, {
        configurable: true,
        enumerable: false,
        value: descriptor,
    });
    return failure;
}
function fail(code, message, extra = {}, descriptor) {
    const failure = { ok: false, code, message, ...extra };
    return descriptor ? attachFailureMessage(failure, descriptor) : failure;
}
function localizedFail(code, key, fallback, values = {}, extra = {}) {
    const descriptor = { key, fallback, values };
    return fail(code, renderVoiceMessage(descriptor), extra, descriptor);
}
function objectKeys(value) {
    if (value === null || typeof value !== "object" || Array.isArray(value))
        return null;
    return Object.keys(value);
}
function hasExactKeys(value, keys) {
    const actual = objectKeys(value);
    if (!actual)
        return false;
    if (actual.length !== keys.length)
        return false;
    return keys.every((key) => actual.includes(key));
}
function isPositiveInteger(value) {
    return typeof value === "number" && Number.isInteger(value) && value > 0;
}
export function normalizeTodoTitle(value) {
    if (typeof value !== "string")
        return null;
    const title = value.trim();
    return title.length > 0 && title.length <= 200 ? title : null;
}
function laneKeys(board) {
    const keys = new Set();
    const order = board.columnOrder ?? [];
    for (const lane of order)
        keys.add(lane.key);
    for (const key of Object.keys(board.columns ?? {}))
        keys.add(key);
    return keys;
}
export function validateCommandIR(value, context) {
    if (!hasExactKeys(value, ["intent", "projectId", "projectSlug", "entities"])) {
        return localizedFail("invalid_schema", "voice.errors.schema.shapeInvalid", "Command shape is invalid.");
    }
    const ir = value;
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
export function commandFailure(code, message, extra = {}) {
    return fail(code, message, extra);
}
export function localizedCommandFailure(code, key, fallback, values = {}, extra = {}) {
    return localizedFail(code, key, fallback, values, extra);
}
export function localizeCommandFailure(failure) {
    const descriptor = failure[COMMAND_FAILURE_MESSAGE];
    return descriptor ? renderVoiceMessage(descriptor) : failure.message;
}
export function cloneCommandFailure(failure, extra = {}) {
    const descriptor = failure[COMMAND_FAILURE_MESSAGE];
    const cloned = {
        ...failure,
        ...extra,
        message: descriptor ? renderVoiceMessage(descriptor) : failure.message,
    };
    return descriptor ? attachFailureMessage(cloned, descriptor) : cloned;
}
export function isCommandFailure(result) {
    return result.ok === false;
}
