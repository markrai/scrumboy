import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { canRunVoiceMutationCommands, canShowVoiceCommands } from '../views/board-command-capabilities.js';
import { callMcpTool } from './mcp-client.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import { isCommandFailure, localizedCommandFailure, } from './schema.js';
export function voiceCommandHash(command) {
    return JSON.stringify(command.ir);
}
export function isVoiceMutationCommand(command) {
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
export function canRunResolvedVoiceCommand(context, command) {
    if (!isVoiceMutationCommand(command))
        return true;
    return canRunVoiceMutationInContext(context);
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
export async function resolveParsedVoiceDraft(draft, context, signal, targetSelection = {}) {
    return resolveCommandDraft(draft, {
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        board: context.board,
        members: context.members,
        callTool: (tool, input) => callMcpTool(tool, input, { signal }),
    }, targetSelection);
}
export async function parseAndResolveVoiceCommand(transcript, options, signal, targetSelection = {}) {
    const context = getActiveVoiceCommandContext(options);
    if (isCommandFailure(context))
        return context;
    const parsed = parseCommand(transcript);
    if (isCommandFailure(parsed))
        return parsed;
    const resolved = await resolveParsedVoiceDraft(parsed.value, context.value, signal, targetSelection);
    if (isCommandFailure(resolved))
        return resolved;
    if (!canRunResolvedVoiceCommand(context.value, resolved.value)) {
        return localizedCommandFailure('unauthorized', 'voice.errors.unauthorizedMutation', 'Only maintainers can run mutating commands.');
    }
    return resolved;
}
