import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  VOICE_INTERPRETATION_INSTRUCTIONS,
  VOICE_INTERPRETATION_LIMITS,
  VOICE_INTERPRETATION_PROMPT_VERSION,
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
  output = '{"command":"move todo login to testing","conversation":null,"unrepresented":[]}',
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

  it('sends the production conversational-v5 contract to generation after explicit invocation', async () => {
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
    expect(local.generate.mock.calls[0][0].instructions)
      .toBe(VOICE_INTERPRETATION_INSTRUCTIONS);
    expect(local.generate.mock.calls[0][0].instructions)
      .toContain('Contract: voice-command-conversational-v5.');
    expect(Object.keys(local.generate.mock.calls[0][0]).sort())
      .toEqual(['input', 'instructions', 'maximumOutputTokens', 'requestId', 'signal']);
  });

  it('keeps the promoted v3/v4 language rules while adding only bounded current-title interpretation', () => {
    expect(VOICE_INTERPRETATION_PROMPT_VERSION).toBe('voice-command-conversational-v5');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Scrumboy, a task and kanban application');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('A todo is a task card');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('speech-transcribed English');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Do not rely on colons, commas, periods, quotation marks, capitalization');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('normalize the user-authored content into a concise natural task title');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Create a to-do called fix the flux capacitor in the bathroom');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":"create todo Fix the flux capacitor in the bathroom","conversation":null,"unrepresented":[]}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":"create todo Buy milk and eggs","conversation":null,"unrepresented":[]}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('preserve identity');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('CRITICAL SEMANTIC COMPLETENESS RULE');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Count intended Scrumboy actions, not conjunction words');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('If the user requests two or more actions, do not choose');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":null,"conversation":null,"unrepresented":["Open and delete Bogus"]}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"command":"create todo Fix the bathroom","conversation":null,"unrepresented":["by 6:00 p.m."]}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Never invent or rename authoritative domain entities');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('"Open it"');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('"Open this todo"');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('"Open this card"');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('"Open that one"');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"action":"open_todo","target":"current"}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"action":"update_title","target":"current","title":null}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('{"action":"update_title","target":"current","title":"Fix the login race condition"}');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('never provide, infer, copy, reconstruct, or guess its ID');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('"Open Bogus" returns command "open todo Bogus" with conversation null');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('"Open todo 553" returns command "open todo 553" with conversation null');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS)
      .toContain('Command and conversation must never both be non-null');
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
    expect(parseVoiceInterpretationEnvelope(
      '{"command":"open story 56","conversation":null,"unrepresented":[]}',
      'open story 56',
    ))
      .toEqual({ kind: 'candidate', command: 'open story 56' });
    expect(parseVoiceInterpretationEnvelope(
      '{"command":null,"conversation":null,"unrepresented":[]}',
      'Please do something ambiguous',
    ))
      .toEqual({ kind: 'refused' });
  });

  it('accepts only the bounded open-current conversational result', () => {
    expect(parseVoiceInterpretationEnvelope(
      '{"command":null,"conversation":{"action":"open_todo","target":"current"},"unrepresented":[]}',
      'Open it',
    )).toEqual({
      kind: 'conversation',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
  });

  it('accepts only bounded current-title results with a missing or authored title', () => {
    expect(parseVoiceInterpretationEnvelope(
      '{"command":null,"conversation":{"action":"update_title","target":"current","title":null},"unrepresented":[]}',
      'Change the title',
    )).toEqual({
      kind: 'conversation',
      intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
    });
    expect(parseVoiceInterpretationEnvelope(
      '{"command":null,"conversation":{"action":"update_title","target":"current","title":"  Fix the login race condition  "},"unrepresented":[]}',
      'Change its title to Fix the login race condition',
    )).toEqual({
      kind: 'conversation',
      intent: {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
    });
  });

  it('sends only bounded pending-slot context in the v5 instructions', async () => {
    const local = capability(undefined, JSON.stringify({
      command: null,
      conversation: { action: 'update_title', target: 'current', title: 'Fix the login race condition' },
      unrepresented: [],
    }));

    await expect(interpretVoiceCommand({
      transcript: 'Make it Fix the login race condition',
      capability: local,
      locale: 'en',
      conversation: {
        pending: { action: 'todo.update_title', slot: 'title' },
      },
    })).resolves.toMatchObject({
      kind: 'conversation',
      intent: { kind: 'update-todo-title', title: 'Fix the login race condition' },
    });
    const request = local.generate.mock.calls[0][0];
    expect(request.instructions).toContain('waiting only for the replacement title');
    expect(Object.keys(request).sort())
      .toEqual(['input', 'instructions', 'maximumOutputTokens', 'requestId', 'signal']);
  });

  it.each([
    { action: 'update_title', target: 'current' },
    { action: 'update_title', target: 'current', title: '', id: 553 },
    { action: 'update_title', target: 553, title: 'New title' },
    { action: 'update_title', target: 'current', title: { value: 'New title' } },
    { action: 'update_title', target: 'current', title: 'x'.repeat(201) },
    { action: 'update_title', target: 'current', title: 'New title', projectSlug: 'alpha' },
  ])('rejects smuggled or invalid title conversation output %#', (conversation) => {
    expect(() => parseVoiceInterpretationEnvelope(
      JSON.stringify({ command: null, conversation, unrepresented: [] }),
      'Change its title',
    )).toThrow();
  });

  it('maps a generated bounded current envelope without canonical entity preservation', async () => {
    const local = capability(undefined, JSON.stringify({
      command: null,
      conversation: { action: 'open_todo', target: 'current' },
      unrepresented: [],
    }));

    await expect(interpretVoiceCommand({
      transcript: 'Open this card',
      capability: local,
      locale: 'en',
    })).resolves.toEqual({
      kind: 'conversation',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    expect(local.generate).toHaveBeenCalledTimes(1);
  });

  it('accepts semantic rewriting only for newly authored create titles', async () => {
    const local = capability(undefined, JSON.stringify({
      command: 'create todo Clean the garage',
      conversation: null,
      unrepresented: [],
    }));

    await expect(interpretVoiceCommand({
      transcript: 'Create a to-do about cleaning the garage',
      capability: local,
      locale: 'en',
    })).resolves.toEqual({ kind: 'candidate', command: 'create todo Clean the garage' });
    expect(local.generate).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['create a todo about calling the dentist', 'create todo Call the dentist'],
    ['add a task for buying milk', 'create todo Buy milk'],
    ['make a todo to clean the garage', 'create todo Clean the garage'],
    ['create a task to email Ada about Scrumboy', 'create todo Email Ada about Scrumboy'],
  ])('accepts bounded authored-title normalization: %s', async (transcript, command) => {
    const local = capability(undefined, JSON.stringify({ command, conversation: null, unrepresented: [] }));

    await expect(interpretVoiceCommand({ transcript, capability: local, locale: 'en' }))
      .resolves.toEqual({ kind: 'candidate', command });
    expect(local.generate).toHaveBeenCalledTimes(1);
  });

  it('refuses a partial create interpretation when meaningful source content is unrepresented', async () => {
    const local = capability(undefined, JSON.stringify({
      command: 'create todo Clean the garage',
      conversation: null,
      unrepresented: ['today'],
    }));

    await expect(interpretVoiceCommand({
      transcript: 'Create a to-do about cleaning the garage today',
      capability: local,
      locale: 'en',
    })).resolves.toEqual({ kind: 'refused' });
    expect(local.generate).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['not an array', '{"command":"create todo Clean the garage","conversation":null,"unrepresented":"today"}', 'Clean the garage today'],
    ['non-string entry', '{"command":"create todo Clean the garage","conversation":null,"unrepresented":[42]}', 'Clean the garage today'],
    [
      'too many entries',
      '{"command":"create todo Clean the garage","conversation":null,"unrepresented":["today","garage","clean","the","todo"]}',
      'Create a todo to clean the garage today',
    ],
    [
      'entry too long',
      JSON.stringify({
        command: 'create todo Clean the garage',
        conversation: null,
        unrepresented: ['x'.repeat(VOICE_INTERPRETATION_LIMITS.unrepresentedItemCodeUnits + 1)],
      }),
      'x'.repeat(VOICE_INTERPRETATION_LIMITS.unrepresentedItemCodeUnits + 1),
    ],
    ['control character', '{"command":"create todo Clean the garage","conversation":null,"unrepresented":["today\\u0000"]}', 'Clean the garage today'],
    [
      'invented explanation',
      '{"command":"create todo Clean the garage","conversation":null,"unrepresented":["before midnight"]}',
      'Clean the garage today',
    ],
    [
      'extra envelope key',
      '{"command":"create todo Clean the garage","conversation":null,"unrepresented":[],"reason":"normalized"}',
      'Clean the garage',
    ],
  ])('rejects invalid unrepresented metadata: %s', (_name, output, transcript) => {
    expect(() => parseVoiceInterpretationEnvelope(output, transcript))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    [
      'rewritten todo and status',
      'Could you move Bogus to Done?',
      'move todo Garage Cleanup to Done',
    ],
    [
      'rewritten status',
      'Could you move Bogus to Done?',
      'move todo Bogus to Completed',
    ],
    [
      'rewritten member',
      'Assign Bogus to Ada',
      'assign todo Bogus to Alice',
    ],
  ])('retains existing-entity preservation for %s', async (_name, transcript, command) => {
    const local = capability(undefined, JSON.stringify({ command, conversation: null, unrepresented: [] }));

    await expect(interpretVoiceCommand({ transcript, capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'output_rejected' });
  });

  it('accepts a whole-output JSON code fence from the physical provider', async () => {
    const local = capability(undefined, '```json\n{\n  "command": "move todo bogus to done",\n  "conversation": null,\n  "unrepresented": []\n}\n```');

    await expect(interpretVoiceCommand({
      transcript: 'Could you put bogus in done?',
      capability: local,
      locale: 'en',
    })).resolves.toEqual({ kind: 'candidate', command: 'move todo bogus to done' });
  });

  it('accepts a whole-output unlabelled code fence', () => {
    expect(parseVoiceInterpretationEnvelope(
      '```\n{"command":"move todo bogus to done","conversation":null,"unrepresented":[]}\n```',
      'Could you move bogus to done?',
    ))
      .toEqual({ kind: 'candidate', command: 'move todo bogus to done' });
  });

  it.each([
    ['text language tag', '```text\n{"command":"open story 56","conversation":null,"unrepresented":[]}\n```'],
    ['JavaScript language tag', '```javascript\n{"command":"open story 56","conversation":null,"unrepresented":[]}\n```'],
    ['prose before a fence', 'Here is the command:\n```json\n{"command":"open story 56","conversation":null,"unrepresented":[]}\n```'],
    ['prose after a fence', '```json\n{"command":"open story 56","conversation":null,"unrepresented":[]}\n```\nDone'],
    ['multiple fenced blocks', '```json\n{"command":"open story 56","conversation":null,"unrepresented":[]}\n```\n```\n{"command":"open story 57","conversation":null,"unrepresented":[]}\n```'],
    ['malformed fenced JSON', '```json\n{"command":\n```'],
    ['an extra fenced envelope field', '```\n{"command":"open story 56","conversation":null,"unrepresented":[],"intent":"open"}\n```'],
    ['an empty fenced command', '```json\n{"command":"","conversation":null,"unrepresented":[]}\n```'],
    ['a fenced tool-like candidate', '```\n{"command":"callMcpTool todos.delete","conversation":null,"unrepresented":[]}\n```'],
    [
      'fenced raw output beyond the envelope bound',
      `\`\`\`json\n${' '.repeat(VOICE_INTERPRETATION_LIMITS.envelopeCodeUnits)}\n\`\`\``,
    ],
  ])('rejects %s without bypassing envelope validation', (_name, output) => {
    expect(() => parseVoiceInterpretationEnvelope(output, 'open story 56'))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    ['prose', 'Here is the command: {"command":"open story 56","conversation":null,"unrepresented":[]}'],
    ['missing conversation', '{"command":"open story 56","unrepresented":[]}'],
    ['missing unrepresented', '{"command":"open story 56","conversation":null}'],
    ['extra key', '{"command":"open story 56","conversation":null,"unrepresented":[],"intent":"open"}'],
    ['multiple lines', '{"command":"open story 56\\nmove story 56 to done","conversation":null,"unrepresented":[]}'],
    ['URL', '{"command":"open https://example.test","conversation":null,"unrepresented":[]}'],
    ['tool name', '{"command":"callMcpTool todos.list","conversation":null,"unrepresented":[]}'],
    ['nested JSON', '{"command":"open {\\"id\\":56}","conversation":null,"unrepresented":[]}'],
    ['array', '[{"command":"open story 56","conversation":null,"unrepresented":[]}]'],
    ['empty', '{"command":"","conversation":null,"unrepresented":[]}'],
    ['oversized candidate', `{"command":"${'x'.repeat(VOICE_INTERPRETATION_LIMITS.candidateCodeUnits + 1)}","conversation":null,"unrepresented":[]}`],
    ['oversized envelope', `{"command":null,"conversation":null,"unrepresented":[]}${' '.repeat(VOICE_INTERPRETATION_LIMITS.envelopeCodeUnits)}`],
  ])('rejects %s output before deterministic parsing', (_name, output) => {
    expect(() => parseVoiceInterpretationEnvelope(output, 'open story 56'))
      .toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    ['unknown action', { action: 'delete_todo', target: 'current' }],
    ['numeric target text', { action: 'open_todo', target: '553' }],
    ['title target text', { action: 'open_todo', target: 'Bogus' }],
    ['identity object target', { action: 'open_todo', target: { localId: 553 } }],
    ['extra field', { action: 'open_todo', target: 'current', localId: 553 }],
    ['array', [{ action: 'open_todo', target: 'current' }]],
    ['string', 'open_current'],
  ])('rejects unauthorized conversational output: %s', (_name, conversation) => {
    expect(() => parseVoiceInterpretationEnvelope(JSON.stringify({
      command: null,
      conversation,
      unrepresented: [],
    }), 'Open it')).toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('rejects command and conversation together', () => {
    expect(() => parseVoiceInterpretationEnvelope(JSON.stringify({
      command: 'open todo 553',
      conversation: { action: 'open_todo', target: 'current' },
      unrepresented: [],
    }), 'Open todo 553')).toThrowError(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('refuses a structurally valid current action with exact unsupported residue', () => {
    expect(parseVoiceInterpretationEnvelope(JSON.stringify({
      command: null,
      conversation: { action: 'open_todo', target: 'current' },
      unrepresented: ['tomorrow'],
    }), 'Open it tomorrow')).toEqual({ kind: 'refused' });
  });

  it('rejects numeric identifiers invented by the model', async () => {
    const local = capability(undefined, '{"command":"open todo 56","conversation":null,"unrepresented":[]}');
    await expect(interpretVoiceCommand({ transcript: 'open the login card', capability: local, locale: 'en' }))
      .rejects.toMatchObject({ code: 'output_rejected' });
  });

  it.each([
    ['invented status', 'Could you move login?', '{"command":"move todo login to done","conversation":null,"unrepresented":[]}'],
    ['invented person', 'Assign login please', '{"command":"assign todo login to Ada","conversation":null,"unrepresented":[]}'],
    ['multiple commands', 'Open and delete todo 56', '{"command":"open todo 56; delete todo 56","conversation":null,"unrepresented":[]}'],
    ['unsupported intent', 'Rename login', '{"command":"rename todo login","conversation":null,"unrepresented":[]}'],
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
    local.generate.mockResolvedValue({ requestId: 'stale', text: '{"command":"move todo login to testing","conversation":null,"unrepresented":[]}' });
    await expect(interpretVoiceCommand({
      transcript: 'move login please',
      capability: local,
      locale: 'en',
      requestIdFactory: () => 'current',
    })).rejects.toMatchObject({ code: 'output_rejected' });
  });
});
