import {
  createClientCapabilityRegistry,
  type AppCapabilityMap,
  type AppCapabilityRegistry,
} from '../../../internal/httpapi/web/modules/platform/client-capabilities.js';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  LOCAL_TEXT_GENERATION_LIMITS,
  LocalTextGenerationError,
  isLocalTextGenerationErrorCode,
  validateLocalTextGenerationOutput,
  validateLocalTextGenerationRequest,
  type LocalTextGenerationCapability,
  type LocalTextGenerationErrorCode,
  type LocalTextGenerationPrepareOptions,
  type LocalTextGenerationRequest,
  type LocalTextGenerationStatus,
  type LocalTextGenerationStatusOptions,
} from '../../../internal/httpapi/web/modules/platform/local-text-generation.js';
import {
  ScrumboyLocalTextGeneration,
  type NativeLocalTextGenerationPlugin,
  type NativeLocalTextGenerationStatus,
} from './native-local-text-generation-plugin.js';

const RECENT_REQUEST_LIMIT = 256;
const INVALIDATION_TIMEOUT_MS = 1_000;

interface NativeFailure {
  code?: unknown;
  data?: {
    recoverable?: unknown;
    retryAfterMs?: unknown;
  };
}

interface ActiveOperation<T> {
  readonly id: string;
  invalidated: boolean;
  cancelling: boolean;
  settled: boolean;
  finishResolve(value: T): void;
  finishReject(error: LocalTextGenerationError): void;
}

export interface LocalTextGenerationComposition {
  registry: AppCapabilityRegistry;
  invalidate(): Promise<void>;
}

export interface LocalTextGenerationCompositionOptions {
  plugin?: NativeLocalTextGenerationPlugin;
  operationIdFactory?: () => string;
  invalidationTimeoutMs?: number;
}

let nextOperationId = 0;

function defaultOperationId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID();
  nextOperationId += 1;
  return `local-ai-${nextOperationId.toString(36)}`;
}

function cancelled(): LocalTextGenerationError {
  return new LocalTextGenerationError('cancelled');
}

function nativeError(error: unknown): LocalTextGenerationError {
  if (error instanceof LocalTextGenerationError) return error;
  const failure = error as NativeFailure | null;
  const code: LocalTextGenerationErrorCode = isLocalTextGenerationErrorCode(failure?.code)
    ? failure.code
    : 'internal';
  const retryAfterMs = failure?.data?.retryAfterMs;
  const recoverable = failure?.data?.recoverable;
  return new LocalTextGenerationError(code, {
    recoverable: typeof recoverable === 'boolean' ? recoverable : undefined,
    retryAfterMs: Number.isSafeInteger(retryAfterMs) &&
      Number(retryAfterMs) >= 0 &&
      Number(retryAfterMs) <= LOCAL_TEXT_GENERATION_LIMITS.retryAfterMs
      ? Number(retryAfterMs)
      : undefined,
  });
}

function optionalNonNegativeInteger(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && Number(value) >= 0 ? Number(value) : undefined;
}

function isOptionalAbortSignal(value: unknown): value is AbortSignal | undefined {
  return value === undefined || (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as AbortSignal).aborted === 'boolean' &&
    typeof (value as AbortSignal).addEventListener === 'function' &&
    typeof (value as AbortSignal).removeEventListener === 'function'
  );
}

function invalidRequestError(): LocalTextGenerationError {
  return new LocalTextGenerationError('invalid_request', { recoverable: false });
}

