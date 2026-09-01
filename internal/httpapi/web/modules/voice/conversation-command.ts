import type { VoiceCommandOptions } from './command-resolution.js';
import type { VoiceConversationSession } from './conversation-session.js';
import type { VoiceTodoReference } from './conversation-state.js';
import { resolveVoiceSemanticCommand } from './semantic-command.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import {
  isCommandFailure,
  localizedCommandFailure,
  type CommandResult,
  type ResolvedCommand,
} from './schema.js';

export type ResolvedVoiceConversationCommand = Readonly<{
  command: ResolvedCommand;
  target: VoiceTodoReference | null;
}>;

/**
 * Compatibility adapter for the retained legacy VoiceFlow surface.
 * The enhanced agent consumes the richer semantic resolution directly.
 */
export async function resolveVoiceConversationCommand(
  intent: VoiceSemanticIntent,
  session: VoiceConversationSession,
  options: VoiceCommandOptions,
  signal?: AbortSignal,
  targetOverride?: VoiceTodoReference | null,
): Promise<CommandResult<ResolvedVoiceConversationCommand | null>> {
  const resolved = await resolveVoiceSemanticCommand(
    intent,
    session,
    options,
    signal,
    targetOverride
      ? {
          todo: {
            selectedLocalId: targetOverride.localId,
            allowedLocalIds: [targetOverride.localId],
          },
        }
      : {},
  );
  if (isCommandFailure(resolved)) {
    if (targetOverride) session.clearActiveTodo();
    return resolved;
  }
  switch (resolved.value.kind) {
    case 'command':
      return {
        ok: true,
        value: Object.freeze({
          command: resolved.value.command,
          target: resolved.value.target,
        }),
      };
    case 'question':
      if (
        !resolved.value.target
        || resolved.value.pendingSlot.operation !== 'todo.update_title'
        || resolved.value.pendingSlot.slot !== 'title'
        || resolved.value.intent.kind !== 'update-todo-title'
      ) {
        return localizedCommandFailure(
          'unsupported',
          'voice.errors.unsupportedCommand',
          'Unsupported command.',
        );
      }
      session.setActiveTodo(resolved.value.target);
      session.setPendingInteraction({
        kind: 'missing-slot',
        operation: resolved.value.pendingSlot.operation,
        slot: resolved.value.pendingSlot.slot,
        intent: resolved.value.intent,
        selection: resolved.value.selection,
        target: resolved.value.target,
      });
      session.setLastInteraction(resolved.value.interaction);
      return { ok: true, value: null };
    case 'information':
      session.clearPendingInteraction();
      session.setLastInteraction(resolved.value.interaction);
      return { ok: true, value: null };
    case 'clarification': {
      session.setPendingInteraction({
        kind: 'clarification',
        intent: resolved.value.intent,
        choices: resolved.value.choices,
        selection: resolved.value.selection,
      });
      session.setLastInteraction(resolved.value.interaction);
      const todoChoices = resolved.value.choices.filter((choice) => choice.kind === 'todo');
      return localizedCommandFailure(
        todoChoices.length > 0 ? 'ambiguous_story' : 'ambiguous_user',
        todoChoices.length > 0 ? 'voice.errors.todoAmbiguous' : 'voice.errors.assigneeAmbiguous',
        todoChoices.length > 0
          ? 'More than one todo matched. Choose one.'
          : 'Assignee matches more than one project member.',
        {},
        todoChoices.length > 0
          ? {
              candidates: todoChoices.map((choice) => ({
                localId: choice.reference.localId,
                title: choice.title,
              })),
            }
          : {},
      );
    }
  }
}
