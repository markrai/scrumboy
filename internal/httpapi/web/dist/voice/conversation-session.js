import { createEmptyVoiceConversationState, } from './conversation-state.js';
function sameProject(left, right) {
    return left?.projectId === right.projectId && left.projectSlug === right.projectSlug;
}
function sameTodo(left, right) {
    return left?.projectId === right.projectId
        && left.projectSlug === right.projectSlug
        && left.localId === right.localId;
}
function freezeProject(project) {
    return Object.freeze({
        projectId: project.projectId,
        projectSlug: project.projectSlug,
    });
}
function freezeTodo(todo) {
    return Object.freeze({
        kind: 'todo',
        projectId: todo.projectId,
        projectSlug: todo.projectSlug,
        localId: todo.localId,
    });
}
function freezeSemanticTodoReference(target) {
    return Object.freeze({ ...target });
}
function freezeSemanticMemberReference(member) {
    return Object.freeze({ ...member });
}
function freezeSemanticIntent(intent) {
    switch (intent.kind) {
        case 'create-todo':
            return Object.freeze({ ...intent });
        case 'open-todo':
        case 'delete-todo':
            return Object.freeze({ ...intent, target: freezeSemanticTodoReference(intent.target) });
        case 'move-todo':
            return Object.freeze({
                ...intent,
                target: freezeSemanticTodoReference(intent.target),
                destination: Object.freeze({ ...intent.destination }),
            });
        case 'assign-todo':
            return Object.freeze({
                ...intent,
                target: freezeSemanticTodoReference(intent.target),
                assignee: freezeSemanticMemberReference(intent.assignee),
            });
        case 'update-todo-title':
            return Object.freeze({ ...intent, target: freezeSemanticTodoReference(intent.target) });
    }
}
function freezeMessage(message) {
    const values = message.values ? Object.freeze({ ...message.values }) : undefined;
    return Object.freeze({
        key: message.key,
        fallback: message.fallback,
        ...(values ? { values } : {}),
    });
}
function freezeInteraction(interaction) {
    const message = freezeMessage(interaction.message);
    switch (interaction.kind) {
        case 'question':
            return Object.freeze({ ...interaction, message });
        case 'clarification':
            return Object.freeze({
                ...interaction,
                message,
                options: Object.freeze(interaction.options.map((option) => Object.freeze({ ...option }))),
            });
        case 'confirmation':
            return Object.freeze({
                ...interaction,
                message,
                confirmLabel: freezeMessage(interaction.confirmLabel),
            });
        case 'refusal':
        case 'error':
            return Object.freeze({ ...interaction, message });
        case 'unsupported-residue':
            return Object.freeze({
                ...interaction,
                message,
                residue: Object.freeze([...interaction.residue]),
            });
        case 'information':
        case 'success':
            return Object.freeze({ ...interaction, message });
    }
}
function freezePending(pending) {
    if (pending.kind === 'clarification') {
        return Object.freeze({
            kind: pending.kind,
            intent: freezeSemanticIntent(pending.intent),
            choices: Object.freeze(pending.choices.map(freezeClarificationChoice)),
            selection: freezeConversationSelection(pending.selection),
        });
    }
    return Object.freeze({
        kind: pending.kind,
        action: pending.action,
        slot: pending.slot,
        target: freezeTodo(pending.target),
    });
}
function freezeConversationSelection(selection) {
    return Object.freeze({
        ...(selection.todo
            ? {
                todo: Object.freeze({
                    selectedLocalId: selection.todo.selectedLocalId,
                    allowedLocalIds: Object.freeze([...selection.todo.allowedLocalIds]),
                }),
            }
            : {}),
        ...(selection.member
            ? {
                member: Object.freeze({
                    selectedUserId: selection.member.selectedUserId,
                    allowedUserIds: Object.freeze([...selection.member.allowedUserIds]),
                }),
            }
            : {}),
    });
}
function freezeClarificationChoice(choice) {
    if (choice.kind === 'member')
        return Object.freeze({ ...choice });
    return Object.freeze({
        ...choice,
        reference: freezeTodo(choice.reference),
    });
}
function freezeState(state) {
    return Object.freeze({ ...state });
}
export function createVoiceConversationSession() {
    let state = createEmptyVoiceConversationState();
    let disposed = false;
    const requireActive = () => {
        if (disposed)
            throw new Error('Voice conversation session is disposed.');
    };
    const session = {
        getState() {
            return state;
        },
        setActiveProject(project) {
            requireActive();
            const projectChanged = !sameProject(state.activeProject, project);
            state = freezeState({
                ...state,
                activeProject: freezeProject(project),
                activeTodo: projectChanged ? null : state.activeTodo,
                pending: projectChanged ? null : state.pending,
            });
        },
        setActiveTodo(todo) {
            requireActive();
            const todoChanged = !sameTodo(state.activeTodo, todo);
            const activeTodo = freezeTodo(todo);
            state = freezeState({
                ...state,
                activeProject: freezeProject(todo),
                activeTodo,
                pending: todoChanged ? null : state.pending,
            });
        },
        clearActiveTodo() {
            requireActive();
            state = freezeState({
                ...state,
                activeTodo: null,
                pending: null,
            });
        },
        setPendingInteraction(pending) {
            requireActive();
            state = freezeState({ ...state, pending: freezePending(pending) });
        },
        clearPendingInteraction() {
            requireActive();
            state = freezeState({ ...state, pending: null });
        },
        setLastInteraction(interaction) {
            requireActive();
            state = freezeState({
                ...state,
                lastInteraction: freezeInteraction(interaction),
            });
        },
        setContinuationEnabled(enabled) {
            requireActive();
            state = freezeState({ ...state, continuationEnabled: enabled });
        },
        reset() {
            requireActive();
            state = createEmptyVoiceConversationState();
        },
        dispose() {
            if (disposed)
                return;
            state = createEmptyVoiceConversationState();
            disposed = true;
        },
    };
    return Object.freeze(session);
}
