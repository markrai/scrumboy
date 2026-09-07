import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
import { localizedCommandFailure } from './schema.js';
export function getActiveVoiceCommandContext(options) {
    const context = options.getContext();
    if (!context
        || context.userId !== options.initialUserId
        || context.projectId !== options.initialProjectId
        || context.projectSlug !== options.initialProjectSlug) {
        return localizedCommandFailure('stale_context', 'voice.errors.staleContext', 'The board changed before the command could run.');
    }
    const allowed = canShowVoiceCommands({
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        role: context.role,
        isTemporary: isTemporaryBoard(context.board),
        isAnonymous: isAnonymousBoard(context.board),
    });
    if (!allowed) {
        return localizedCommandFailure('stale_context', 'voice.errors.commandsUnavailable', 'Commands are unavailable for this board.');
    }
    return { ok: true, value: context };
}
export function canRunVoiceMutationInContext(context) {
    return canRunVoiceMutationCommands({
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        role: context.role,
        isTemporary: isTemporaryBoard(context.board),
        isAnonymous: isAnonymousBoard(context.board),
    });
}
