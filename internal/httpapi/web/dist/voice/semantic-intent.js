export function isVoiceSemanticMutationIntent(intent) {
    return intent.kind !== 'open-todo';
}
