import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { deterministicVoiceCommandInterpreter } from './deterministic-interpreter.js';

const createLocalAiInterpreterMock = vi.hoisted(() => vi.fn());

vi.mock('./local-ai-interpreter.js', () => ({
  createLocalAiVoiceCommandInterpreter: createLocalAiInterpreterMock,
}));

import {
  normalizeVoiceInterpreterFailure,
  prepareVoiceCommandInterpreterForTurn,
  selectVoiceCommandInterpreterForTurn,
} from './interpreter-selection.js';
import { LocalTextGenerationError } from '../platform/local-text-generation.js';

function capabilityWithStatus(
  status: Awaited<ReturnType<LocalTextGenerationCapability['status']>>,
): LocalTextGenerationCapability {
  return {
    status: vi.fn().mockResolvedValue(status),
    prepare: vi.fn().mockResolvedValue(undefined),
    generate: vi.fn(),
  };
}

function runtimeWith(capability: LocalTextGenerationCapability | null) {
  return { capability: vi.fn().mockReturnValue(capability) };
}

beforeEach(() => {
  createLocalAiInterpreterMock.mockReset();
});

describe('selectVoiceCommandInterpreterForTurn', () => {
  it('selects deterministic interpretation when the capability is absent', async () => {
    const runtime = runtimeWith(null);

    await expect(selectVoiceCommandInterpreterForTurn({ runtime, locale: 'en' })).resolves.toEqual({
      kind: 'interpreter',
      provider: 'deterministic',
      availability: { state: 'absent' },
      interpreter: deterministicVoiceCommandInterpreter,
    });
  });

  it('selects deterministic interpretation for an explicitly unsupported capability', async () => {
    const capability = capabilityWithStatus({ state: 'unsupported', reason: 'device' });

    await expect(selectVoiceCommandInterpreterForTurn({ runtime: runtimeWith(capability), locale: 'en' }))
      .resolves.toEqual({
        kind: 'interpreter',
        provider: 'deterministic',
        availability: { state: 'unsupported' },
        interpreter: deterministicVoiceCommandInterpreter,
      });
  });

  it('selects deterministic interpretation for a locale outside the current AI product contract', async () => {
    const runtime = runtimeWith(capabilityWithStatus({ state: 'ready', maximumOutputTokens: 96 }));

    const selection = await selectVoiceCommandInterpreterForTurn({ runtime, locale: 'de' });

    expect(selection).toMatchObject({
      kind: 'interpreter',
      provider: 'deterministic',
      availability: { state: 'locale-unsupported' },
    });
    expect(runtime.capability).not.toHaveBeenCalled();
  });

  it('binds the selected ready capability and locale into local AI without performing inference', async () => {
    const interpreter = { interpret: vi.fn() };
    const capability = capabilityWithStatus({ state: 'ready', maximumOutputTokens: 96 });
    createLocalAiInterpreterMock.mockReturnValue(interpreter);

    const selection = await selectVoiceCommandInterpreterForTurn({
      runtime: runtimeWith(capability),
      locale: 'en',
    });

    expect(selection).toEqual({
      kind: 'interpreter',
      provider: 'local-ai',
      availability: { state: 'ready' },
      interpreter,
    });
    expect(createLocalAiInterpreterMock).toHaveBeenCalledWith({ capability, locale: 'en' });
    expect(capability.status).toHaveBeenCalledTimes(1);
    expect(capability.prepare).not.toHaveBeenCalled();
    expect(capability.generate).not.toHaveBeenCalled();
    expect(interpreter.interpret).not.toHaveBeenCalled();
  });

  it.each([
    [{ state: 'action-required', action: 'download' } as const],
    [{ state: 'preparing', downloadedBytes: 12, totalBytes: 24 } as const],
    [{ state: 'temporarily-unavailable', reason: 'provider' } as const],
  ])('keeps supported but not-ready status in the enhanced product state: %o', async (status) => {
    const capability = capabilityWithStatus(status);

    const selection = await selectVoiceCommandInterpreterForTurn({
      runtime: runtimeWith(capability),
      locale: 'en',
    });

    expect(selection).toEqual({ kind: 'enhanced-not-ready', availability: status });
    expect(createLocalAiInterpreterMock).not.toHaveBeenCalled();
    expect(capability.prepare).not.toHaveBeenCalled();
    expect(capability.generate).not.toHaveBeenCalled();
  });

  it('honors an already-aborted turn before capability lookup', async () => {
    const runtime = runtimeWith(null);
    const controller = new AbortController();
    controller.abort();

    await expect(selectVoiceCommandInterpreterForTurn({
      runtime,
      locale: 'en',
      signal: controller.signal,
    })).rejects.toMatchObject({ code: 'cancelled' });
    expect(runtime.capability).not.toHaveBeenCalled();
  });

  it('propagates a capability status failure without selecting deterministic as a fallback', async () => {
    const capability = capabilityWithStatus({ state: 'ready', maximumOutputTokens: 96 });
    const failure = new LocalTextGenerationError('busy');
    vi.mocked(capability.status).mockRejectedValue(failure);

    await expect(selectVoiceCommandInterpreterForTurn({
      runtime: runtimeWith(capability),
      locale: 'en',
    })).rejects.toBe(failure);
    expect(createLocalAiInterpreterMock).not.toHaveBeenCalled();
    expect(capability.generate).not.toHaveBeenCalled();
  });
});

describe('selection support operations', () => {
  it('delegates explicit setup through the existing bounded preparation operation', async () => {
    const capability = capabilityWithStatus({ state: 'action-required', action: 'download' });
    const runtime = runtimeWith(capability);

    await prepareVoiceCommandInterpreterForTurn({ runtime, locale: 'en' });

    expect(runtime.capability).toHaveBeenCalledWith('local-text-generation');
    expect(capability.prepare).toHaveBeenCalledWith(expect.objectContaining({ userInitiated: true }));
    expect(capability.generate).not.toHaveBeenCalled();
  });

  it('normalizes provider failures without adding them to the common interpreter contract', () => {
    expect(normalizeVoiceInterpreterFailure(new LocalTextGenerationError('busy'))).toEqual({
      code: 'busy',
      recoverable: true,
    });
    expect(normalizeVoiceInterpreterFailure(new LocalTextGenerationError('output_rejected'))).toEqual({
      code: 'invalid-output',
      recoverable: false,
    });
  });
});
