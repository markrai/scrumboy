import { LOCAL_TEXT_GENERATION_CAPABILITY } from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { getVoiceCreateDryRunBoardPorts } from '../views/board.js';
import { createVoiceCreateDryRunSession, evaluateVoiceCreateDryRun } from './voice-create-evaluation.js';
import { createVoiceCreatePlanner } from './voice-create-planner.js';
async function evaluateOnCurrentBoard(transcript, options = {}) {
    const capability = getAppRuntime().capability(LOCAL_TEXT_GENERATION_CAPABILITY);
    const unavailableProvider = {
        status: async () => ({ state: 'unsupported', reason: 'provider' }),
    };
    const provider = capability ?? unavailableProvider;
    return evaluateVoiceCreateDryRun(transcript, {
        planner: capability
            ? createVoiceCreatePlanner(capability, { includeDryRunParserOutputPreview: true })
            : async () => { throw Object.assign(new Error('provider_unavailable'), { code: 'unsupported' }); },
        provider,
        ...getVoiceCreateDryRunBoardPorts(),
        timeoutMs: options.timeoutMs,
    });
}
/** Lazily loaded only after a native debug request; never part of board activation. */
export const evaluateVoiceCreateDryRunOnCurrentBoard = createVoiceCreateDryRunSession(evaluateOnCurrentBoard);
