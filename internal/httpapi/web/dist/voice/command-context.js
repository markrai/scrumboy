import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
import { localizedCommandFailure } from './schema.js';
/** Shared identity/policy check for interactive commands and read-only evaluation. */
export function getVoiceCommandContextForIdentity(identity, getContext) {
    const context = getContext();
    if (!context
        || context.userId !== identity.initialUserId
        || context.projectId !== identity.initialProjectId
        || context.projectSlug !== identity.initialProjectSlug) {
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
export function getActiveVoiceCommandContext(options) {
    return getVoiceCommandContextForIdentity(options, options.getContext);
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
