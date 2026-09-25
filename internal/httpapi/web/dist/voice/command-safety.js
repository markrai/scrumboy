/** The existing policy: only deleting a todo sets danger. Confirmation is separate. */
export function classifyVoiceCommandSafety(ir) {
    return ir.intent === 'todos.delete'
        ? { danger: true, reason: 'destructive_delete' }
        : { danger: false, reason: 'non_delete_command' };
}
export function classifyVoiceCommandBatchSafety(commands) {
    return commands.map(classifyVoiceCommandSafety).find(value => value.danger)
        ?? { danger: false, reason: 'non_delete_command' };
}
