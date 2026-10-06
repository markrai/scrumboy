export function createEmptyVoiceConversationState() {
    return Object.freeze({
        activeProject: null,
        activeTodo: null,
        pending: null,
        lastInteraction: null,
        continuationEnabled: false,
    });
}
