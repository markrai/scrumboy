import {
  createClientCapabilityRegistry,
  type AppCapabilityMap,
  type AppCapabilityRegistry,
} from '../../../internal/httpapi/web/modules/platform/client-capabilities.js';
import {
  SPEECH_INPUT_CAPABILITY,
  SpeechInputError,
  isSpeechInputErrorCode,
  isSpeechInputProviderCode,
  isSpeechInputProviderReason,
  validateSpeechInputListenOptions,
  validateSpeechInputResult,
  type SpeechInputCapability,
  type SpeechInputErrorCode,
  type SpeechInputListenOptions,
  type SpeechInputStatus,
  type SpeechInputStatusOptions,
} from '../../../internal/httpapi/web/modules/platform/speech-input.js';
import { voiceFlowDiagnostic } from '../../../internal/httpapi/web/modules/platform/voiceflow-diagnostics.js';
import {
  ScrumboySpeechInput,
  NATIVE_SPEECH_LISTENING_EVENT,
  type NativeSpeechInputPlugin,
  type NativeSpeechInputStatus,
} from './native-speech-input-plugin.js';

const OPERATION_ID_CODE_UNITS = 128;
const INVALIDATION_TIMEOUT_MS = 1_000;

interface NativeFailure {
  code?: unknown;
  data?: {
    recoverable?: unknown;
    providerCode?: unknown;
    providerReason?: unknown;
  };
}

interface ActiveOperation<T> {
  readonly id: string;
  invalidated: boolean;
  cancelling: boolean;
  settled: boolean;
  finishResolve(value: T): void;
  finishReject(error: SpeechInputError): void;
}

export interface SpeechInputComposition {
  registry: AppCapabilityRegistry;
  invalidate(): Promise<void>;
}

export interface SpeechInputCompositionOptions {
  plugin?: NativeSpeechInputPlugin;
  operationIdFactory?: () => string;
  invalidationTimeoutMs?: number;
}

let nextOperationId = 0;

function defaultOperationId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID();
  nextOperationId += 1;
  return `speech-input-${nextOperationId.toString(36)}`;
}

function nativeError(error: unknown): SpeechInputError {
  if (error instanceof SpeechInputError) return error;
  const failure = error as NativeFailure | null;
  const code: SpeechInputErrorCode = isSpeechInputErrorCode(failure?.code)
    ? failure.code
    : 'internal';
  return new SpeechInputError(code, {
    recoverable: typeof failure?.data?.recoverable === 'boolean'
      ? failure.data.recoverable
      : undefined,
    providerCode: isSpeechInputProviderCode(failure?.data?.providerCode)
      ? failure.data.providerCode
      : undefined,
    providerReason: isSpeechInputProviderReason(failure?.data?.providerReason)
      ? failure.data.providerReason
      : undefined,
  });
}

function cancelled(): SpeechInputError {
  return new SpeechInputError('cancelled');
}

function validSignal(signal: unknown): signal is AbortSignal | undefined {
  return signal === undefined || (
    typeof signal === 'object'
    && signal !== null
    && typeof (signal as AbortSignal).aborted === 'boolean'
    && typeof (signal as AbortSignal).addEventListener === 'function'
    && typeof (signal as AbortSignal).removeEventListener === 'function'
  );
}

function validateStatus(value: NativeSpeechInputStatus): SpeechInputStatus {
  if (!value || typeof value !== 'object') throw new SpeechInputError('internal');
  switch (value.state) {
    case 'ready':
      return { state: 'ready' };
    case 'unsupported':
      if (['os', 'device', 'provider', 'policy'].includes(value.reason)) {
        return { state: 'unsupported', reason: value.reason };
      }
      break;
    case 'temporarily-unavailable':
      if (['busy', 'foreground', 'provider'].includes(value.reason)) {
        return { state: 'temporarily-unavailable', reason: value.reason };
      }
      break;
  }
  throw new SpeechInputError('internal');
}

function boundedWait(promise: Promise<void>, timeoutMs: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = globalThis.setTimeout(resolve, timeoutMs);
    promise.catch(() => undefined).then(() => {
      globalThis.clearTimeout(timer);
      resolve();
    });
  });
}

