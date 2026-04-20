function fail(code, message) {
    return { ok: false, code, message };
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
        return fail("invalid_schema", "Command shape is invalid.");
    }
    const ir = value;
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
export function commandFailure(code, message) {
    return fail(code, message);
}
export function isCommandFailure(result) {
    return result.ok === false;
}
