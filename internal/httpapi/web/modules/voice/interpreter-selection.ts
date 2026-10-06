import { getLocale } from '../i18n/index.js';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  LocalTextGenerationError,
  type LocalTextGenerationAction,
  type LocalTextGenerationCapability,
  type LocalTextGenerationUnavailableReason,
} from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { deterministicVoiceCommandInterpreter } from './deterministic-interpreter.js';
import type { VoiceCommandInterpreter } from './interpreter.js';
import { createLocalAiVoiceCommandInterpreter } from './local-ai-interpreter.js';
import { prepareVoiceInterpretation } from './local-interpretation.js';

export type VoiceInterpreterAvailability =
  | { state: 'absent' }
  | { state: 'locale-unsupported' }
  | { state: 'unsupported' }
  | { state: 'action-required'; action: LocalTextGenerationAction }
  | { state: 'preparing'; downloadedBytes?: number; totalBytes?: number }
  | { state: 'ready' }
  | {
      state: 'temporarily-unavailable';
      reason: LocalTextGenerationUnavailableReason;
      retryAfterMs?: number;
    };

export type VoiceInterpreterSelection =
  | {
      kind: 'interpreter';
      provider: 'deterministic';
      availability: Extract<
        VoiceInterpreterAvailability,
        { state: 'absent' | 'locale-unsupported' | 'unsupported' }
      >;
      interpreter: VoiceCommandInterpreter;
    }
  | {
      kind: 'interpreter';
      provider: 'local-ai';
      availability: Extract<VoiceInterpreterAvailability, { state: 'ready' }>;
      interpreter: VoiceCommandInterpreter;
    }
  | {
      kind: 'enhanced-not-ready';
      availability: Extract<
        VoiceInterpreterAvailability,
        { state: 'action-required' | 'preparing' | 'temporarily-unavailable' }
      >;
    };

export type VoiceInterpreterFailureCode =
  | 'cancelled'
  | 'busy'
  | 'temporarily-unavailable'
  | 'foreground-required'
  | 'storage-required'
  | 'invalid-output'
  | 'input-too-large'
  | 'unavailable';

export type VoiceInterpreterFailure = {
  code: VoiceInterpreterFailureCode;
  recoverable: boolean;
  retryAfterMs?: number;
};

type VoiceInterpreterCapabilityRegistry = {
  capability(name: typeof LOCAL_TEXT_GENERATION_CAPABILITY): LocalTextGenerationCapability | null;
};

export type VoiceInterpreterSelectionOptions = {
  locale?: string;
  runtime?: VoiceInterpreterCapabilityRegistry;
  signal?: AbortSignal;
};

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw new LocalTextGenerationError('cancelled');
}

export async function selectVoiceCommandInterpreterForTurn(
  options: VoiceInterpreterSelectionOptions = {},
): Promise<VoiceInterpreterSelection> {
  throwIfAborted(options.signal);
  const locale = options.locale ?? getLocale();
  if (locale !== 'en') {
    return {
      kind: 'interpreter',
      provider: 'deterministic',
      availability: { state: 'locale-unsupported' },
      interpreter: deterministicVoiceCommandInterpreter,
    };
  }

  const capability = (options.runtime ?? getAppRuntime()).capability(
    LOCAL_TEXT_GENERATION_CAPABILITY,
  );
  if (!capability) {
    return {
      kind: 'interpreter',
      provider: 'deterministic',
      availability: { state: 'absent' },
      interpreter: deterministicVoiceCommandInterpreter,
    };
  }

  const status = await capability.status({ signal: options.signal });
  throwIfAborted(options.signal);
  switch (status.state) {
    case 'unsupported':
      return {
        kind: 'interpreter',
        provider: 'deterministic',
        availability: { state: 'unsupported' },
        interpreter: deterministicVoiceCommandInterpreter,
      };
    case 'ready':
      return {
        kind: 'interpreter',
        provider: 'local-ai',
        availability: { state: 'ready' },
        interpreter: createLocalAiVoiceCommandInterpreter({ capability, locale }),
      };
    case 'action-required':
      return {
        kind: 'enhanced-not-ready',
        availability: { state: status.state, action: status.action },
      };
    case 'preparing':
      return {
        kind: 'enhanced-not-ready',
        availability: {
          state: status.state,
          downloadedBytes: status.downloadedBytes,
          totalBytes: status.totalBytes,
        },
      };
    case 'temporarily-unavailable':
      return {
        kind: 'enhanced-not-ready',
        availability: {
          state: status.state,
          reason: status.reason,
          retryAfterMs: status.retryAfterMs,
        },
      };
  }
}

export async function prepareVoiceCommandInterpreterForTurn(
  options: VoiceInterpreterSelectionOptions = {},
): Promise<void> {
  throwIfAborted(options.signal);
  const locale = options.locale ?? getLocale();
  const capability = (options.runtime ?? getAppRuntime()).capability(
    LOCAL_TEXT_GENERATION_CAPABILITY,
  );
  await prepareVoiceInterpretation({ capability, locale, signal: options.signal });
}

export function normalizeVoiceInterpreterFailure(error: unknown): VoiceInterpreterFailure {
  if (error instanceof Error && error.name === 'AbortError') {
    return { code: 'cancelled', recoverable: true };
  }
  if (!(error instanceof LocalTextGenerationError)) {
    return { code: 'unavailable', recoverable: true };
  }
  const code = ({
    cancelled: 'cancelled',
    busy: 'busy',
    quota_exceeded: 'temporarily-unavailable',
    foreground_required: 'foreground-required',
    insufficient_storage: 'storage-required',
    output_rejected: 'invalid-output',
    input_too_large: 'input-too-large',
    unsupported: 'unavailable',
    disabled: 'unavailable',
    not_ready: 'unavailable',
    download_failed: 'unavailable',
    invalid_request: 'unavailable',
    internal: 'unavailable',
  } as const satisfies Record<LocalTextGenerationError['code'], VoiceInterpreterFailureCode>)[error.code];
  return {
    code,
    recoverable: error.recoverable,
    ...(error.retryAfterMs == null ? {} : { retryAfterMs: error.retryAfterMs }),
  };
}
