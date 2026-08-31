import { getLocale } from '../i18n/index.js';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  type LocalTextGenerationCapability,
  type LocalTextGenerationStatus,
} from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import {
  SPEECH_INPUT_CAPABILITY,
  type SpeechInputCapability,
  type SpeechInputStatus,
} from '../platform/speech-input.js';

type VoiceExperienceRuntime = Pick<ReturnType<typeof getAppRuntime>, 'capability'>;

export type VoiceFlowExperienceSelection =
  | Readonly<{
      kind: 'legacy-deterministic';
      reason: 'locale' | 'ai-absent' | 'ai-unsupported' | 'speech-absent' | 'speech-unsupported';
    }>
  | Readonly<{
      kind: 'enhanced-agent';
      localTextGeneration: LocalTextGenerationCapability;
      speechInput: SpeechInputCapability;
    }>
  | Readonly<{
      kind: 'enhanced-not-ready';
      localTextGeneration: LocalTextGenerationCapability;
      speechInput: SpeechInputCapability;
      aiStatus: LocalTextGenerationStatus | null;
      speechStatus: SpeechInputStatus | null;
      reason: 'ai' | 'speech' | 'status-error';
    }>;

export type VoiceFlowExperienceSelectionOptions = Readonly<{
  runtime?: VoiceExperienceRuntime;
  locale?: string;
  signal?: AbortSignal;
}>;

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw new DOMException('VoiceFlow selection cancelled.', 'AbortError');
}

export async function selectVoiceFlowExperience(
  options: VoiceFlowExperienceSelectionOptions = {},
): Promise<VoiceFlowExperienceSelection> {
  throwIfAborted(options.signal);
  if ((options.locale ?? getLocale()) !== 'en') {
    return { kind: 'legacy-deterministic', reason: 'locale' };
  }

  const runtime = options.runtime ?? getAppRuntime();
  const localTextGeneration = runtime.capability(LOCAL_TEXT_GENERATION_CAPABILITY);
  if (!localTextGeneration) return { kind: 'legacy-deterministic', reason: 'ai-absent' };
  const speechInput = runtime.capability(SPEECH_INPUT_CAPABILITY);
  if (!speechInput) return { kind: 'legacy-deterministic', reason: 'speech-absent' };

  let aiStatus: LocalTextGenerationStatus | null = null;
  let speechStatus: SpeechInputStatus | null = null;
  try {
    [aiStatus, speechStatus] = await Promise.all([
      localTextGeneration.status({ signal: options.signal }),
      speechInput.status({ signal: options.signal }),
    ]);
  } catch {
    throwIfAborted(options.signal);
    return {
      kind: 'enhanced-not-ready',
      localTextGeneration,
      speechInput,
      aiStatus,
      speechStatus,
      reason: 'status-error',
    };
  }
  throwIfAborted(options.signal);

  if (aiStatus.state === 'unsupported') {
    return { kind: 'legacy-deterministic', reason: 'ai-unsupported' };
  }
  if (speechStatus.state === 'unsupported') {
    return { kind: 'legacy-deterministic', reason: 'speech-unsupported' };
  }
  if (aiStatus.state === 'ready' && speechStatus.state === 'ready') {
    return { kind: 'enhanced-agent', localTextGeneration, speechInput };
  }
  return {
    kind: 'enhanced-not-ready',
    localTextGeneration,
    speechInput,
    aiStatus,
    speechStatus,
    reason: aiStatus.state === 'ready' ? 'speech' : 'ai',
  };
}
