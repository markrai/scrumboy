import { callMcpTool } from './mcp-client.js';
import { VoiceCreatePlanError } from './voice-create-plan.js';
function isVoiceCreateMember(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value))
        return false;
    const member = value;
    return Number.isInteger(member.userId)
        && Number(member.userId) > 0
        && typeof member.name === 'string'
        && typeof member.email === 'string'
        && typeof member.role === 'string';
}
/** Shared production/dry-run adapter for the richer MCP member projection. */
export async function readVoiceCreateMembers(projectSlug, signal, callTool = callMcpTool) {
    const result = await callTool('members_list', { projectSlug }, { signal });
    if (!Array.isArray(result.items) || !result.items.every(isVoiceCreateMember)) {
        throw new VoiceCreatePlanError('network');
    }
    return Object.freeze(result.items.map(member => Object.freeze({
        userId: member.userId,
        name: member.name,
        email: member.email,
        role: member.role,
    })));
}
