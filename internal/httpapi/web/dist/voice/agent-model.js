import { LOCAL_TEXT_GENERATION_LIMITS } from '../platform/local-text-generation.js';
import { AgentProtocolError } from './agent-protocol.js';
import { VOICE_AGENT_PROMPT, VOICE_AGENT_PROMPT_VERSION } from './agent-prompt.js';
let request = 0;
export function createVoiceAgentModel(capability, locale) {
    return async (input, signal) => {
        if (signal.aborted || input.length > LOCAL_TEXT_GENERATION_LIMITS.inputCodeUnits)
            throw new AgentProtocolError('Task input limit');
        const requestId = `${VOICE_AGENT_PROMPT_VERSION}-${++request}`;
        const currentLocale = typeof locale === 'function' ? locale() : locale;
        const result = await capability.generate({ requestId, input, instructions: `${VOICE_AGENT_PROMPT}\nUI locale: ${currentLocale.slice(0, 64)}.`, maximumOutputTokens: 256, signal });
        if (signal.aborted || result.requestId !== requestId)
            throw new AgentProtocolError('Stale model response');
        return result.text;
    };
}
