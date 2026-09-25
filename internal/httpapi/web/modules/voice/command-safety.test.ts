import { describe, expect, it } from 'vitest';
import { classifyVoiceCommandSafety, classifyVoiceCommandBatchSafety } from './command-safety.js';
import type { CommandIntent } from './schema.js';

describe('VoiceFlow safety policy', () => {
  it('marks a batch dangerous exactly when it contains a delete', () => {
    expect(classifyVoiceCommandBatchSafety([{ intent: 'todos.move' }, { intent: 'todos.delete' }]))
      .toEqual({ danger: true, reason: 'destructive_delete' });
    expect(classifyVoiceCommandBatchSafety([{ intent: 'todos.move' }, { intent: 'todos.replace_notes' }]))
      .toEqual({ danger: false, reason: 'non_delete_command' });
  });
  it('identifies the destructive delete rule', () => {
    expect(classifyVoiceCommandSafety({ intent: 'todos.delete' })).toEqual({
      danger: true, reason: 'destructive_delete',
    });
  });
  it.each<CommandIntent>([
    'todos.create', 'todos.move', 'todos.assign', 'todos.unassign', 'todos.update_title',
    'todos.append_notes', 'todos.replace_notes', 'todos.add_tag', 'todos.remove_tag', 'open_todo',
  ])('keeps %s non-dangerous', (intent) => {
    expect(classifyVoiceCommandSafety({ intent })).toEqual({ danger: false, reason: 'non_delete_command' });
  });
});
