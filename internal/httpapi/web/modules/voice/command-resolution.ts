import type { BoardMember } from '../state/state.js';
import type { Board } from '../types.js';
import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
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

export type VoiceCommandContext = {
  userId: number;
  projectId: number;
  projectSlug: string;
  board: Board;
  members: BoardMember[];
  role: string | null;
};

export type VoiceCommandOptions = {
  initialUserId: number;
  initialProjectId: number;
  initialProjectSlug: string;
  getContext: () => VoiceCommandContext | null;
  refreshBoard: () => Promise<void>;
  openTodo: (localId: number) => Promise<void>;
  recordMutation?: () => void;
  showMessage?: (message: string) => void;
};

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
    case 'todos.update_title':
      return true;
    case 'open_todo':
      return false;
  }
}

export function getActiveVoiceCommandContext(
  options: VoiceCommandOptions,
): CommandResult<VoiceCommandContext> {
  const context = options.getContext();
  if (
    !context
    || context.userId !== options.initialUserId
    || context.projectId !== options.initialProjectId
    || context.projectSlug !== options.initialProjectSlug
  ) {
    return localizedCommandFailure(
      'stale_context',
      'voice.errors.staleContext',
      'The board changed before the command could run.',
    );
  }
  const allowed = canShowVoiceCommands({
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    role: context.role,
    isTemporary: isTemporaryBoard(context.board),
    isAnonymous: isAnonymousBoard(context.board),
  });
  if (!allowed) {
    return localizedCommandFailure(
      'stale_context',
      'voice.errors.commandsUnavailable',
      'Commands are unavailable for this board.',
    );
  }
  return { ok: true, value: context };
}

export function canRunResolvedVoiceCommand(
  context: VoiceCommandContext,
  command: ResolvedCommand,
): boolean {
  if (!isVoiceMutationCommand(command)) return true;
  return canRunVoiceMutationCommands({
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    role: context.role,
    isTemporary: isTemporaryBoard(context.board),
    isAnonymous: isAnonymousBoard(context.board),
  });
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
