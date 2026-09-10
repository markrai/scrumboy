import type { BoardMember } from '../state/state.js';
import { callMcpTool } from './mcp-client.js';
import { VoiceCreatePlanError } from './voice-create-plan.js';

/** The authoritative member shape required by Create v2 resolution. */
export type VoiceCreateMember = Readonly<{
  userId: BoardMember['userId'];
  name: BoardMember['name'];
  email: string;
  role: BoardMember['role'];
}>;

function isVoiceCreateMember(value: unknown): value is VoiceCreateMember {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const member = value as Record<string, unknown>;
  return Number.isInteger(member.userId)
    && Number(member.userId) > 0
    && typeof member.name === 'string'
    && typeof member.email === 'string'
    && typeof member.role === 'string';
}

/** Shared production/dry-run adapter for the richer MCP member projection. */
export async function readVoiceCreateMembers(
  projectSlug: string,
  signal: AbortSignal,
  callTool: typeof callMcpTool = callMcpTool,
): Promise<readonly VoiceCreateMember[]> {
  const result = await callTool<{ items?: unknown[] }>('members_list', { projectSlug }, { signal });
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
