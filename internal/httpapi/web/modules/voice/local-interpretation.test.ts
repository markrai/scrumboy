import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  VOICE_INTERPRETATION_INSTRUCTIONS,
  VOICE_INTERPRETATION_LIMITS,
  getVoiceInterpretationAvailability,
  interpretVoiceCommand,
  parseVoiceInterpretationEnvelope,
  prepareVoiceInterpretation,
} from './local-interpretation.js';
import type {
  LocalTextGenerationCapability,
  LocalTextGenerationStatus,
} from '../platform/local-text-generation.js';
import {
  getBrowserRuntimeForTests,
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from '../platform/runtime.js';

function capability(
  status: LocalTextGenerationStatus = { state: 'ready', maximumOutputTokens: 256 },
  output = '{"command":"move todo login to testing"}',
): LocalTextGenerationCapability & {
  status: ReturnType<typeof vi.fn>;
  prepare: ReturnType<typeof vi.fn>;
  generate: ReturnType<typeof vi.fn>;
} {
  return {
    status: vi.fn().mockResolvedValue(status),
    prepare: vi.fn().mockResolvedValue(undefined),
    generate: vi.fn().mockImplementation(async ({ requestId }) => ({ requestId, text: output })),
  };
}

describe('VoiceFlow local interpretation contract', () => {
  afterEach(() => resetAppRuntimeForTests());

  it('discovers local interpretation only through the runtime capability seam', async () => {
    const local = capability();
    const browser = getBrowserRuntimeForTests();
    installAppRuntime({
      ...browser,
      capability: (() => local) as AppRuntime['capability'],
    });

    await expect(getVoiceInterpretationAvailability({ locale: 'en' }))
      .resolves.toMatchObject({ state: 'ready' });
    expect(local.status).toHaveBeenCalledTimes(1);
  });

  it('treats the browser runtime registry absence as normal without querying a provider', async () => {
    const browser = getBrowserRuntimeForTests();
    const capabilityLookup = vi.fn(browser.capability.bind(browser));
    installAppRuntime({
      ...browser,
      capability: capabilityLookup as AppRuntime['capability'],
    });

    expect(await getVoiceInterpretationAvailability({ locale: 'en' }))
      .toEqual({ state: 'absent' });
    expect(capabilityLookup).toHaveBeenCalledWith('local-text-generation');

    const local = capability();
    expect(await getVoiceInterpretationAvailability({ capability: local, locale: 'de' }))
      .toEqual({ state: 'locale-unsupported' });
    expect(local.status).not.toHaveBeenCalled();
  });

  it('reports shared capability status without preparing automatically', async () => {
    const local = capability({ state: 'action-required', action: 'download' });
    expect(await getVoiceInterpretationAvailability({ capability: local, locale: 'en' }))
      .toEqual({ state: 'action-required', action: 'download' });
    expect(local.status).toHaveBeenCalledTimes(1);
    expect(local.prepare).not.toHaveBeenCalled();
    expect(local.generate).not.toHaveBeenCalled();
  });

  it('prepares only a user-selected download action', async () => {
    const download = capability({ state: 'action-required', action: 'download' });
    await prepareVoiceInterpretation({ capability: download, locale: 'en' });
    expect(download.prepare).toHaveBeenCalledWith({ userInitiated: true, signal: undefined });

    for (const action of ['enable', 'system-update'] as const) {
      const unavailable = capability({ state: 'action-required', action });
      await expect(prepareVoiceInterpretation({ capability: unavailable, locale: 'en' }))
        .rejects.toMatchObject({ code: action === 'enable' ? 'disabled' : 'not_ready' });
      expect(unavailable.prepare).not.toHaveBeenCalled();
    }
  });

  it('sends only the bounded transcript and constant contract after explicit invocation', async () => {
    const local = capability({ state: 'ready', maximumOutputTokens: 72 });
    await expect(interpretVoiceCommand({
      transcript: '  Could you move login over to testing?  ',
      capability: local,
      locale: 'en',
      requestIdFactory: () => 'voice-request-1',
    })).resolves.toEqual({ kind: 'candidate', command: 'move todo login to testing' });

    expect(local.status).toHaveBeenCalledTimes(1);
    expect(local.generate).toHaveBeenCalledWith({
      requestId: 'voice-request-1',
      input: 'Could you move login over to testing?',
      instructions: VOICE_INTERPRETATION_INSTRUCTIONS,
      maximumOutputTokens: 72,
      signal: undefined,
    });
    expect(Object.keys(local.generate.mock.calls[0][0]).sort())
      .toEqual(['input', 'instructions', 'maximumOutputTokens', 'requestId', 'signal']);
  });

  it('caps output tokens below the provider maximum', async () => {
    const local = capability({ state: 'ready', maximumOutputTokens: 256 });
    await interpretVoiceCommand({ transcript: 'move login to testing please', capability: local, locale: 'en' });
    expect(local.generate.mock.calls[0][0].maximumOutputTokens)
      .toBe(VOICE_INTERPRETATION_LIMITS.maximumOutputTokens);
  });

  it('creates unique bounded non-secret request identities', async () => {
    const local = capability();
    await interpretVoiceCommand({ transcript: 'move login to testing', capability: local, locale: 'en' });
    await interpretVoiceCommand({ transcript: 'move login to testing', capability: local, locale: 'en' });
    const requestIds = local.generate.mock.calls.map(([request]) => request.requestId as string);

    expect(new Set(requestIds).size).toBe(2);
    expect(requestIds.every((requestId) => requestId.length <= 128)).toBe(true);
    expect(requestIds.join(' ')).not.toContain('login');
  });

  it('accepts only the exact canonical envelope and explicit refusal', () => {
    expect(parseVoiceInterpretationEnvelope('{"command":"open story 56"}'))
      .toEqual({ kind: 'candidate', command: 'open story 56' });
    expect(parseVoiceInterpretationEnvelope('{"command":null}'))
      .toEqual({ kind: 'refused' });
  });

  it('accepts a whole-output JSON code fence from the physical provider', async () => {
    const local = capability(undefined, '```json\n{\n  "command": "move todo bogus to done"\n}\n```');

    await expect(interpretVoiceCommand({
      transcript: 'Could you put bogus in done?',
      capability: local,
      locale: 'en',
    })).resolves.toEqual({ kind: 'candidate', command: 'move todo bogus to done' });
  });

  it('accepts a whole-output unlabelled code fence', () => {
    expect(parseVoiceInterpretationEnvelope('```\n{"command":"move todo bogus to done"}\n```'))
      .toEqual({ kind: 'candidate', command: 'move todo bogus to done' });
  });

  it.each([
    ['text language tag', '```text\n{"command":"open story 56"}\n```'],
    ['JavaScript language tag', '```javascript\n{"command":"open story 56"}\n```'],
    ['prose before a fence', 'Here is the command:\n```json\n{"command":"open story 56"}\n```'],
    ['prose after a fence', '```json\n{"command":"open story 56"}\n```\nDone'],
    ['multiple fenced blocks', '```json\n{"command":"open story 56"}\n```\n```\n{"command":"open story 57"}\n```'],
    ['malformed fenced JSON', '```json\n{"command":\n```'],
    ['an extra fenced envelope field', '```\n{"command":"open story 56","intent":"open"}\n```'],
    ['an empty fenced command', '```json\n{"command":""}\n```'],
    ['a fenced tool-like candidate', '```\n{"command":"callMcpTool todos.delete"}\n```'],
    [
      'fenced raw output beyond the envelope bound',
      `\`\`\`json\n${' '.repeat(VOICE_INTERPRETATION_LIMITS.envelopeCodeUnits)}\n\`\`\``,
    ],
  ])('rejects %s without bypassing envelope validation', (_name, output) => {
    expect(() => parseVoiceInterpretationEnvelope(output))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    ['prose', 'Here is the command: {"command":"open story 56"}'],
    ['extra key', '{"command":"open story 56","intent":"open"}'],
    ['multiple lines', '{"command":"open story 56\\nmove story 56 to done"}'],
    ['URL', '{"command":"open https://example.test"}'],
    ['tool name', '{"command":"callMcpTool todos.list"}'],
    ['nested JSON', '{"command":"open {\\"id\\":56}"}'],
    ['array', '[{"command":"open story 56"}]'],
    ['empty', '{"command":""}'],
    ['oversized candidate', `{"command":"${'x'.repeat(VOICE_INTERPRETATION_LIMITS.candidateCodeUnits + 1)}"}`],
    ['oversized envelope', `{"command":null}${' '.repeat(VOICE_INTERPRETATION_LIMITS.envelopeCodeUnits)}`],
  ])('rejects %s output before deterministic parsing', (_name, output) => {
    expect(() => parseVoiceInterpretationEnvelope(output))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('rejects numeric identifiers invented by the model', async () => {
    const local = capability(undefined, '{"command":"open todo 56"}');
    await expect(interpretVoiceCommand({ transcript: 'open the login card', capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'output_rejected' });
  });

  it.each([
    ['invented status', 'Could you move login?', '{"command":"move todo login to done"}'],
    ['invented person', 'Assign login please', '{"command":"assign todo login to Ada"}'],
    ['multiple commands', 'Open and delete todo 56', '{"command":"open todo 56; delete todo 56"}'],
    ['unsupported intent', 'Rename login', '{"command":"rename todo login"}'],
  ])('rejects %s even when the envelope shape is valid', async (_name, transcript, output) => {
    const local = capability(undefined, output);
    await expect(interpretVoiceCommand({ transcript, capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'output_rejected' });
  });

  it('rejects malformed, multiline, and oversized transcript input before generation', async () => {
    const local = capability();
    await expect(interpretVoiceCommand({ transcript: 'first\nsecond', capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'invalid_request' });
    await expect(interpretVoiceCommand({ transcript: 'x'.repeat(261), capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'input_too_large' });
    expect(local.generate).not.toHaveBeenCalled();
  });

  it.each([
    [{ state: 'temporarily-unavailable', reason: 'busy' }, 'busy'],
    [{ state: 'temporarily-unavailable', reason: 'quota' }, 'quota_exceeded'],
    [{ state: 'temporarily-unavailable', reason: 'foreground' }, 'foreground_required'],
    [{ state: 'temporarily-unavailable', reason: 'storage' }, 'insufficient_storage'],
    [{ state: 'preparing' }, 'not_ready'],
    [{ state: 'unsupported', reason: 'device' }, 'unsupported'],
  ] as Array<[LocalTextGenerationStatus, string]>)('normalizes unavailable status %#', async (status, code) => {
    const local = capability(status);
    await expect(interpretVoiceCommand({ transcript: 'move login please', capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code });
    expect(local.generate).not.toHaveBeenCalled();
  });

  it('normalizes provider failures and cancellation without exposing provider messages', async () => {
    const broken = capability();
    broken.generate.mockRejectedValue(new Error('provider prompt contained a secret'));
    await expect(interpretVoiceCommand({ transcript: 'move login please', capability: broken, locale: 'en' }))
      .rejects.toMatchObject({ code: 'internal', message: 'Local text generation failed' });

    const controller = new AbortController();
    controller.abort();
    await expect(interpretVoiceCommand({
      transcript: 'move login please',
      capability: capability(),
      locale: 'en',
      signal: controller.signal,
    })).rejects.toMatchObject({ code: 'cancelled' });
  });

  it('rejects a mismatched provider request identity', async () => {
    const local = capability();
    local.generate.mockResolvedValue({ requestId: 'stale', text: '{"command":"move todo login to testing"}' });
    await expect(interpretVoiceCommand({
      transcript: 'move login please',
      capability: local,
      locale: 'en',
      requestIdFactory: () => 'current',
    })).rejects.toMatchObject({ code: 'output_rejected' });
  });
});
