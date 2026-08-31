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
        case 'clarification':
            return Object.freeze({ ...interaction, message });
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
    return Object.freeze({
        kind: pending.kind,
        action: pending.action,
        slot: pending.slot,
        target: freezeTodo(pending.target),
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
