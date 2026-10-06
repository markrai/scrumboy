import { registerPlugin, type PluginListenerHandle } from '@capacitor/core';

export const NATIVE_VOICE_CREATE_DRY_RUN_EVENT = 'voiceCreateDryRunRequest' as const;

type NativeDryRunRequest = Readonly<{
  requestId: string;
  transcript: string;
  timeoutMs?: number;
}>;

interface NativeVoiceFlowPlugin {
  completeDryRun(options: { requestId: string; resultJson: string }): Promise<void>;
  addListener(
    eventName: typeof NATIVE_VOICE_CREATE_DRY_RUN_EVENT,
    listener: (event: NativeDryRunRequest) => void,
  ): Promise<PluginListenerHandle>;
}

type DeviceEvaluationModule = Readonly<{
  evaluateVoiceCreateDryRunOnCurrentBoard(
    transcript: string,
    options?: Readonly<{ timeoutMs?: number }>,
  ): Promise<VoiceCreateDryRunResultV1>;
}>;

type ModuleImporter = (path: string) => Promise<unknown>;

type VoiceCreateDryRunResultV1 = Readonly<{
  version: 1;
  input: string;
  outcome: string;
  planner: unknown;
  preparation?: unknown;
  confirmationReady: boolean;
  plannerCallCount: number;
  mutationExecuted: false;
}>;

const plugin = registerPlugin<NativeVoiceFlowPlugin>('ScrumboyVoiceFlow');

function bridgeFailure(input: string): VoiceCreateDryRunResultV1 {
  return Object.freeze({
    version: 1,
    input,
    outcome: 'unexpected_failure',
    planner: Object.freeze({
      status: 'failed',
      stage: 'context_authorization',
      code: 'debug_bridge_unavailable',
    }),
    confirmationReady: false,
    plannerCallCount: 0,
    mutationExecuted: false,
  });
}

function validRequest(request: NativeDryRunRequest): boolean {
  return !!request
    && typeof request.requestId === 'string'
    && /^[A-Za-z0-9_-]{1,64}$/.test(request.requestId)
    && typeof request.transcript === 'string'
    && request.transcript.length <= 8_192
    && (request.timeoutMs === undefined || (Number.isInteger(request.timeoutMs) && request.timeoutMs > 0 && request.timeoutMs <= 120_000));
}

function validResult(result: unknown): result is VoiceCreateDryRunResultV1 {
  if (!result || typeof result !== 'object') return false;
  const value = result as Partial<VoiceCreateDryRunResultV1>;
  return value.version === 1
    && value.mutationExecuted === false
    && typeof value.confirmationReady === 'boolean'
    && Number.isInteger(value.plannerCallCount)
    && Number(value.plannerCallCount) >= 0
    && Number(value.plannerCallCount) <= 1;
}

/** Native requests are deliberately serialized to avoid concurrent Nano generations. */
export async function installVoiceCreateDryRunBridge(
  importer: ModuleImporter = path => import(path),
  nativePlugin: NativeVoiceFlowPlugin = plugin,
): Promise<PluginListenerHandle> {
  let serial = Promise.resolve();
  return nativePlugin.addListener(NATIVE_VOICE_CREATE_DRY_RUN_EVENT, request => {
    if (!validRequest(request)) return;
    serial = serial.then(async () => {
      let result: VoiceCreateDryRunResultV1;
      try {
        const evaluator = await importer('/dist/voice/voice-create-device-evaluation.js') as DeviceEvaluationModule;
        const evaluated = await evaluator.evaluateVoiceCreateDryRunOnCurrentBoard(request.transcript, { timeoutMs: request.timeoutMs });
        result = validResult(evaluated) ? evaluated : bridgeFailure(request.transcript);
      } catch {
        result = bridgeFailure(request.transcript);
      }
      await nativePlugin.completeDryRun({ requestId: request.requestId, resultJson: JSON.stringify(result) });
    }).catch(() => undefined);
  });
}
