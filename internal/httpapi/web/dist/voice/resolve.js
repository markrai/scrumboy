import { normalizeLookup } from './normalize.js';
import { cloneCommandFailure, localizedCommandFailure, isCommandFailure, validateCommandIR } from './schema.js';
import { BUILTIN_STATUS_ALIASES } from './vocabulary.js';
import { resolveTodoTarget } from './target-resolver.js';
import { voiceText } from './i18n.js';
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
        return localizedCommandFailure("unknown_status", "voice.errors.statusNotFound", "Status was not found on this board.");
    }
    const lanes = Array.from(matches);
    if (lanes.length > 1) {
        return localizedCommandFailure("ambiguous_status", "voice.errors.statusAmbiguous", "Status matches more than one lane.");
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
            const data = await context.callTool("members_list", {
                projectSlug: context.projectSlug,
            });
            if (Array.isArray(data?.items)) {
                matches = findMatchingMembers(rawUser, data.items);
            }
        }
        catch {
            return localizedCommandFailure("unknown_user", "voice.errors.assigneeNotFound", "Assignee was not found in this project.");
        }
    }
    if (matches.length === 0) {
        return localizedCommandFailure("unknown_user", "voice.errors.assigneeNotFound", "Assignee was not found in this project.");
    }
    const uniqueById = new Map(matches.map((member) => [member.userId, member]));
    if (uniqueById.size > 1) {
        return localizedCommandFailure("ambiguous_user", "voice.errors.assigneeAmbiguous", "Assignee matches more than one project member.");
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
function withDraft(failure, draft) {
    return cloneCommandFailure(failure, { draft });
}
async function resolveDraftTarget(draft, context, options) {
    const resolved = await resolveTodoTarget(draft.target, {
        projectSlug: context.projectSlug,
        board: context.board,
        callTool: context.callTool,
    }, options.selectedLocalId, options.allowedLocalIds);
    if (isCommandFailure(resolved)) {
        return resolved.code === "ambiguous_story" ? withDraft(resolved, draft) : resolved;
    }
    return resolved;
}
export function formatResolvedCommand(command) {
    switch (command.ir.intent) {
        case "todos.create": {
            const title = command.ir.entities.title;
            return {
                summary: voiceText("voice.summary.create", "Create todo \"{title}\"", { title }),
                confirmLabel: voiceText("voice.action.create", "Create"),
            };
        }
        case "open_todo": {
            const localId = command.ir.entities.localId;
            const title = command.storyTitle ?? "";
            return {
                summary: voiceText("voice.summary.open", "Open todo #{localId}: {title}", { localId, title }),
                confirmLabel: voiceText("voice.action.open", "Open"),
            };
        }
        case "todos.delete": {
            const localId = command.ir.entities.localId;
            const title = command.storyTitle ?? "";
            return {
                summary: voiceText("voice.summary.delete", "Delete todo #{localId}: {title}", { localId, title }),
                confirmLabel: voiceText("voice.action.delete", "Delete"),
            };
        }
        case "todos.move": {
            const localId = command.ir.entities.localId;
            const title = command.storyTitle ?? "";
            const statusName = command.statusName ?? "";
            return {
                summary: voiceText("voice.summary.move", "Move todo #{localId}: {title} to {statusName}", { localId, title, statusName }),
                confirmLabel: voiceText("voice.action.move", "Move"),
            };
        }
        case "todos.assign": {
            const localId = command.ir.entities.localId;
            const title = command.storyTitle ?? "";
            const assigneeName = command.assigneeName ?? "";
            return {
                summary: voiceText("voice.summary.assign", "Assign todo #{localId}: {title} to {assigneeName}", { localId, title, assigneeName }),
                confirmLabel: voiceText("voice.action.assign", "Assign"),
            };
        }
        default: {
            const exhaustive = command.ir;
            return exhaustive;
        }
    }
}
function withResolvedCommandDisplay(command) {
    return { ...command, ...formatResolvedCommand(command) };
}
export async function resolveCommandDraft(draft, context, options = {}) {
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
            value: withResolvedCommandDisplay({
                ir: validated.value,
                summary: "",
                confirmLabel: "",
                danger: false,
                requiresConfirmation: true,
            }),
        };
    }
    if (draft.intent === "open_todo") {
        const target = await resolveDraftTarget(draft, context, options);
        if (isCommandFailure(target))
            return target;
        const todo = target.value.todo;
        const ir = {
            intent: "open_todo",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: todo.localId },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: withResolvedCommandDisplay({
                ir: validated.value,
                summary: "",
                confirmLabel: "",
                danger: false,
                requiresConfirmation: !!target.value.ambiguousId,
                storyTitle: todo.title,
            }),
        };
    }
    if (draft.intent === "todos.delete") {
        const target = await resolveDraftTarget(draft, context, options);
        if (isCommandFailure(target))
            return target;
        const todo = target.value.todo;
        const ir = {
            intent: "todos.delete",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: todo.localId },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: withResolvedCommandDisplay({
                ir: validated.value,
                summary: "",
                confirmLabel: "",
                danger: true,
                requiresConfirmation: true,
                storyTitle: todo.title,
            }),
        };
    }
    if (draft.intent === "todos.move") {
        const lane = resolveStatus(draft.rawStatus, context.board);
        if (isCommandFailure(lane))
            return lane;
        const target = await resolveDraftTarget(draft, context, options);
        if (isCommandFailure(target))
            return target;
        const todo = target.value.todo;
        const ir = {
            intent: "todos.move",
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: todo.localId, toColumnKey: lane.value.key },
        };
        const validated = validateResolvedIR(ir, context);
        if (isCommandFailure(validated))
            return validated;
        return {
            ok: true,
            value: withResolvedCommandDisplay({
                ir: validated.value,
                summary: "",
                confirmLabel: "",
                danger: false,
                requiresConfirmation: true,
                storyTitle: todo.title,
                statusName: lane.value.name,
            }),
        };
    }
    const member = await resolveMember(draft.rawUser, context);
    if (isCommandFailure(member))
        return member;
    const target = await resolveDraftTarget(draft, context, options);
    if (isCommandFailure(target))
        return target;
    const todo = target.value.todo;
    const ir = {
        intent: "todos.assign",
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        entities: { localId: todo.localId, assigneeUserId: member.value.userId },
    };
    const validated = validateResolvedIR(ir, context);
    if (isCommandFailure(validated))
        return validated;
    return {
        ok: true,
        value: withResolvedCommandDisplay({
            ir: validated.value,
            summary: "",
            confirmLabel: "",
            danger: false,
            requiresConfirmation: true,
            storyTitle: todo.title,
            assigneeName: member.value.name || member.value.email,
        }),
    };
}
