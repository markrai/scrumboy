import type { BoardMember } from '../state/state.js';
import type { Board } from '../types.js';
import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
import { localizedCommandFailure, type CommandResult } from './schema.js';
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

export type VoiceCommandContextIdentity = Pick<
  VoiceCommandOptions,
  'initialUserId' | 'initialProjectId' | 'initialProjectSlug'
>;

/** Shared identity/policy check for interactive commands and read-only evaluation. */
export function getVoiceCommandContextForIdentity(
  identity: VoiceCommandContextIdentity,
  getContext: () => VoiceCommandContext | null,
): CommandResult<VoiceCommandContext> {
  const context = getContext();
  if (
    !context
    || context.userId !== identity.initialUserId
    || context.projectId !== identity.initialProjectId
    || context.projectSlug !== identity.initialProjectSlug
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

export function getActiveVoiceCommandContext(
  options: VoiceCommandOptions,
): CommandResult<VoiceCommandContext> {
  return getVoiceCommandContextForIdentity(options, options.getContext);
}

export function canRunVoiceMutationInContext(context: VoiceCommandContext): boolean {
  return canRunVoiceMutationCommands({
    projectId: context.projectId,
    projectSlug: context.projectSlug,
    role: context.role,
    isTemporary: isTemporaryBoard(context.board),
    isAnonymous: isAnonymousBoard(context.board),
  });
}

