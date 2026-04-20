import { normalizeLookup } from './normalize.js';
import { commandFailure, isCommandFailure, validateCommandIR } from './schema.js';
import { BUILTIN_STATUS_ALIASES } from './vocabulary.js';
function boardLanes(board) {
    if (board.columnOrder && board.columnOrder.length > 0) {
        return board.columnOrder.map((lane) => ({
            key: lane.key,
            name: lane.name,
            isDone: !!lane.isDone,
        }));
    }
    return Object.keys(board.columns ?? {}).map((key) => ({
        key,
        name: key.replace(/_/g, " "),
        isDone: key === "done",
    }));
}
function findTodoInBoard(board, localId) {
    for (const todos of Object.values(board.columns ?? {})) {
        const found = todos.find((todo) => todo.localId === localId);
        if (found)
            return found;
    }
    return null;
}
async function resolveTodo(localId, context) {
    const fromBoard = findTodoInBoard(context.board, localId);
    if (fromBoard)
        return { ok: true, value: fromBoard };
    if (!context.callTool) {
        return commandFailure("unknown_story", `Todo #${localId} was not found in this project.`);
    }
    try {
        const data = await context.callTool("todos.get", {
            projectSlug: context.projectSlug,
            localId,
        });
        if (data?.todo?.localId === localId) {
            return { ok: true, value: data.todo };
        }
        return commandFailure("unknown_story", `Todo #${localId} was not found in this project.`);
    }
    catch {
        return commandFailure("unknown_story", `Todo #${localId} was not found in this project.`);
    }
}
function addAlias(aliases, alias, lane) {
    const key = normalizeLookup(alias);
    if (!key)
        return;
    const existing = aliases.get(key) ?? new Set();
    existing.add(lane);
    aliases.set(key, existing);
}
function buildLaneAliasMap(board) {
    const lanes = boardLanes(board);
    const byKey = new Map(lanes.map((lane) => [lane.key, lane]));
    const aliases = new Map();
    for (const lane of lanes) {
        addAlias(aliases, lane.name, lane);
        addAlias(aliases, lane.key, lane);
        addAlias(aliases, lane.key.replace(/_/g, " "), lane);
    }
    const doneLane = byKey.get("done") ?? lanes.find((lane) => lane.isDone);
    for (const [alias, key] of BUILTIN_STATUS_ALIASES) {
        const targetKey = key === "done" ? doneLane?.key : key;
        if (!targetKey)
            continue;
        const lane = byKey.get(targetKey);
        if (lane)
            addAlias(aliases, alias, lane);
    }
    return aliases;
}
function resolveStatus(rawStatus, board) {
    const alias = normalizeLookup(rawStatus);
    const matches = buildLaneAliasMap(board).get(alias);
    if (!matches || matches.size === 0) {
        return commandFailure("unknown_status", "Status was not found on this board.");
    }
    const lanes = Array.from(matches);
    if (lanes.length > 1) {
        return commandFailure("ambiguous_status", "Status matches more than one lane.");
    }
    return { ok: true, value: lanes[0] };
}
function memberAliases(member) {
    const aliases = [];
    if (member.name)
        aliases.push(member.name);
    if (member.email) {
        aliases.push(member.email);
        aliases.push(member.email.split("@")[0]);
    }
    return aliases.map(normalizeLookup).filter(Boolean);
}
function findMatchingMembers(rawUser, members) {
    const wanted = normalizeLookup(rawUser);
    if (!wanted)
        return [];
    return members.filter((member) => memberAliases(member).includes(wanted));
}
async function resolveMember(rawUser, context) {
    let matches = findMatchingMembers(rawUser, context.members);
    if (matches.length === 0 && context.callTool) {
        try {
            const data = await context.callTool("members.list", {
                projectSlug: context.projectSlug,
            });
            if (Array.isArray(data?.items)) {
                matches = findMatchingMembers(rawUser, data.items);
            }
        }
        catch {
            return commandFailure("unknown_user", "Assignee was not found in this project.");
        }
    }
    if (matches.length === 0) {
        return commandFailure("unknown_user", "Assignee was not found in this project.");
    }
    const uniqueById = new Map(matches.map((member) => [member.userId, member]));
    if (uniqueById.size > 1) {
        return commandFailure("ambiguous_user", "Assignee matches more than one project member.");
    }
    return { ok: true, value: Array.from(uniqueById.values())[0] };
}
function validateResolvedIR(ir, context) {
    return validateCommandIR(ir, {
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        board: context.board,
    });
}
export async function resolveCommandDraft(draft, context) {
    if (draft.intent === "todos.create") {
        const ir = {
            intent: "todos.create",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { title: draft.title },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: {
                ir: validated.value,
                summary: `Create todo "${ir.entities.title}"`,
                confirmLabel: "Create",
                danger: false,
                requiresConfirmation: true,
            },
        };
    }
    const todo = await resolveTodo(draft.localId, context);
    if (isCommandFailure(todo))
        return todo;
    if (draft.intent === "open_todo") {
        const ir = {
            intent: "open_todo",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: draft.localId },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: {
                ir: validated.value,
                summary: `Open todo #${draft.localId}: ${todo.value.title}`,
                confirmLabel: "Open",
                danger: false,
                requiresConfirmation: !!draft.ambiguousId,
                storyTitle: todo.value.title,
            },
        };
    }
    if (draft.intent === "todos.delete") {
        const ir = {
            intent: "todos.delete",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: draft.localId },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: {
                ir: validated.value,
                summary: `Delete todo #${draft.localId}: ${todo.value.title}`,
                confirmLabel: "Delete",
                danger: true,
                requiresConfirmation: true,
                storyTitle: todo.value.title,
            },
        };
    }
    if (draft.intent === "todos.move") {
        const lane = resolveStatus(draft.rawStatus, context.board);
        if (isCommandFailure(lane))
            return lane;
        const ir = {
            intent: "todos.move",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: draft.localId, toColumnKey: lane.value.key },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: {
                ir: validated.value,
                summary: `Move todo #${draft.localId}: ${todo.value.title} to ${lane.value.name}`,
                confirmLabel: "Move",
                danger: false,
                requiresConfirmation: true,
                storyTitle: todo.value.title,
                statusName: lane.value.name,
            },
        };
    }
    const member = await resolveMember(draft.rawUser, context);
    if (isCommandFailure(member))
        return member;
    const ir = {
        intent: "todos.assign",
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        entities: { localId: draft.localId, assigneeUserId: member.value.userId },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated))
        return validated;
    return {
        ok: true,
        value: {
            ir: validated.value,
            summary: `Assign todo #${draft.localId}: ${todo.value.title} to ${member.value.name || member.value.email}`,
            confirmLabel: "Assign",
            danger: false,
            requiresConfirmation: true,
            storyTitle: todo.value.title,
            assigneeName: member.value.name || member.value.email,
        },
    };
}
