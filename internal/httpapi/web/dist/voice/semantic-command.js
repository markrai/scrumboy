import { canRunVoiceMutationInContext, getActiveVoiceCommandContext, } from './command-resolution.js';
import { isVoiceSemanticMutationIntent } from './semantic-intent.js';
import { callMcpTool } from './mcp-client.js';
import { resolveVoiceSemanticIntent, } from './semantic-resolver.js';
import { isCommandFailure, localizedCommandFailure, } from './schema.js';
export async function resolveVoiceSemanticCommand(intent, session, options, signal, selection = {}) {
    const context = getActiveVoiceCommandContext(options);
    if (isCommandFailure(context)) {
        session.clearActiveTodo();
        return context;
    }
    if (isVoiceSemanticMutationIntent(intent)
        && !canRunVoiceMutationInContext(context.value)) {
        return localizedCommandFailure('unauthorized', 'voice.errors.unauthorizedMutation', 'Only maintainers can run mutating commands.');
    }
    const resolved = await resolveVoiceSemanticIntent(intent, session.getState(), {
        projectId: context.value.projectId,
        projectSlug: context.value.projectSlug,
        board: context.value.board,
        members: context.value.members,
        callTool: (tool, input) => callMcpTool(tool, input, { signal }),
    }, selection);
    if (isCommandFailure(resolved) && resolved.code === 'stale_context') {
        session.clearActiveTodo();
    }
    return resolved;
}
