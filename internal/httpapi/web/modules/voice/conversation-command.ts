import {
  canRunResolvedVoiceCommand,
  getActiveVoiceCommandContext,
  resolveParsedVoiceDraft,
  type VoiceCommandOptions,
} from './command-resolution.js';
import { resolveConversationTodoTarget } from './conversation-resolve.js';
import type { VoiceConversationSession } from './conversation-session.js';
import type { VoiceTodoReference } from './conversation-state.js';
import type { VoiceConversationIntent } from './interpreter.js';
import { callMcpTool } from './mcp-client.js';
import { resolveTodoTitleUpdate } from './resolve.js';
import {
  isCommandFailure,
  localizedCommandFailure,
  type CommandResult,
  type ResolvedCommand,
} from './schema.js';

export type ResolvedVoiceConversationCommand = Readonly<{
  command: ResolvedCommand;
  target: VoiceTodoReference;
}>;

export async function resolveVoiceConversationCommand(
  intent: VoiceConversationIntent,
  session: VoiceConversationSession,
  options: VoiceCommandOptions,
  signal?: AbortSignal,
  targetOverride?: VoiceTodoReference | null,
): Promise<CommandResult<ResolvedVoiceConversationCommand | null>> {
  const context = getActiveVoiceCommandContext(options);
  if (isCommandFailure(context)) {
    session.clearActiveTodo();
    return context;
  }

  const pending = session.getState().pending;
  const targetInput = targetOverride
    ?? (intent.kind === 'update-todo-title' && pending ? pending.target : intent.target);
  const target = resolveConversationTodoTarget(
    targetInput,
    session.getState(),
    {
      projectId: context.value.projectId,
      projectSlug: context.value.projectSlug,
      board: context.value.board,
    },
  );
  if (target.ok === false) {
    if (target.code === 'project_mismatch' || target.code === 'todo_missing') {
      session.clearActiveTodo();
    }
    return target.code === 'no_active_todo'
      ? localizedCommandFailure(
          'unknown_story',
          'voice.errors.todoReferenceRequired',
          'Todo reference is required.',
        )
      : localizedCommandFailure(
          'stale_context',
          'voice.errors.staleContext',
          'The board changed before the command could run.',
        );
  }

  const localId = target.value.reference.localId;
  if (intent.kind === 'update-todo-title' && intent.title === null) {
    session.setActiveTodo(target.value.reference);
    session.setPendingInteraction({
      kind: 'missing-slot',
      action: 'todo.update_title',
      slot: 'title',
      target: target.value.reference,
    });
    session.setLastInteraction({
      kind: 'question',
      response: 'free-text',
      message: {
        key: 'voice.question.updateTitle',
        fallback: 'What would you like to change the title to?',
      },
    });
    return { ok: true, value: null };
  }

  const resolved = intent.kind === 'update-todo-title'
    ? await resolveTodoTitleUpdate(localId, intent.title, {
        projectId: context.value.projectId,
        projectSlug: context.value.projectSlug,
        board: context.value.board,
        members: context.value.members,
        callTool: (tool, input) => callMcpTool(tool, input, { signal }),
      })
    : await resolveParsedVoiceDraft({
        intent: 'open_todo',
        target: {
          kind: 'id',
          localId,
          display: String(localId),
        },
        display: `open todo ${localId}`,
      }, context.value, signal);
  if (isCommandFailure(resolved)) return resolved;
  if (!canRunResolvedVoiceCommand(context.value, resolved.value)) {
    return localizedCommandFailure(
      'unauthorized',
      'voice.errors.unauthorizedMutation',
      'Only maintainers can run mutating commands.',
    );
  }
  return {
    ok: true,
    value: Object.freeze({
      command: resolved.value,
      target: target.value.reference,
    }),
  };
}
