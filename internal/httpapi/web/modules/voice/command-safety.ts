import type { CommandIR } from './schema.js';

export type VoiceCommandSafetyReason = 'destructive_delete' | 'non_delete_command';

/** The existing policy: only deleting a todo sets danger. Confirmation is separate. */
export function classifyVoiceCommandSafety(ir: Pick<CommandIR, 'intent'>): Readonly<{
  danger: boolean;
  reason: VoiceCommandSafetyReason;
}> {
  return ir.intent === 'todos.delete'
    ? { danger: true, reason: 'destructive_delete' }
    : { danger: false, reason: 'non_delete_command' };
}

export function classifyVoiceCommandBatchSafety(commands: readonly Pick<CommandIR, 'intent'>[]) {
  return commands.map(classifyVoiceCommandSafety).find(value => value.danger)
    ?? { danger: false, reason: 'non_delete_command' as const };
}