export function createSpeechInputComposition(
  options: SpeechInputCompositionOptions = {},
): SpeechInputComposition {
  const plugin = options.plugin ?? ScrumboySpeechInput;
  const operationIdFactory = options.operationIdFactory ?? defaultOperationId;
  const requestedTimeout = options.invalidationTimeoutMs ?? INVALIDATION_TIMEOUT_MS;
  const invalidationTimeoutMs = Number.isFinite(requestedTimeout) && requestedTimeout >= 0
    ? requestedTimeout
    : INVALIDATION_TIMEOUT_MS;
  const operations = new Map<string, ActiveOperation<unknown>>();
  let activeListening: string | null = null;
  let invalidation: Promise<void> | null = null;

  function run<T>(
    signal: AbortSignal | undefined,
    maxDurationMs: number | null,
    invoke: (operationId: string) => Promise<T>,
  ): Promise<T> {
    if (signal?.aborted || invalidation !== null) return Promise.reject(cancelled());
    let operationId: string;
    try {
      operationId = operationIdFactory();
    } catch {
      return Promise.reject(new SpeechInputError('internal'));
    }
    if (
      typeof operationId !== 'string'
      || operationId.length === 0
      || operationId.length > OPERATION_ID_CODE_UNITS
      || operations.has(operationId)
    ) {
      return Promise.reject(new SpeechInputError('invalid_request', { recoverable: false }));
    }

    return new Promise<T>((resolve, reject) => {
      let removeAbort = () => {};
      let deadline: ReturnType<typeof globalThis.setTimeout> | null = null;
      const operation: ActiveOperation<T> = {
        id: operationId,
        invalidated: false,
        cancelling: false,
        settled: false,
        finishResolve(value) {
          if (operation.settled) return;
          operation.settled = true;
          operations.delete(operationId);
          if (deadline !== null) globalThis.clearTimeout(deadline);
          removeAbort();
          resolve(value);
        },
        finishReject(error) {
          if (operation.settled) return;
          operation.settled = true;
          operations.delete(operationId);
          if (deadline !== null) globalThis.clearTimeout(deadline);
          removeAbort();
          reject(error);
        },
      };
      operations.set(operationId, operation as ActiveOperation<unknown>);

      const cancel = (error: SpeechInputError) => {
        if (operation.settled || operation.cancelling || operation.invalidated) return;
        operation.cancelling = true;
        try {
          void Promise.resolve(plugin.cancel({ operationId })).catch(() => undefined);
        } catch {
          // The shared operation still loses ownership immediately. A late native
          // result is ignored even if a broken provider cannot acknowledge cancel.
        }
        operation.finishReject(error);
      };

      const onAbort = () => cancel(cancelled());
      if (signal) {
        signal.addEventListener('abort', onAbort, { once: true });
        removeAbort = () => signal.removeEventListener('abort', onAbort);
      }
      if (maxDurationMs !== null) {
        deadline = globalThis.setTimeout(
          () => cancel(new SpeechInputError('timeout')),
          maxDurationMs,
        );
      }

      let pending: Promise<T>;
      try {
        pending = invoke(operationId);
      } catch (error) {
        operation.finishReject(nativeError(error));
        return;
      }
      pending.then(
        (value) => {
          if (!operation.cancelling && !operation.invalidated) operation.finishResolve(value);
        },
        (error) => {
          if (!operation.cancelling && !operation.invalidated) operation.finishReject(nativeError(error));
        },
      );
    });
  }

  const capability: SpeechInputCapability = Object.freeze({
    status(options: SpeechInputStatusOptions = {}) {
      if (!options || typeof options !== 'object' || !validSignal(options.signal)) {
        return Promise.reject(new SpeechInputError('invalid_request', { recoverable: false }));
      }
      return run(options.signal, null, (operationId) => plugin.status({ operationId }))
        .then(validateStatus);
    },
    listen(listenOptions: SpeechInputListenOptions) {
      try {
        validateSpeechInputListenOptions(listenOptions);
      } catch (error) {
        return Promise.reject(nativeError(error));
      }
      if (!validSignal(listenOptions.signal)) {
        return Promise.reject(new SpeechInputError('invalid_request', { recoverable: false }));
      }
      if (activeListening !== null) return Promise.reject(new SpeechInputError('busy'));
      activeListening = 'starting';
      return run(
        listenOptions.signal,
        listenOptions.maxDurationMs,
        async (operationId) => {
          activeListening = operationId;
          voiceFlowDiagnostic('ASR start', { operationId });
          let announced = false;
          const listener = await plugin.addListener(NATIVE_SPEECH_LISTENING_EVENT, (event) => {
            if (
              announced
              || event.operationId !== operationId
              || activeListening !== operationId
              || listenOptions.signal?.aborted
            ) return;
            announced = true;
            voiceFlowDiagnostic('ASR ready', { operationId });
            try {
              listenOptions.onListening?.();
            } catch {
              // Presentation callbacks never own or terminate native recognition.
            }
          });
          try {
            if (activeListening !== operationId || listenOptions.signal?.aborted) throw cancelled();
            return await plugin.listen({
              operationId,
              maxDurationMs: listenOptions.maxDurationMs,
              ...(listenOptions.language ? { language: listenOptions.language } : {}),
            });
          } finally {
            await listener.remove().catch(() => undefined);
          }
        },
      ).then((result) => {
        validateSpeechInputResult(result);
        const transcript = result.transcript.trim();
        voiceFlowDiagnostic('ASR result', { operationId: activeListening, transcript });
        return { transcript };
      }).catch((error: unknown) => {
        const failure = nativeError(error);
        voiceFlowDiagnostic('ASR failure', {
          operationId: activeListening,
          normalizedCode: failure.code,
          ...(failure.providerCode === undefined ? {} : { providerCode: failure.providerCode }),
          ...(failure.providerReason === undefined ? {} : { providerReason: failure.providerReason }),
        });
        throw failure;
      }).finally(() => {
        activeListening = null;
      });
    },
  });

  return {
    registry: createClientCapabilityRegistry<AppCapabilityMap>({
      [SPEECH_INPUT_CAPABILITY]: capability,
    }),
    invalidate() {
      if (invalidation !== null) return invalidation;
      invalidation = (async () => {
        const active = [...operations.values()];
        for (const operation of active) operation.invalidated = true;
        let nativeInvalidation: Promise<void>;
        try {
          nativeInvalidation = Promise.resolve(plugin.invalidate());
        } catch {
          nativeInvalidation = Promise.resolve();
        }
        await boundedWait(nativeInvalidation, invalidationTimeoutMs);
        for (const operation of active) operation.finishReject(cancelled());
        activeListening = null;
      })().finally(() => {
        invalidation = null;
      });
      return invalidation;
    },
  };
}
