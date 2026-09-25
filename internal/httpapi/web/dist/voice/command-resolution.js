import { getActiveVoiceCommandContext, canRunVoiceMutationInContext } from './command-context.js';
export { getActiveVoiceCommandContext, canRunVoiceMutationInContext } from './command-context.js';
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
export function canRunResolvedVoiceCommand(context, command) {
    if (!isVoiceMutationCommand(command))
        return true;
    return canRunVoiceMutationInContext(context);
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
