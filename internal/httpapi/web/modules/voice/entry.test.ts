import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';

const openLegacy = vi.hoisted(() => vi.fn());
const openAgent = vi.hoisted(() => vi.fn());
const openNotReady = vi.hoisted(() => vi.fn());
const closeAgent = vi.hoisted(() => vi.fn());

vi.mock('./flow.js', () => ({ openVoiceCommandDialog: openLegacy }));
vi.mock('./agent.js', () => ({
  openVoiceAgent: openAgent,
  openVoiceAgentNotReady: openNotReady,
  closeVoiceAgent: closeAgent,
}));

import { openVoiceFlow } from './entry.js';

const localTextGeneration = {} as LocalTextGenerationCapability;
const speechInput = {} as SpeechInputCapability;
const options = {
  initialUserId: 7,
  initialProjectId: 1,
  initialProjectSlug: 'alpha',
  getContext: vi.fn(),
  refreshBoard: vi.fn(),
  openTodo: vi.fn(),
};

beforeEach(() => {
  openLegacy.mockClear();
  openAgent.mockClear();
  openNotReady.mockClear();
  closeAgent.mockClear();
});

describe('VoiceFlow entry coordinator', () => {
  it('routes an unsupported runtime to the unchanged deterministic dialog', async () => {
    await openVoiceFlow(options, {
      selectExperience: async () => ({ kind: 'legacy-deterministic', reason: 'speech-unsupported' }),
    });

    expect(openLegacy).toHaveBeenCalledWith(options);
    expect(openAgent).not.toHaveBeenCalled();
    expect(openNotReady).not.toHaveBeenCalled();
  });

  it('routes a fully ready capability pair to the floating agent', async () => {
    await openVoiceFlow(options, {
      selectExperience: async () => ({ kind: 'enhanced-agent', localTextGeneration, speechInput }),
    });

    expect(openAgent).toHaveBeenCalledWith(expect.objectContaining({
      ...options,
      localTextGeneration,
      speechInput,
      onUseBasic: expect.any(Function),
    }));
    expect(openLegacy).not.toHaveBeenCalled();
  });

  it('keeps supported-but-not-ready state in enhanced UI with only an explicit basic escape', async () => {
    await openVoiceFlow(options, {
      selectExperience: async () => ({
        kind: 'enhanced-not-ready',
        localTextGeneration,
        speechInput,
        aiStatus: { state: 'preparing' },
        speechStatus: { state: 'ready' },
        reason: 'ai',
      }),
    });

    expect(openNotReady).toHaveBeenCalledWith(expect.objectContaining({ onUseBasic: expect.any(Function) }));
    expect(openLegacy).not.toHaveBeenCalled();

    openNotReady.mock.calls[0][0].onUseBasic();
    await vi.waitFor(() => expect(openLegacy).toHaveBeenCalledWith(options));
  });
});
