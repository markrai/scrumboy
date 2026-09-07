import { LOCAL_TEXT_GENERATION_LIMITS, type LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { AgentProtocolError } from './agent-protocol.js';
import { VOICE_AGENT_PROMPT, VOICE_AGENT_PROMPT_VERSION } from './agent-prompt.js';
export type VoiceAgentModel = (input: string, signal: AbortSignal) => Promise<string>;
let request = 0;
export function createVoiceAgentModel(capability: LocalTextGenerationCapability, locale: string): VoiceAgentModel {
  return async (input, signal) => {
    if (signal.aborted || input.length > LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits) throw new AgentProtocolError('Task input limit');
    const requestId = `${VOICE_AGENT_PROMPT_VERSION}-${++request}`;
    const result = await capability.generate({ requestId, input, instructions: `${VOICE_AGENT_PROMPT}\nUI locale: ${locale.slice(0, 64)}.`, maximumOutputTokens: 256, signal });
    if (signal.aborted || result.requestId !== requestId) throw new AgentProtocolError('Stale model response');
    return result.text;
  };
}
