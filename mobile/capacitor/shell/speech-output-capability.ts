import {
  createClientCapabilityRegistry,
  type AppCapabilityMap,
  type AppCapabilityRegistry,
} from '../../../internal/httpapi/web/modules/platform/client-capabilities.js';
import {
  SPEECH_OUTPUT_CAPABILITY,
  SpeechOutputError,
  isSpeechOutputErrorCode,
  validateSpeechOutputSpeakOptions,
  type SpeechOutputCapability,
  type SpeechOutputErrorCode,
  type SpeechOutputResult,
  type SpeechOutputSpeakOptions,
  type SpeechOutputStatus,
  type SpeechOutputStatusOptions,
} from '../../../internal/httpapi/web/modules/platform/speech-output.js';
import {
  ScrumboySpeechOutput,
  type NativeSpeechOutputPlugin,
  type NativeSpeechOutputStatus,
} from './native-speech-output-plugin.js';

const OPERATION_ID_CODE_UNITS = 128;

interface NativeFailure {
  code?: unknown;
  data?: { recoverable?: unknown };
}

type ActiveSpeech = {
  readonly operationId: string;
  readonly reject: (error: SpeechOutputError) => void;
  settled: boolean;
};

type NativeStopInFlight = {
  readonly operationId: string;
  readonly promise: Promise<void>;
};

export interface SpeechOutputComposition {
  registry: AppCapabilityRegistry;
  invalidate(): Promise<void>;
}

export interface SpeechOutputCompositionOptions {
  plugin?: NativeSpeechOutputPlugin;
  operationIdFactory?: () => string;
}

let nextOperationId = 0;

function defaultOperationId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID();
  nextOperationId += 1;
  return `speech-output-${nextOperationId.toString(36)}`;
}

function nativeError(error: unknown): SpeechOutputError {
  if (error instanceof SpeechOutputError) return error;
  const failure = error as NativeFailure | null;
  const code: SpeechOutputErrorCode = isSpeechOutputErrorCode(failure?.code)
    ? failure.code
    : 'internal';
  return new SpeechOutputError(code, {
    recoverable: typeof failure?.data?.recoverable === 'boolean'
      ? failure.data.recoverable
      : undefined,
  });
}

function cancelled(): SpeechOutputError {
  return new SpeechOutputError('cancelled');
}

function validateStatus(value: NativeSpeechOutputStatus): SpeechOutputStatus {
  if (!value || typeof value !== 'object') throw new SpeechOutputError('internal');
  switch (value.state) {
    case 'ready': return { state: 'ready' };
    case 'not-ready':
      if (value.reason === 'initializing') return { state: 'not-ready', reason: value.reason };
      break;
    case 'unsupported':
      if (['os', 'provider', 'policy', 'no-local-voice'].includes(value.reason)) {
        return { state: 'unsupported', reason: value.reason };
      }
      break;
    case 'temporarily-unavailable':
      if (['busy', 'foreground', 'provider'].includes(value.reason)) {
        return { state: 'temporarily-unavailable', reason: value.reason };
      }
      break;
  }
  throw new SpeechOutputError('internal');
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

export function createSpeechOutputComposition(
  options: SpeechOutputCompositionOptions = {},
): SpeechOutputComposition {
  const plugin = options.plugin ?? ScrumboySpeechOutput;
  const operationIdFactory = options.operationIdFactory ?? defaultOperationId;
  let active: ActiveSpeech | null = null;
  let nativeStopInFlight: NativeStopInFlight | null = null;
  let invalidating: Promise<void> | null = null;

  const operationId = (): string => {
    const value = operationIdFactory();
    if (
      typeof value !== 'string'
      || value.length === 0
      || value.length > OPERATION_ID_CODE_UNITS
      || !/^[A-Za-z0-9._:-]+$/.test(value)
    ) throw new SpeechOutputError('invalid_request', { recoverable: false });
    return value;
  };

  const stopNativeOperation = (ownedOperationId: string): Promise<void> => {
    if (nativeStopInFlight) return nativeStopInFlight.promise;
    let promise!: Promise<void>;
    promise = Promise.resolve()
      .then(() => plugin.stop({ operationId: ownedOperationId }))
      .catch(() => undefined)
      .finally(() => {
        if (nativeStopInFlight?.promise === promise) nativeStopInFlight = null;
      });
    nativeStopInFlight = { operationId: ownedOperationId, promise };
    return promise;
  };

  const stopActive = (): Promise<void> => {
    const owned = active;
    if (owned && !owned.settled) {
      owned.settled = true;
      active = null;
      owned.reject(cancelled());
      return stopNativeOperation(owned.operationId);
    }
    return nativeStopInFlight?.promise ?? Promise.resolve();
  };

  const capability: SpeechOutputCapability = Object.freeze({
    async status(statusOptions: SpeechOutputStatusOptions = {}) {
      if (!statusOptions || typeof statusOptions !== 'object' || !validSignal(statusOptions.signal)) {
        throw new SpeechOutputError('invalid_request', { recoverable: false });
      }
      if (statusOptions.signal?.aborted) throw cancelled();
      const result = await plugin.status({ operationId: operationId() }).catch((error) => {
        throw nativeError(error);
      });
      if (statusOptions.signal?.aborted) throw cancelled();
      return validateStatus(result);
    },
    speak(speakOptions: SpeechOutputSpeakOptions): Promise<SpeechOutputResult> {
      try {
        validateSpeechOutputSpeakOptions(speakOptions);
      } catch (error) {
        return Promise.reject(nativeError(error));
      }
      if (!validSignal(speakOptions.signal)) {
        return Promise.reject(new SpeechOutputError('invalid_request', { recoverable: false }));
      }
      if (speakOptions.signal?.aborted || invalidating) return Promise.reject(cancelled());
      if (active || nativeStopInFlight) return Promise.reject(new SpeechOutputError('busy'));
      let id: string;
      try {
        id = operationId();
      } catch (error) {
        return Promise.reject(nativeError(error));
      }
      return new Promise<SpeechOutputResult>((resolve, reject) => {
        const operation: ActiveSpeech = { operationId: id, reject, settled: false };
        active = operation;
        const onAbort = () => {
          if (active !== operation || operation.settled) return;
          operation.settled = true;
          active = null;
          reject(cancelled());
          void stopNativeOperation(id);
        };
        speakOptions.signal?.addEventListener('abort', onAbort, { once: true });
        plugin.speak({
          operationId: id,
          text: speakOptions.text.trim(),
          ...(speakOptions.language ? { language: speakOptions.language } : {}),
        }).then(
          (result) => {
            if (active !== operation || operation.settled) return;
            operation.settled = true;
            active = null;
            speakOptions.signal?.removeEventListener('abort', onAbort);
            if (result.operationId !== id) {
              reject(new SpeechOutputError('synthesis_failed'));
              return;
            }
            resolve({ completed: true });
          },
          (error) => {
            if (active !== operation || operation.settled) return;
            operation.settled = true;
            active = null;
            speakOptions.signal?.removeEventListener('abort', onAbort);
            reject(nativeError(error));
          },
        );
      });
    },
    stop: stopActive,
    invalidate() {
      if (invalidating) return invalidating;
      invalidating = (async () => {
        await stopActive();
        await Promise.resolve(plugin.invalidate()).catch(() => undefined);
      })().finally(() => {
        invalidating = null;
      });
      return invalidating;
    },
  });

  return {
    registry: createClientCapabilityRegistry<AppCapabilityMap>({
      [SPEECH_OUTPUT_CAPABILITY]: capability,
    }),
    invalidate: () => capability.invalidate(),
  };
}
