import { getActiveVoiceCommandContext, canRunVoiceMutationInContext, type VoiceCommandContext, type VoiceCommandOptions } from './command-context.js';
export { getActiveVoiceCommandContext, canRunVoiceMutationInContext, type VoiceCommandContext, type VoiceCommandOptions } from './command-context.js';
import { callMcpTool } from './mcp-client.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import {
  isCommandFailure,
  localizedCommandFailure,
  type CommandResult,
  type ParsedCommandDraft,
  type ResolvedCommand,
} from './schema.js';

export type VoiceTargetSelection = {
  selectedLocalId?: number;
  allowedLocalIds?: number[];
};

export function voiceCommandHash(command: ResolvedCommand): string {
  return JSON.stringify(command.ir);
}

export function isVoiceMutationCommand(command: ResolvedCommand): boolean {
  switch (command.ir.intent) {
    case 'todos.create':
    case 'todos.move':
    case 'todos.delete':
    case 'todos.assign':
    case 'todos.unassign':
    case 'todos.update_title':
    case 'todos.append_notes':
    case 'todos.replace_notes':
    case 'todos.add_tag':
    case 'todos.remove_tag':
      return true;
    case 'open_todo':
      return false;
  }
}

export function canRunResolvedVoiceCommand(
  context: VoiceCommandContext,
  command: ResolvedCommand,
): boolean {
  if (!isVoiceMutationCommand(command)) return true;
  return canRunVoiceMutationInContext(context);
}

export async function resolveParsedVoiceDraft(
  draft: ParsedCommandDraft,
  context: VoiceCommandContext,
  signal?: AbortSignal,
  targetSelection: VoiceTargetSelection = {},
): Promise<CommandResult<ResolvedCommand>> {
  return resolveCommandDraft(draft, {
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    board: context.board,
    members: context.members,
    callTool: (tool, input) => callMcpTool(tool, input, { signal }),
  }, targetSelection);
}

export async function parseAndResolveVoiceCommand(
  transcript: string,
  options: VoiceCommandOptions,
  signal?: AbortSignal,
  targetSelection: VoiceTargetSelection = {},
): Promise<CommandResult<ResolvedCommand>> {
  const context = getActiveVoiceCommandContext(options);
  if (isCommandFailure(context)) return context;
  const parsed = parseCommand(transcript);
  if (isCommandFailure(parsed)) return parsed;
  const resolved = await resolveParsedVoiceDraft(parsed.value, context.value, signal, targetSelection);
  if (isCommandFailure(resolved)) return resolved;
  if (!canRunResolvedVoiceCommand(context.value, resolved.value)) {
    return localizedCommandFailure(
      'unauthorized',
      'voice.errors.unauthorizedMutation',
      'Only maintainers can run mutating commands.',
    );
  }
  return resolved;
}
