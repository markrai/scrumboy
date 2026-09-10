import { LOCAL_TEXT_GENERATION_CAPABILITY, type LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { getVoiceCreateDryRunBoardPorts } from '../views/board.js';
import { createVoiceCreateDryRunSession, evaluateVoiceCreateDryRun, type VoiceCreateDryRunResultV1 } from './voice-create-evaluation.js';
import { createVoiceCreatePlanner } from './voice-create-planner.js';

async function evaluateOnCurrentBoard(
  transcript: string,
  options: Readonly<{ timeoutMs?: number }> = {},
): Promise<VoiceCreateDryRunResultV1> {
  const capability = getAppRuntime().capability(LOCAL_TEXT_GENERATION_CAPABILITY);
  const unavailableProvider: Pick<LocalTextGenerationCapability, 'status'> = {
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
