import { commandFailure, isCommandFailure } from './schema.js';
import { containsProjectScopeOverride, normalizePhrase, parseSpokenNumber, stripWrappingQuotes, } from './normalize.js';
import { ENTITY_ALIAS_PATTERN, normalizeEntityAlias } from './vocabulary.js';
function parseId(raw) {
    const parsed = parseSpokenNumber(raw);
    if (!parsed) {
        return commandFailure("invalid_id", "Todo ID was not recognized.");
    }
    return { ok: true, value: { localId: parsed.value, ambiguousId: parsed.ambiguous } };
}
function entityPattern() {
    return ENTITY_ALIAS_PATTERN;
}
function isEntityAlias(raw) {
    return normalizeEntityAlias(raw) === "todo";
}
function parseCreate(input) {
    const match = input.match(new RegExp(`^create\\s+${entityPattern()}\\s+(.+)$`, "i"));
    if (!match)
        return null;
    if (!isEntityAlias(match[1]))
        return null;
    const title = stripWrappingQuotes(match[2]);
    if (!title) {
        return commandFailure("invalid_title", "Todo title is required.");
    }
    return { ok: true, value: { intent: "todos.create", title, display: `create todo ${title}` } };
}
function parseMove(input) {
    const match = input.match(new RegExp(`^move\\s+(?:(?:${entityPattern()})\\s+)?(.+?)\\s+to\\s+(.+)$`, "i"));
    if (!match)
        return null;
    if (match[1] && !isEntityAlias(match[1]))
        return null;
    const id = parseId(match[2]);
    if (isCommandFailure(id))
        return id;
    const rawStatus = normalizePhrase(match[3]);
    if (!rawStatus) {
        return commandFailure("unknown_status", "Status is required.");
    }
    return {
        ok: true,
        value: {
            intent: "todos.move",
            localId: id.value.localId,
            rawStatus,
            ambiguousId: id.value.ambiguousId,
            display: `move todo ${id.value.localId} to ${rawStatus}`,
        },
    };
}
function parseTodoIs(input) {
    const match = input.match(new RegExp(`^${entityPattern()}\\s+(.+?)\\s+is\\s+(.+)$`, "i"));
    if (!match)
        return null;
    if (!isEntityAlias(match[1]))
        return null;
    const id = parseId(match[2]);
    if (isCommandFailure(id))
        return id;
    const rawStatus = normalizePhrase(match[3]);
    if (!rawStatus) {
        return commandFailure("unknown_status", "Status is required.");
    }
    return {
        ok: true,
        value: {
            intent: "todos.move",
            localId: id.value.localId,
            rawStatus,
            ambiguousId: id.value.ambiguousId,
            display: `todo ${id.value.localId} is ${rawStatus}`,
        },
    };
}
function parseDelete(input) {
    const match = input.match(new RegExp(`^delete\\s+(?:(?:${entityPattern()})\\s+)?(.+)$`, "i"));
    if (!match)
        return null;
    if (match[1] && !isEntityAlias(match[1]))
        return null;
    const id = parseId(match[2]);
    if (isCommandFailure(id))
        return id;
    return {
        ok: true,
        value: {
            intent: "todos.delete",
            localId: id.value.localId,
            ambiguousId: id.value.ambiguousId,
            display: `delete todo ${id.value.localId}`,
        },
    };
}
function parseOpen(input) {
    const match = input.match(new RegExp(`^(open|edit)\\s+(?:(?:${entityPattern()})\\s+)?(.+)$`, "i"));
    if (!match)
        return null;
    if (match[2] && !isEntityAlias(match[2]))
        return null;
    const id = parseId(match[3]);
    if (isCommandFailure(id))
        return id;
    return {
        ok: true,
        value: {
            intent: "open_todo",
            localId: id.value.localId,
            ambiguousId: id.value.ambiguousId,
            display: `${normalizePhrase(match[1])} todo ${id.value.localId}`,
        },
    };
}
function parseAssign(input) {
    const match = input.match(new RegExp(`^assign\\s+${entityPattern()}\\s+(.+?)\\s+to\\s+(.+)$`, "i"));
    if (!match)
        return null;
    if (!isEntityAlias(match[1]))
        return null;
    const id = parseId(match[2]);
    if (isCommandFailure(id))
        return id;
    const rawUser = normalizePhrase(match[3]);
    if (!rawUser) {
        return commandFailure("unknown_user", "Assignee is required.");
    }
    return {
        ok: true,
        value: {
            intent: "todos.assign",
            localId: id.value.localId,
            rawUser,
            ambiguousId: id.value.ambiguousId,
            display: `assign todo ${id.value.localId} to ${rawUser}`,
        },
    };
}
export function parseCommand(input) {
    const trimmed = String(input ?? "").trim();
    if (!trimmed) {
        return commandFailure("unsupported", "Command is required.");
    }
    if (containsProjectScopeOverride(trimmed)) {
        return commandFailure("project_scope", "Project scope is fixed by the current board.");
    }
    const parsers = [parseCreate, parseMove, parseDelete, parseOpen, parseAssign, parseTodoIs];
    for (const parser of parsers) {
        const parsed = parser(trimmed);
        if (parsed)
            return parsed;
    }
    return commandFailure("unsupported", "Unsupported command.");
}