function validateStatus(value: NativeLocalTextGenerationStatus): LocalTextGenerationStatus {
  if (!value || typeof value !== 'object') throw new LocalTextGenerationError('internal');
  switch (value.state) {
    case 'unsupported':
      if (!['os', 'device', 'provider', 'policy'].includes(value.reason)) break;
      return { state: value.state, reason: value.reason };
    case 'action-required':
      if (!['download', 'enable', 'system-update'].includes(value.action)) break;
      return { state: value.state, action: value.action };
    case 'preparing': {
      const downloadedBytes = optionalNonNegativeInteger(value.downloadedBytes);
      const totalBytes = optionalNonNegativeInteger(value.totalBytes);
      if (value.downloadedBytes !== undefined && downloadedBytes === undefined) break;
      if (value.totalBytes !== undefined && totalBytes === undefined) break;
      return { state: value.state, downloadedBytes, totalBytes };
    }
    case 'ready': {
      if (
        !Number.isInteger(value.maximumOutputTokens) ||
        value.maximumOutputTokens < 1 ||
        value.maximumOutputTokens > LOCAL_TEXT_GENERATION_LIMITS.maximumOutputTokens
      ) break;
      const contextTokenLimit = optionalNonNegativeInteger(value.contextTokenLimit);
      if (value.contextTokenLimit !== undefined && contextTokenLimit === undefined) break;
      if (
        value.providerModel !== undefined &&
        (typeof value.providerModel !== 'string' ||
          value.providerModel.length > LOCAL_TEXT_GENERATION_LIMITS.providerModelCodeUnits)
      ) break;
      return {
        state: value.state,
        maximumOutputTokens: value.maximumOutputTokens,
        contextTokenLimit,
        providerModel: value.providerModel,
      };
    }
    case 'temporarily-unavailable': {
      if (!['initializing', 'busy', 'quota', 'foreground', 'storage', 'provider'].includes(value.reason)) break;
      const retryAfterMs = optionalNonNegativeInteger(value.retryAfterMs);
      if (value.retryAfterMs !== undefined && retryAfterMs === undefined) break;
      return { state: value.state, reason: value.reason, retryAfterMs };
    }
  }
  throw new LocalTextGenerationError('internal');
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

export function createLocalTextGenerationComposition(
  options: LocalTextGenerationCompositionOptions = {},
): LocalTextGenerationComposition {
  const plugin = options.plugin ?? ScrumboyLocalTextGeneration;
  const operationIdFactory = options.operationIdFactory ?? defaultOperationId;
  const requestedInvalidationTimeout = options.invalidationTimeoutMs ?? INVALIDATION_TIMEOUT_MS;
  const invalidationTimeoutMs = Number.isFinite(requestedInvalidationTimeout) && requestedInvalidationTimeout >= 0
    ? requestedInvalidationTimeout
    : INVALIDATION_TIMEOUT_MS;
  const operations = new Map<string, ActiveOperation<unknown>>();
  const recentRequestIds = new Set<string>();
  const recentRequestOrder: string[] = [];
  let activeGeneration: string | null = null;
  let invalidation: Promise<void> | null = null;

  function rememberRequestId(requestId: string): void {
    recentRequestIds.add(requestId);
    recentRequestOrder.push(requestId);
    if (recentRequestOrder.length > RECENT_REQUEST_LIMIT) {
      recentRequestIds.delete(recentRequestOrder.shift()!);
    }
  }

  function run<T>(
    signal: AbortSignal | undefined,
    invoke: (operationId: string) => Promise<T>,
  ): Promise<T> {
    if (signal?.aborted || invalidation !== null) return Promise.reject(cancelled());
    let operationId: string;
    try {
      operationId = operationIdFactory();
    } catch {
      return Promise.reject(new LocalTextGenerationError('internal'));
    }
    if (
      typeof operationId !== 'string' ||
      operationId.length === 0 ||
      operationId.length > LOCAL_TEXT_GENERATION_LIMITS.requestIdCodeUnits ||
      operations.has(operationId)
    ) {
      return Promise.reject(new LocalTextGenerationError('invalid_request', { recoverable: false }));
    }

    return new Promise<T>((resolve, reject) => {
      let removeAbort = () => {};
      const operation: ActiveOperation<T> = {
        id: operationId,
        invalidated: false,
        cancelling: false,
        settled: false,
        finishResolve(value) {
          if (operation.settled) return;
          operation.settled = true;
          operations.delete(operationId);
          removeAbort();
          resolve(value);
        },
        finishReject(error) {
          if (operation.settled) return;
          operation.settled = true;
          operations.delete(operationId);
          removeAbort();
          reject(error);
        },
      };
      operations.set(operationId, operation as ActiveOperation<unknown>);

      const abort = () => {
        if (operation.settled || operation.cancelling || operation.invalidated) return;
        operation.cancelling = true;
        let nativeCancellation: Promise<void>;
        try {
          nativeCancellation = Promise.resolve(plugin.cancel({ operationId }));
        } catch {
          nativeCancellation = Promise.resolve();
        }
        void nativeCancellation
          .catch(() => undefined)
          .then(() => operation.finishReject(cancelled()));
      };
      if (signal) {
        signal.addEventListener('abort', abort, { once: true });
        removeAbort = () => signal.removeEventListener('abort', abort);
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

  const capability: LocalTextGenerationCapability = Object.freeze({
    status(options: LocalTextGenerationStatusOptions = {}) {
      if (!options || typeof options !== 'object' || !isOptionalAbortSignal(options.signal)) {
        return Promise.reject(invalidRequestError());
      }
      return run(options.signal, (operationId) => plugin.status({ operationId })).then(validateStatus);
    },
    prepare(options: LocalTextGenerationPrepareOptions) {
      if (
        !options ||
        typeof options !== 'object' ||
        options.userInitiated !== true ||
        !isOptionalAbortSignal(options.signal)
      ) {
        return Promise.reject(invalidRequestError());
      }
      return run(options.signal, (operationId) => plugin.prepare({ operationId, userInitiated: true }));
    },
    generate(request: LocalTextGenerationRequest) {
      try {
        validateLocalTextGenerationRequest(request);
      } catch (error) {
        return Promise.reject(nativeError(error));
      }
      if (!isOptionalAbortSignal(request.signal)) return Promise.reject(invalidRequestError());
      if (activeGeneration !== null) {
        return Promise.reject(new LocalTextGenerationError('busy'));
      }
      if (recentRequestIds.has(request.requestId)) {
        return Promise.reject(new LocalTextGenerationError('invalid_request', { recoverable: false }));
      }
      rememberRequestId(request.requestId);
      activeGeneration = request.requestId;
      return run(request.signal, (operationId) => plugin.generate({
        operationId,
        requestId: request.requestId,
        input: request.input,
        instructions: request.instructions,
        maximumOutputTokens: request.maximumOutputTokens,
      })).then((result) => {
        if (!result || result.requestId !== request.requestId) {
          throw new LocalTextGenerationError('output_rejected', { recoverable: false });
        }
        validateLocalTextGenerationOutput(result.text);
        return { requestId: result.requestId, text: result.text };
      }).finally(() => {
        if (activeGeneration === request.requestId) activeGeneration = null;
      });
    },
  });

  const registry = createClientCapabilityRegistry<AppCapabilityMap>({
    [LOCAL_TEXT_GENERATION_CAPABILITY]: capability,
  });

  return {
    registry,
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
        activeGeneration = null;
      })().finally(() => {
        invalidation = null;
      });
      return invalidation;
    },
  };
}
