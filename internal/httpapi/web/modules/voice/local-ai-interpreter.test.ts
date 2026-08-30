import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LocalTextGenerationError, type LocalTextGenerationCapability } from '../platform/local-text-generation.js';

const interpretVoiceCommandMock = vi.hoisted(() => vi.fn());

vi.mock('./local-interpretation.js', () => ({
  interpretVoiceCommand: interpretVoiceCommandMock,
}));

import { createLocalAiVoiceCommandInterpreter } from './local-ai-interpreter.js';

function capability(): LocalTextGenerationCapability {
  return {
    status: vi.fn(),
    prepare: vi.fn(),
    generate: vi.fn(),
  };
}

beforeEach(() => {
  interpretVoiceCommandMock.mockReset();
});

describe('local-AI VoiceFlow interpreter', () => {
  it('delegates unchanged input and bound dependencies to the existing local interpretation operation', async () => {
    const local = capability();
    const controller = new AbortController();
    const input = '  Could you move bogus to done?  ';
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'candidate',
      command: 'move todo bogus to done',
    });

    const interpreter = createLocalAiVoiceCommandInterpreter({
      capability: local,
      locale: 'en',
    });

    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    await expect(interpreter.interpret(input, { signal: controller.signal })).resolves.toEqual({
      kind: 'candidate',
      command: 'move todo bogus to done',
    });
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
    expect(interpretVoiceCommandMock).toHaveBeenCalledWith({
      transcript: input,
      capability: local,
      locale: 'en',
      signal: controller.signal,
    });
  });

  it('maps semantic-gap refusal to the established unsupported command failure without deterministic parsing', async () => {
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'refused' });
    const interpreter = createLocalAiVoiceCommandInterpreter({
      capability: capability(),
      locale: 'en',
    });

    await expect(interpreter.interpret('Create a to-do about cleaning the garage today')).resolves.toMatchObject({
      kind: 'unsupported',
      failure: {
        ok: false,
        code: 'unsupported',
        message: 'Unsupported command.',
      },
    });
    expect(interpretVoiceCommandMock).toHaveBeenCalledWith(expect.objectContaining({
      transcript: 'Create a to-do about cleaning the garage today',
    }));
  });

  it('preserves operational provider errors instead of manufacturing command failures', async () => {
    const providerError = new LocalTextGenerationError('busy');
    interpretVoiceCommandMock.mockRejectedValue(providerError);
    const interpreter = createLocalAiVoiceCommandInterpreter({
      capability: capability(),
      locale: 'en',
    });

    await expect(interpreter.interpret('Could you move bogus to done?')).rejects.toBe(providerError);
  });
});
