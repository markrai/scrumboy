import { normalizeLookup } from './normalize.js';
import { resolveConversationTodoTarget, } from './conversation-resolve.js';
import { formatResolvedCommand, resolveVoiceLane, voiceBoardLanes, } from './resolve.js';
import { isCommandFailure, localizedCommandFailure, normalizeTodoTitle, validateCommandIR, } from './schema.js';
import { resolveTodoTarget } from './target-resolver.js';
function todoReference(context, todo) {
    return Object.freeze({
        kind: 'todo',
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        localId: todo.localId,
    });
}
function laneForTodo(board, todo) {
    const containingColumn = Object.entries(board.columns ?? {}).find(([, todos]) => todos.some((candidate) => candidate.id === todo.id));
    const key = todo.columnKey
        ?? containingColumn?.[0]
        ?? voiceBoardLanes(board).find((lane) => lane.key.toLocaleLowerCase() === todo.status.toLocaleLowerCase())?.key
        ?? todo.status;
    return voiceBoardLanes(board).find((lane) => lane.key === key)
        ?? { key, name: key.replace(/_/g, ' '), isDone: false };
}
function resolvedTodoCandidate(context, todo) {
    return Object.freeze({
        todo,
        reference: todoReference(context, todo),
        lane: laneForTodo(context.board, todo),
    });
}
function classifyTodoCandidates(candidates, classify) {
    return candidates.map((candidate) => Object.freeze({
        candidate,
        disposition: classify(candidate),
    }));
}
function targetContext(context) {
    return {
        projectSlug: context.projectSlug,
        board: context.board,
        callTool: context.callTool,
    };
}
async function resolveSelectedTodo(selection, context) {
    if (!selection.allowedLocalIds.includes(selection.selectedLocalId)) {
        return localizedCommandFailure('invalid_schema', 'voice.errors.selectedTodoNotOffered', 'Selected todo was not one of the offered choices.');
    }
    const resolved = await resolveTodoTarget({
        kind: 'id',
        localId: selection.selectedLocalId,
        display: String(selection.selectedLocalId),
    }, targetContext(context));
    if (isCommandFailure(resolved))
        return resolved;
    return { ok: true, value: [resolvedTodoCandidate(context, resolved.value.todo)] };
}
async function resolveTodoCandidates(target, state, context, selection) {
    if (selection)
        return resolveSelectedTodo(selection, context);
    if (target.kind === 'current' || target.kind === 'todo') {
        const resolved = resolveConversationTodoTarget(target, state, {
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            board: context.board,
        });
        if (resolved.ok === false) {
            return resolved.code === 'no_active_todo'
                ? localizedCommandFailure('unknown_story', 'voice.errors.todoReferenceRequired', 'Todo reference is required.')
                : localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
        }
        return {
            ok: true,
            value: [resolvedTodoCandidate(context, resolved.value.todo)],
        };
    }
    if (target.kind === 'local-id') {
        const resolved = await resolveTodoTarget({
            kind: 'id',
            localId: target.localId,
            display: String(target.localId),
        }, targetContext(context));
        if (isCommandFailure(resolved))
            return resolved;
        return { ok: true, value: [resolvedTodoCandidate(context, resolved.value.todo)] };
    }
    const resolved = await resolveTodoTarget({
        kind: 'title',
        phrase: target.text,
        display: target.text,
    }, targetContext(context));
    if (!isCommandFailure(resolved)) {
        return { ok: true, value: [resolvedTodoCandidate(context, resolved.value.todo)] };
    }
    if (resolved.code !== 'ambiguous_story' || !resolved.candidates?.length)
        return resolved;
    const candidates = [];
    for (const candidate of resolved.candidates) {
        const candidateResult = await resolveTodoTarget({
            kind: 'id',
            localId: candidate.localId,
            display: String(candidate.localId),
        }, targetContext(context));
        if (!isCommandFailure(candidateResult)) {
            candidates.push(resolvedTodoCandidate(context, candidateResult.value.todo));
        }
    }
    if (candidates.length === 0)
        return resolved;
    return { ok: true, value: candidates };
}
function clarification(intent, choices, selection, member = false) {
    const options = choices.map((choice) => Object.freeze({
        id: choice.kind === 'todo'
            ? `todo:${choice.reference.localId}`
            : `member:${choice.userId}`,
        label: choice.kind === 'todo'
            ? `#${choice.reference.localId} · ${choice.title} · ${choice.laneName}`
            : choice.email && choice.email !== choice.name
                ? `${choice.name} · ${choice.email}`
                : choice.name,
    }));
    return Object.freeze({
        kind: 'clarification',
        intent,
        choices: Object.freeze([...choices]),
        selection,
        interaction: Object.freeze({
            kind: 'clarification',
            response: 'choice',
            message: {
                key: member ? 'voice.prompt.whichPerson' : 'voice.prompt.whichOne',
                fallback: member ? 'Which person?' : 'Which one?',
            },
            options: Object.freeze(options),
        }),
    });
}
function todoClarification(intent, candidates, selection) {
    return clarification(intent, candidates.slice(0, 3).map((candidate) => Object.freeze({
        kind: 'todo',
        reference: candidate.reference,
        title: candidate.todo.title,
        laneKey: candidate.lane.key,
        laneName: candidate.lane.name,
    })), selection);
}
function information(key, fallback, values) {
    return Object.freeze({
        kind: 'information',
        interaction: Object.freeze({
            kind: 'information',
            message: Object.freeze({ key, fallback, values: Object.freeze({ ...values }) }),
        }),
    });
}
function buildResolvedCommand(ir, context, details) {
    const validated = validateCommandIR(ir, {
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        board: context.board,
    });
    if (isCommandFailure(validated))
        return validated;
    const unresolvedDisplay = {
        ir: validated.value,
        summary: '',
        confirmLabel: '',
        danger: details.danger,
        requiresConfirmation: details.requiresConfirmation,
        ...(details.storyTitle == null ? {} : { storyTitle: details.storyTitle }),
        ...(details.statusName == null ? {} : { statusName: details.statusName }),
        ...(details.assigneeName == null ? {} : { assigneeName: details.assigneeName }),
    };
    return {
        ok: true,
        value: { ...unresolvedDisplay, ...formatResolvedCommand(unresolvedDisplay) },
    };
}
function commandResolution(command, target, selection) {
    return Object.freeze({ kind: 'command', command, target, selection });
}
function uniqueMembers(members) {
    return Array.from(new Map(members.map((member) => [member.userId, member])).values());
}
function matchingMembers(reference, members) {
    const wanted = normalizeLookup(reference.text);
    if (!wanted)
        return [];
    const available = uniqueMembers(members);
    const exactEmail = available.filter((member) => normalizeLookup(member.email) === wanted);
    if (reference.kind === 'email')
        return exactEmail;
    const exactName = available.filter((member) => normalizeLookup(member.name) === wanted);
    if (exactName.length > 0)
        return exactName;
    if (exactEmail.length > 0)
        return exactEmail;
    return available.filter((member) => {
        const name = normalizeLookup(member.name);
        if (!name)
            return false;
        const components = name.split(' ').filter(Boolean);
        return name.startsWith(`${wanted} `)
            || components.includes(wanted)
            || (wanted.length >= 2 && components.some((component) => component.startsWith(wanted)));
    });
}
async function projectMembers(context, requireRefresh) {
    if (!requireRefresh || !context.callTool)
        return context.members;
    try {
        const response = await context.callTool('members_list', {
            projectSlug: context.projectSlug,
        });
        if (!Array.isArray(response?.items))
            return context.members;
        return uniqueMembers(response.items);
    }
    catch {
        return context.members;
    }
}
async function resolveMemberCandidates(reference, context) {
    const local = matchingMembers(reference, context.members);
    if (local.length > 0)
        return local;
    return matchingMembers(reference, await projectMembers(context, true));
}
async function selectedMember(selection, context) {
    if (!selection.allowedUserIds.includes(selection.selectedUserId)) {
        return localizedCommandFailure('invalid_schema', 'voice.errors.selectedMemberNotOffered', 'Selected member was not one of the offered choices.');
    }
    const members = await projectMembers(context, true);
    const member = members.find((candidate) => candidate.userId === selection.selectedUserId);
    return member
        ? { ok: true, value: member }
        : localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
}
function memberClarification(intent, members, selection) {
    return clarification(intent, members.slice(0, 3).map((member) => Object.freeze({
        kind: 'member',
        userId: member.userId,
        name: member.name || member.email,
        email: member.email,
    })), selection, true);
}
function effectiveTarget(intent, state) {
    return intent.kind === 'update-todo-title'
        && state.pending?.kind === 'missing-slot'
        && intent.target.kind === 'current'
        ? state.pending.target
        : intent.target;
}
function titleQuestion(target) {
    return Object.freeze({
        kind: 'question',
        target: target.reference,
        interaction: Object.freeze({
            kind: 'question',
            response: 'free-text',
            message: Object.freeze({
                key: 'voice.question.updateTitle',
                fallback: 'What would you like to change the title to?',
            }),
        }),
    });
}
function failureFromEmptyCandidates() {
    return localizedCommandFailure('unknown_story', 'voice.errors.todoNotFound', 'Todo was not found in this project.');
}
export async function resolveVoiceSemanticIntent(intent, state, context, selection = {}) {
    if (intent.kind === 'create-todo') {
        const destination = voiceBoardLanes(context.board)[0];
        if (!destination) {
            return localizedCommandFailure('unknown_status', 'voice.errors.statusNotFound', 'Status was not found on this board.');
        }
        const resolved = buildResolvedCommand({
            intent: 'todos.create',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { title: intent.title, columnKey: destination.key },
        }, context, { danger: false, requiresConfirmation: true });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, null, selection) };
    }
    if (intent.kind === 'move-todo') {
        const lane = resolveVoiceLane(intent.destination.text, context.board);
        if (isCommandFailure(lane))
            return lane;
        const candidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
        if (isCommandFailure(candidates))
            return candidates;
        const classified = classifyTodoCandidates(candidates.value, (candidate) => candidate.lane.key === lane.value.key ? 'already-satisfied' : 'actionable');
        const actionable = classified
            .filter((candidate) => candidate.disposition === 'actionable')
            .map((candidate) => candidate.candidate);
        if (actionable.length > 1) {
            return { ok: true, value: todoClarification(intent, actionable, selection) };
        }
        if (actionable.length === 0) {
            const satisfied = classified.find((candidate) => candidate.disposition === 'already-satisfied')?.candidate;
            if (!satisfied)
                return failureFromEmptyCandidates();
            return {
                ok: true,
                value: information('voice.info.alreadyInLane', '{title} is already in {lane}.', { title: satisfied.todo.title, lane: lane.value.name }),
            };
        }
        const target = actionable[0];
        const resolved = buildResolvedCommand({
            intent: 'todos.move',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, toColumnKey: lane.value.key },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
            statusName: lane.value.name,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    if (intent.kind === 'assign-todo') {
        let resolvedMember;
        if (selection.member) {
            const member = await selectedMember(selection.member, context);
            if (isCommandFailure(member))
                return member;
            resolvedMember = member.value;
        }
        if (!resolvedMember) {
            const members = await resolveMemberCandidates(intent.assignee, context);
            if (members.length === 0) {
                return localizedCommandFailure('unknown_user', 'voice.errors.assigneeNotFound', 'Assignee was not found in this project.');
            }
            if (members.length > 1) {
                return { ok: true, value: memberClarification(intent, members, selection) };
            }
            resolvedMember = members[0];
        }
        const candidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
        if (isCommandFailure(candidates))
            return candidates;
        const classified = classifyTodoCandidates(candidates.value, (candidate) => candidate.todo.assigneeUserId === resolvedMember.userId
            ? 'already-satisfied'
            : 'actionable');
        const actionable = classified
            .filter((candidate) => candidate.disposition === 'actionable')
            .map((candidate) => candidate.candidate);
        if (actionable.length > 1) {
            return { ok: true, value: todoClarification(intent, actionable, selection) };
        }
        if (actionable.length === 0) {
            const satisfied = classified.find((candidate) => candidate.disposition === 'already-satisfied')?.candidate;
            if (!satisfied)
                return failureFromEmptyCandidates();
            return {
                ok: true,
                value: information('voice.info.alreadyAssigned', '{title} is already assigned to {member}.', { title: satisfied.todo.title, member: resolvedMember.name || resolvedMember.email }),
            };
        }
        const target = actionable[0];
        const resolved = buildResolvedCommand({
            intent: 'todos.assign',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, assigneeUserId: resolvedMember.userId },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
            assigneeName: resolvedMember.name || resolvedMember.email,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    const candidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
    if (isCommandFailure(candidates))
        return candidates;
    if (intent.kind === 'update-todo-title') {
        if (intent.title === null) {
            if (candidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, candidates.value, selection) };
            }
            const target = candidates.value[0];
            return target
                ? { ok: true, value: titleQuestion(target) }
                : failureFromEmptyCandidates();
        }
        const title = normalizeTodoTitle(intent.title);
        if (title === null) {
            return localizedCommandFailure('invalid_title', 'voice.errors.schema.titleLength', 'Todo title must be between 1 and 200 characters.');
        }
        const classified = classifyTodoCandidates(candidates.value, (candidate) => normalizeLookup(candidate.todo.title) === normalizeLookup(title)
            ? 'already-satisfied'
            : 'actionable');
        const actionable = classified
            .filter((candidate) => candidate.disposition === 'actionable')
            .map((candidate) => candidate.candidate);
        if (actionable.length > 1) {
            return { ok: true, value: todoClarification(intent, actionable, selection) };
        }
        if (actionable.length === 0) {
            const satisfied = classified.find((candidate) => candidate.disposition === 'already-satisfied')?.candidate;
            if (!satisfied)
                return failureFromEmptyCandidates();
            return {
                ok: true,
                value: information('voice.info.titleUnchanged', '{title} already has that title.', { title: satisfied.todo.title }),
            };
        }
        const target = actionable[0];
        const resolved = buildResolvedCommand({
            intent: 'todos.update_title',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, title },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    if (candidates.value.length > 1) {
        return { ok: true, value: todoClarification(intent, candidates.value, selection) };
    }
    const target = candidates.value[0];
    if (!target)
        return failureFromEmptyCandidates();
    const ir = intent.kind === 'open-todo'
        ? {
            intent: 'open_todo',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId },
        }
        : {
            intent: 'todos.delete',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId },
        };
    const resolved = buildResolvedCommand(ir, context, {
        danger: intent.kind === 'delete-todo',
        requiresConfirmation: intent.kind === 'delete-todo',
        storyTitle: target.todo.title,
    });
    return isCommandFailure(resolved)
        ? resolved
        : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
}
