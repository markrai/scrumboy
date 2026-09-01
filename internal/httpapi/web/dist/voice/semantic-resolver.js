import { normalizeLookup } from './normalize.js';
import { resolveConversationTodoTarget, } from './conversation-resolve.js';
import { voiceSemanticOperation } from './dialogue-intent.js';
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
async function refreshTodoCandidate(candidate, context) {
    if (!context.callTool)
        return { ok: true, value: candidate };
    try {
        const response = await context.callTool('todos_get', {
            projectSlug: context.projectSlug,
            localId: candidate.reference.localId,
        });
        if (response?.todo?.localId !== candidate.reference.localId) {
            return localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
        }
        return { ok: true, value: resolvedTodoCandidate(context, response.todo) };
    }
    catch {
        return localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
    }
}
async function refreshTodoCandidates(candidates, context) {
    const refreshed = [];
    for (const candidate of candidates) {
        const result = await refreshTodoCandidate(candidate, context);
        if (isCommandFailure(result))
            return result;
        refreshed.push(result.value);
    }
    return { ok: true, value: refreshed };
}
function projectTagNames(board) {
    const names = new Map();
    for (const tag of board.tags ?? []) {
        const name = tag.name?.trim();
        const normalized = normalizeLookup(name ?? '');
        if (name && normalized && !names.has(normalized))
            names.set(normalized, name);
    }
    return [...names.values()];
}
function matchingTagNames(reference, board) {
    const wanted = normalizeLookup(reference.text);
    if (!wanted)
        return [];
    const available = projectTagNames(board);
    const exact = available.filter((name) => normalizeLookup(name) === wanted);
    if (exact.length > 0)
        return exact;
    return available.filter((name) => {
        const normalized = normalizeLookup(name);
        const components = normalized.split(' ').filter(Boolean);
        return normalized.startsWith(`${wanted} `)
            || (wanted.length >= 2 && components.some((component) => component.startsWith(wanted)));
    });
}
function selectedTagName(selection, context) {
    if (!selection.allowedNames.includes(selection.selectedName)) {
        return localizedCommandFailure('invalid_schema', 'voice.errors.selectedTagNotOffered', 'Selected tag was not one of the offered choices.');
    }
    const selected = projectTagNames(context.board).find((name) => normalizeLookup(name) === normalizeLookup(selection.selectedName));
    return selected
        ? { ok: true, value: selected }
        : localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
}
function tagClarification(intent, names, selection) {
    return clarification(intent, names.slice(0, 3).map((name) => Object.freeze({
        kind: 'tag',
        name,
    })), selection);
}
function todoHasTag(todo, tagName) {
    const wanted = normalizeLookup(tagName);
    return (todo.tags ?? []).some((tag) => normalizeLookup(tag) === wanted);
}
function tagsAfterAdd(todo, tagName) {
    return todoHasTag(todo, tagName) ? [...(todo.tags ?? [])] : [...(todo.tags ?? []), tagName];
}
function tagsAfterRemove(todo, tagName) {
    const wanted = normalizeLookup(tagName);
    return (todo.tags ?? []).filter((tag) => normalizeLookup(tag) !== wanted);
}
function appendedNotes(body, notes) {
    const existing = body ?? '';
    if (!existing)
        return notes;
    return existing.endsWith('\n') ? `${existing}${notes}` : `${existing}\n${notes}`;
}
function clarification(intent, choices, selection) {
    const options = choices.map((choice) => Object.freeze({
        id: choice.kind === 'todo'
            ? `todo:${choice.reference.localId}`
            : choice.kind === 'member'
                ? `member:${choice.userId}`
                : `tag:${choice.name}`,
        label: choice.kind === 'todo'
            ? `#${choice.reference.localId} · ${choice.title} · ${choice.laneName}`
            : choice.kind === 'member'
                ? choice.email && choice.email !== choice.name
                    ? `${choice.name} · ${choice.email}`
                    : choice.name
                : choice.name,
    }));
    const firstKind = choices[0]?.kind;
    const prompt = firstKind === 'member'
        ? {
            key: 'voice.prompt.whichPerson',
            fallback: 'Which person?',
        }
        : firstKind === 'tag'
            ? {
                key: 'voice.prompt.whichTag',
                fallback: 'Which tag?',
            }
            : {
                key: 'voice.prompt.whichOne',
                fallback: 'Which one?',
            };
    const spokenChoices = choices.length <= 3
        ? choices.map((choice) => choice.kind === 'todo'
            ? `number ${choice.reference.localId} in ${choice.laneName}`
            : choice.kind === 'member'
                ? choice.email && choice.email !== choice.name
                    ? `${choice.name}, ${choice.email}`
                    : choice.name
                : choice.name).join(' or ')
        : '';
    return Object.freeze({
        kind: 'clarification',
        intent,
        choices: Object.freeze([...choices]),
        selection,
        interaction: Object.freeze({
            kind: 'clarification',
            response: 'choice',
            message: prompt,
            speech: spokenChoices
                ? {
                    key: 'voice.prompt.whichChoiceSpoken',
                    fallback: 'I found {count} matches. Which one, {choices}?',
                    values: { count: choices.length, choices: spokenChoices },
                }
                : {
                    key: 'voice.prompt.whichChoiceManySpoken',
                    fallback: 'I found {count} matches. Which one?',
                    values: { count: choices.length },
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
function information(key, fallback, values, target) {
    return Object.freeze({
        kind: 'information',
        ...(target ? { target } : {}),
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
    })), selection);
}
function effectiveTarget(intent, state) {
    const pending = state.pending;
    return pending?.kind === 'missing-slot'
        && 'target' in pending
        && pending.operation === voiceSemanticOperation(intent)
        ? pending.target
        : intent.target;
}
function missingSlotQuestion(intent, pendingSlot, selection, target) {
    const prompt = (() => {
        switch (pendingSlot.operation) {
            case 'todo.create':
                return { key: 'voice.question.createTitle', fallback: 'What should I call it?' };
            case 'todo.move':
                return { key: 'voice.question.moveDestination', fallback: 'Where should I move it?' };
            case 'todo.assign':
                return { key: 'voice.question.assignMember', fallback: 'Who should I assign it to?' };
            case 'todo.update_title':
                return {
                    key: 'voice.question.updateTitle',
                    fallback: 'What would you like to change the title to?',
                };
            case 'todo.append_notes':
                return { key: 'voice.question.appendNotes', fallback: 'What should I add?' };
            case 'todo.replace_notes':
                return { key: 'voice.question.replaceNotes', fallback: 'What should the notes say?' };
            case 'todo.add_tag':
            case 'todo.remove_tag':
                return { key: 'voice.question.whichTag', fallback: 'Which tag?' };
        }
    })();
    return Object.freeze({
        kind: 'question',
        intent,
        pendingSlot,
        selection,
        ...(target ? { target: target.reference } : {}),
        interaction: Object.freeze({
            kind: 'question',
            response: 'free-text',
            message: Object.freeze(prompt),
        }),
    });
}
function failureFromEmptyCandidates() {
    return localizedCommandFailure('unknown_story', 'voice.errors.todoNotFound', 'Todo was not found in this project.');
}
export async function resolveVoiceSemanticIntent(intent, state, context, selection = {}) {
    if (intent.kind === 'create-todo') {
        if (intent.title === null) {
            return {
                ok: true,
                value: missingSlotQuestion(intent, { operation: 'todo.create', slot: 'title' }, selection),
            };
        }
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
    if (intent.kind === 'count-completed-todos') {
        if (!context.callTool) {
            return localizedCommandFailure('network', 'voice.errors.completedCountUnavailable', 'The completed todo count is unavailable.');
        }
        try {
            const response = await context.callTool('todos_countCompleted', {
                projectSlug: context.projectSlug,
                period: intent.period.kind,
                timezone: context.timezone ?? 'UTC',
            });
            if (!Number.isInteger(response?.count) || Number(response.count) < 0) {
                throw new Error('invalid completed count');
            }
            const count = Number(response.count);
            return {
                ok: true,
                value: information(count === 1 ? 'voice.info.completedThisWeekOne' : 'voice.info.completedThisWeek', count === 1
                    ? '1 story was completed this week.'
                    : '{count} stories were completed this week.', { count }),
            };
        }
        catch {
            return localizedCommandFailure('network', 'voice.errors.completedCountUnavailable', 'The completed todo count is unavailable.');
        }
    }
    if (intent.kind === 'move-todo') {
        if (intent.destination === null) {
            const pendingCandidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
            if (isCommandFailure(pendingCandidates))
                return pendingCandidates;
            if (pendingCandidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, pendingCandidates.value, selection) };
            }
            const target = pendingCandidates.value[0];
            return target
                ? {
                    ok: true,
                    value: missingSlotQuestion(intent, { operation: 'todo.move', slot: 'destination' }, selection, target),
                }
                : failureFromEmptyCandidates();
        }
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
        if (intent.assignee === null) {
            const pendingCandidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
            if (isCommandFailure(pendingCandidates))
                return pendingCandidates;
            if (pendingCandidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, pendingCandidates.value, selection) };
            }
            const target = pendingCandidates.value[0];
            return target
                ? {
                    ok: true,
                    value: missingSlotQuestion(intent, { operation: 'todo.assign', slot: 'assignee' }, selection, target),
                }
                : failureFromEmptyCandidates();
        }
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
    if (intent.kind === 'add-todo-tag' || intent.kind === 'remove-todo-tag') {
        if (intent.tag === null) {
            const pendingCandidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
            if (isCommandFailure(pendingCandidates))
                return pendingCandidates;
            if (pendingCandidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, pendingCandidates.value, selection) };
            }
            const target = pendingCandidates.value[0];
            return target
                ? {
                    ok: true,
                    value: missingSlotQuestion(intent, {
                        operation: intent.kind === 'add-todo-tag' ? 'todo.add_tag' : 'todo.remove_tag',
                        slot: 'tag',
                    }, selection, target),
                }
                : failureFromEmptyCandidates();
        }
        let tagName;
        if (selection.tag) {
            const selected = selectedTagName(selection.tag, context);
            if (isCommandFailure(selected))
                return selected;
            tagName = selected.value;
        }
        if (!tagName) {
            const matches = matchingTagNames(intent.tag, context.board);
            if (matches.length === 0) {
                return localizedCommandFailure('unknown_tag', 'voice.errors.tagNotFound', 'Tag was not found in this project.');
            }
            if (matches.length > 1) {
                return { ok: true, value: tagClarification(intent, matches, selection) };
            }
            tagName = matches[0];
        }
        const candidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
        if (isCommandFailure(candidates))
            return candidates;
        const fresh = await refreshTodoCandidates(candidates.value, context);
        if (isCommandFailure(fresh))
            return fresh;
        const adding = intent.kind === 'add-todo-tag';
        const classified = classifyTodoCandidates(fresh.value, (candidate) => {
            const present = todoHasTag(candidate.todo, tagName);
            return present === adding ? 'already-satisfied' : 'actionable';
        });
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
                value: information(adding ? 'voice.info.tagAlreadyPresent' : 'voice.info.tagAlreadyAbsent', adding
                    ? '{title} already has tag {tag}.'
                    : '{title} does not have tag {tag}.', { title: satisfied.todo.title, tag: tagName }, satisfied.reference),
            };
        }
        const target = actionable[0];
        const tags = adding
            ? tagsAfterAdd(target.todo, tagName)
            : tagsAfterRemove(target.todo, tagName);
        const resolved = buildResolvedCommand({
            intent: adding ? 'todos.add_tag' : 'todos.remove_tag',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, tags, tag: tagName },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    const candidates = await resolveTodoCandidates(effectiveTarget(intent, state), state, context, selection.todo);
    if (isCommandFailure(candidates))
        return candidates;
    if (intent.kind === 'append-todo-notes' || intent.kind === 'replace-todo-notes') {
        if (intent.notes === null) {
            if (candidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, candidates.value, selection) };
            }
            const target = candidates.value[0];
            return target
                ? {
                    ok: true,
                    value: missingSlotQuestion(intent, {
                        operation: intent.kind === 'append-todo-notes'
                            ? 'todo.append_notes'
                            : 'todo.replace_notes',
                        slot: 'notes',
                    }, selection, target),
                }
                : failureFromEmptyCandidates();
        }
        const fresh = await refreshTodoCandidates(candidates.value, context);
        if (isCommandFailure(fresh))
            return fresh;
        const appending = intent.kind === 'append-todo-notes';
        const classified = classifyTodoCandidates(fresh.value, (candidate) => !appending && (candidate.todo.body ?? '') === intent.notes
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
                value: information('voice.info.notesUnchanged', '{title} already has those notes.', { title: satisfied.todo.title }, satisfied.reference),
            };
        }
        const target = actionable[0];
        const body = appending
            ? appendedNotes(target.todo.body, intent.notes)
            : intent.notes;
        const resolved = buildResolvedCommand({
            intent: appending ? 'todos.append_notes' : 'todos.replace_notes',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, body, notes: intent.notes },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    if (intent.kind === 'unassign-todo') {
        let namedMember;
        if (intent.assignee && selection.member) {
            const member = await selectedMember(selection.member, context);
            if (isCommandFailure(member))
                return member;
            namedMember = member.value;
        }
        if (intent.assignee && !namedMember) {
            const matching = await resolveMemberCandidates(intent.assignee, context);
            if (matching.length === 0) {
                return localizedCommandFailure('unknown_user', 'voice.errors.assigneeNotFound', 'Assignee was not found in this project.');
            }
            if (matching.length > 1) {
                return { ok: true, value: memberClarification(intent, matching, selection) };
            }
            namedMember = matching[0];
        }
        const fresh = await refreshTodoCandidates(candidates.value, context);
        if (isCommandFailure(fresh))
            return fresh;
        const classified = classifyTodoCandidates(fresh.value, (candidate) => {
            if (namedMember) {
                return candidate.todo.assigneeUserId === namedMember.userId
                    ? 'actionable'
                    : 'already-satisfied';
            }
            return candidate.todo.assigneeUserId == null ? 'already-satisfied' : 'actionable';
        });
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
                value: information(namedMember ? 'voice.info.notAssignedToMember' : 'voice.info.alreadyUnassigned', namedMember
                    ? '{title} is not assigned to {member}.'
                    : '{title} is already unassigned.', {
                    title: satisfied.todo.title,
                    ...(namedMember ? { member: namedMember.name || namedMember.email } : {}),
                }, satisfied.reference),
            };
        }
        const target = actionable[0];
        const resolved = buildResolvedCommand({
            intent: 'todos.unassign',
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            entities: { localId: target.todo.localId, assigneeUserId: null },
        }, context, {
            danger: false,
            requiresConfirmation: true,
            storyTitle: target.todo.title,
        });
        return isCommandFailure(resolved)
            ? resolved
            : { ok: true, value: commandResolution(resolved.value, target.reference, selection) };
    }
    if (intent.kind === 'inspect-todo') {
        const fresh = await refreshTodoCandidates(candidates.value, context);
        if (isCommandFailure(fresh))
            return fresh;
        if (fresh.value.length > 1) {
            return { ok: true, value: todoClarification(intent, fresh.value, selection) };
        }
        const target = fresh.value[0];
        if (!target)
            return failureFromEmptyCandidates();
        switch (intent.aspect) {
            case 'summary':
                return {
                    ok: true,
                    value: information('voice.info.todoSummary', '#{localId} {title} is in {lane}.', { localId: target.todo.localId, title: target.todo.title, lane: target.lane.name }, target.reference),
                };
            case 'lane':
                return {
                    ok: true,
                    value: information('voice.info.todoLane', '{title} is in {lane}.', { title: target.todo.title, lane: target.lane.name }, target.reference),
                };
            case 'tags': {
                const tags = (target.todo.tags ?? []).join(', ');
                return {
                    ok: true,
                    value: information(tags ? 'voice.info.todoTags' : 'voice.info.todoTagsNone', tags ? '{title} has tags: {tags}.' : '{title} has no tags.', { title: target.todo.title, tags }, target.reference),
                };
            }
            case 'notes': {
                const notes = target.todo.body ?? '';
                return {
                    ok: true,
                    value: information(notes ? 'voice.info.todoNotes' : 'voice.info.todoNotesNone', notes ? 'Notes for {title}: {notes}' : '{title} has no notes.', { title: target.todo.title, notes }, target.reference),
                };
            }
            case 'assignee': {
                const assigneeId = target.todo.assigneeUserId;
                if (assigneeId == null) {
                    return {
                        ok: true,
                        value: information('voice.info.todoUnassigned', '{title} is unassigned.', { title: target.todo.title }, target.reference),
                    };
                }
                const members = await projectMembers(context, true);
                const assignee = members.find((member) => member.userId === assigneeId);
                const member = assignee?.name || assignee?.email;
                return {
                    ok: true,
                    value: information(member ? 'voice.info.todoAssignee' : 'voice.info.todoAssigneeUnavailable', member
                        ? '{title} is assigned to {member}.'
                        : '{title} is assigned to a member who is no longer available.', { title: target.todo.title, member: member ?? '' }, target.reference),
                };
            }
        }
    }
    if (intent.kind === 'update-todo-title') {
        if (intent.title === null) {
            if (candidates.value.length > 1) {
                return { ok: true, value: todoClarification(intent, candidates.value, selection) };
            }
            const target = candidates.value[0];
            return target
                ? {
                    ok: true,
                    value: missingSlotQuestion(intent, { operation: 'todo.update_title', slot: 'title' }, selection, target),
                }
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
