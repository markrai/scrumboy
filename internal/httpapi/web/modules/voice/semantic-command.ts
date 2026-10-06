import {
  canRunVoiceMutationInContext,
  getActiveVoiceCommandContext,
  type VoiceCommandOptions,
} from './command-resolution.js';
import type { VoiceConversationSession } from './conversation-session.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import { isVoiceSemanticMutationIntent } from './semantic-intent.js';
import { callMcpTool } from './mcp-client.js';
import {
  resolveVoiceSemanticIntent,
  type VoiceSemanticResolution,
  type VoiceSemanticSelection,
} from './semantic-resolver.js';
import {
  isCommandFailure,
  localizedCommandFailure,
  type CommandResult,
} from './schema.js';

function currentTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

export async function resolveVoiceSemanticCommand(
  intent: VoiceSemanticIntent,
  session: VoiceConversationSession,
  options: VoiceCommandOptions,
  signal?: AbortSignal,
  selection: VoiceSemanticSelection = {},
): Promise<CommandResult<VoiceSemanticResolution>> {
  const context = getActiveVoiceCommandContext(options);
  if (isCommandFailure(context)) {
    session.clearActiveTodo();
    return context;
  }
  if (
    isVoiceSemanticMutationIntent(intent)
    && !canRunVoiceMutationInContext(context.value)
  ) {
    return localizedCommandFailure(
      'unauthorized',
      'voice.errors.unauthorizedMutation',
      'Only maintainers can run mutating commands.',
    );
  }

  const resolved = await resolveVoiceSemanticIntent(
    intent,
    session.getState(),
    {
      projectId: context.value.projectId,
      projectSlug: context.value.projectSlug,
      board: context.value.board,
      members: context.value.members,
      timezone: currentTimezone(),
      callTool: (tool, input) => callMcpTool(tool, input, { signal }),
    },
    selection,
  );
  if (isCommandFailure(resolved) && resolved.code === 'stale_context') {
    session.clearActiveTodo();
  }
  return resolved;
}
