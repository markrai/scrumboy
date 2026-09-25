export function voiceSemanticOperation(intent) {
    switch (intent.kind) {
        case 'create-todo': return 'todo.create';
        case 'open-todo': return 'todo.open';
        case 'move-todo': return 'todo.move';
        case 'assign-todo': return 'todo.assign';
        case 'unassign-todo': return 'todo.unassign';
        case 'delete-todo': return 'todo.delete';
        case 'update-todo-title': return 'todo.update_title';
        case 'append-todo-notes': return 'todo.append_notes';
        case 'replace-todo-notes': return 'todo.replace_notes';
        case 'add-todo-tag': return 'todo.add_tag';
        case 'remove-todo-tag': return 'todo.remove_tag';
        case 'inspect-todo': return 'todo.inspect';
        case 'count-completed-todos': return 'todo.count_completed';
    }
}
export function isVoiceDialogueIntent(intent) {
    return [
        'confirm',
        'decline',
        'cancel',
        'select-choice',
        'provide-slot',
        'correct-choice',
        'correct-value',
    ].includes(intent.kind);
}
