import { registerPlugin, type PluginListenerHandle } from '@capacitor/core';

export const NATIVE_VOICE_AGENT_EVAL_EVENT = 'voiceAgentEvaluationRequest' as const;
export const VOICE_AGENT_EVAL_CORPUS = '__corpus__' as const;

type NativeEvalRequest = Readonly<{
  requestId: string;
  transcript: string;
  timeoutMs?: number;
}>;

interface NativeVoiceFlowPlugin {
  completeAgentEval(options: { requestId: string; resultJson: string }): Promise<void>;
  addListener(
    eventName: typeof NATIVE_VOICE_AGENT_EVAL_EVENT,
    listener: (event: NativeEvalRequest) => void,
  ): Promise<PluginListenerHandle>;
}

type DeviceEvaluationModule = Readonly<{
  evaluateVoiceAgentOnCurrentBoard(
    transcript: string,
    options?: Readonly<{ timeoutMs?: number }>,
  ): Promise<VoiceAgentEvalResult>;
  evaluateVoiceAgentCorpusOnCurrentBoard(
    options?: Readonly<{ timeoutMs?: number }>,
  ): Promise<VoiceAgentEvalResult>;
}>;

type ModuleImporter = (path: string) => Promise<unknown>;

type VoiceAgentEvalResult = Readonly<{
  version: 1;
  mutationExecuted: false;
  providerUsed: boolean;
  transcript?: string;
  caseCount?: number;
  rawMatched?: number;
  applicationMatched?: number;
}>;

const plugin = registerPlugin<NativeVoiceFlowPlugin>('ScrumboyVoiceFlow');

function bridgeFailure(input: string): VoiceAgentEvalResult {
  return Object.freeze({
    version: 1,
    mutationExecuted: false,
    providerUsed: false,
    transcript: input,
    caseCount: 0,
    rawMatched: 0,
    applicationMatched: 0,
  });
}

function validRequest(request: NativeEvalRequest): boolean {
  return !!request
    && typeof request.requestId === 'string'
    && /^[A-Za-z0-9_-]{1,64}$/.test(request.requestId)
    && typeof request.transcript === 'string'
    && request.transcript.length <= 8_192
    && (request.timeoutMs === undefined || (Number.isInteger(request.timeoutMs) && request.timeoutMs > 0 && request.timeoutMs <= 600_000));
}

function validResult(result: unknown): result is VoiceAgentEvalResult {
  if (!result || typeof result !== 'object') return false;
  const value = result as Partial<VoiceAgentEvalResult>;
  if (value.version !== 1 || value.mutationExecuted !== false || typeof value.providerUsed !== 'boolean') return false;
  if (typeof value.transcript === 'string') return true;
  return Number.isInteger(value.caseCount)
    && Number(value.caseCount) >= 0
    && Number.isInteger(value.rawMatched)
    && Number.isInteger(value.applicationMatched);
}

/** Native requests are deliberately serialized to avoid concurrent Nano generations. */
export async function installVoiceAgentEvaluationBridge(
  importer: ModuleImporter = path => import(path),
  nativePlugin: NativeVoiceFlowPlugin = plugin,
): Promise<PluginListenerHandle> {
  let serial = Promise.resolve();
  return nativePlugin.addListener(NATIVE_VOICE_AGENT_EVAL_EVENT, request => {
    if (!validRequest(request)) return;
    serial = serial.then(async () => {
      let result: VoiceAgentEvalResult;
      try {
        const evaluator = await importer('/dist/voice/agent-device-evaluation.js') as DeviceEvaluationModule;
        const evaluated = request.transcript === VOICE_AGENT_EVAL_CORPUS
          ? await evaluator.evaluateVoiceAgentCorpusOnCurrentBoard({ timeoutMs: request.timeoutMs })
          : await evaluator.evaluateVoiceAgentOnCurrentBoard(request.transcript, { timeoutMs: request.timeoutMs });
        result = validResult(evaluated) ? evaluated : bridgeFailure(request.transcript);
      } catch {
        result = bridgeFailure(request.transcript);
      }
      await nativePlugin.completeAgentEval({ requestId: request.requestId, resultJson: JSON.stringify(result) });
    }).catch(() => undefined);
  });
}
