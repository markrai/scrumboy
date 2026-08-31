import { describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability, LocalTextGenerationStatus } from '../platform/local-text-generation.js';
import type { SpeechInputCapability, SpeechInputStatus } from '../platform/speech-input.js';
import { selectVoiceFlowExperience } from './experience-selection.js';

function ai(status: LocalTextGenerationStatus): LocalTextGenerationCapability {
  return {
    status: vi.fn().mockResolvedValue(status),
    prepare: vi.fn(),
    generate: vi.fn(),
  };
}

function speech(status: SpeechInputStatus): SpeechInputCapability {
  return {
    status: vi.fn().mockResolvedValue(status),
    listen: vi.fn(),
  };
}

function runtime(localAi: LocalTextGenerationCapability | null, speechInput: SpeechInputCapability | null) {
  return {
    capability: vi.fn((name: string) => name === 'local-text-generation' ? localAi : speechInput),
  } as any;
}

describe('VoiceFlow experience selection', () => {
  it('keeps absent browser capabilities on legacy deterministic VoiceFlow', async () => {
    await expect(selectVoiceFlowExperience({ runtime: runtime(null, null), locale: 'en' }))
      .resolves.toEqual({ kind: 'legacy-deterministic', reason: 'ai-absent' });
  });

  it('keeps explicitly unsupported AI or speech on legacy VoiceFlow', async () => {
    await expect(selectVoiceFlowExperience({
      runtime: runtime(ai({ state: 'unsupported', reason: 'device' }), speech({ state: 'ready' })),
      locale: 'en',
    })).resolves.toEqual({ kind: 'legacy-deterministic', reason: 'ai-unsupported' });

    await expect(selectVoiceFlowExperience({
      runtime: runtime(
        ai({ state: 'ready', maximumOutputTokens: 96 }),
        speech({ state: 'unsupported', reason: 'provider' }),
      ),
      locale: 'en',
    })).resolves.toEqual({ kind: 'legacy-deterministic', reason: 'speech-unsupported' });
  });

  it('selects the floating agent only when both independent capabilities are ready', async () => {
    const localAi = ai({ state: 'ready', maximumOutputTokens: 96 });
    const speechInput = speech({ state: 'ready' });

    await expect(selectVoiceFlowExperience({
      runtime: runtime(localAi, speechInput),
      locale: 'en',
    })).resolves.toEqual({ kind: 'enhanced-agent', localTextGeneration: localAi, speechInput });
  });

  it('lets supported-but-not-ready enhanced capability state own the turn', async () => {
    const localAi = ai({ state: 'preparing', downloadedBytes: 1, totalBytes: 2 });
    const speechInput = speech({ state: 'ready' });

    await expect(selectVoiceFlowExperience({
      runtime: runtime(localAi, speechInput),
      locale: 'en',
    })).resolves.toMatchObject({ kind: 'enhanced-not-ready', reason: 'ai' });
  });

  it('does not inspect runtime kind or platform identity', async () => {
    const selectedRuntime = runtime(
      ai({ state: 'ready', maximumOutputTokens: 96 }),
      speech({ state: 'ready' }),
    );

    await selectVoiceFlowExperience({ runtime: selectedRuntime, locale: 'en' });

    expect(selectedRuntime.capability.mock.calls.map((call: unknown[]) => call[0]))
      .toEqual(['local-text-generation', 'speech-input']);
  });
});
