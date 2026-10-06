export function isVoiceSemanticMutationIntent(intent) {
    return intent.kind !== 'open-todo'
        && intent.kind !== 'inspect-todo'
        && intent.kind !== 'count-completed-todos';
}
