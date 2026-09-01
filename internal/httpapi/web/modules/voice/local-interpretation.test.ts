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

function envelope(intent: unknown, unrepresented: unknown[] = []): string {
  return JSON.stringify({ intent, unrepresented });
}

function capability(
  status: LocalTextGenerationStatus = { state: 'ready', maximumOutputTokens: 256 },
  output = envelope({
    kind: 'move-todo',
    target: { kind: 'title', text: 'login' },
    destination: { kind: 'name', text: 'testing' },
  }),
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

describe('VoiceFlow local semantic interpretation contract', () => {
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
    expect(local.status).toHaveBeenCalledOnce();
  });

  it('treats capability absence and unsupported locales as normal fallback conditions', async () => {
    const browser = getBrowserRuntimeForTests();
    const capabilityLookup = vi.fn(browser.capability.bind(browser));
    installAppRuntime({
      ...browser,
      capability: capabilityLookup as AppRuntime['capability'],
    });

    await expect(getVoiceInterpretationAvailability({ locale: 'en' }))
      .resolves.toEqual({ state: 'absent' });
    expect(capabilityLookup).toHaveBeenCalledWith('local-text-generation');

    const local = capability();
    await expect(getVoiceInterpretationAvailability({ capability: local, locale: 'de' }))
      .resolves.toEqual({ state: 'locale-unsupported' });
    expect(local.status).not.toHaveBeenCalled();
  });

  it('prepares only an explicitly selected provider download', async () => {
    const download = capability({ state: 'action-required', action: 'download' });
    await prepareVoiceInterpretation({ capability: download, locale: 'en' });
    expect(download.prepare).toHaveBeenCalledWith({ userInitiated: true, signal: undefined });

    const disabled = capability({ state: 'action-required', action: 'enable' });
    await expect(prepareVoiceInterpretation({ capability: disabled, locale: 'en' }))
      .rejects.toMatchObject({ code: 'disabled' });
    expect(disabled.prepare).not.toHaveBeenCalled();
  });

  it('sends the versioned v6 semantic contract after explicit invocation', async () => {
    const local = capability({ state: 'ready', maximumOutputTokens: 144 });
    await expect(interpretVoiceCommand({
      transcript: '  Could you move login over to testing?  ',
      capability: local,
      locale: 'en',
      requestIdFactory: () => 'voice-request-1',
    })).resolves.toEqual({
      kind: 'semantic',
      intent: {
        kind: 'move-todo',
        target: { kind: 'title', text: 'login' },
        destination: { kind: 'name', text: 'testing' },
      },
    });

    expect(VOICE_INTERPRETATION_PROMPT_VERSION).toBe('voice-semantic-v6');
    expect(local.generate).toHaveBeenCalledWith({
      requestId: 'voice-request-1',
      input: 'Could you move login over to testing?',
      instructions: VOICE_INTERPRETATION_INSTRUCTIONS,
      maximumOutputTokens: 144,
      signal: undefined,
    });
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Contract: voice-semantic-v6.');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Do not translate the request into a textual command');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Move Fixed Radical Login to done');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Assign Bogus to Mark Rai');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Open and delete Bogus');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Never emit projectId');
  });

  it.each([
    [
      'Create story called fixed radical login',
      { kind: 'create-todo', title: 'Fix the radical login' },
    ],
    [
      'Open Fixed Radical Login',
      { kind: 'open-todo', target: { kind: 'title', text: 'Fixed Radical Login' } },
    ],
    [
      'Move Fixed Radical Login to done',
      {
        kind: 'move-todo',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        destination: { kind: 'name', text: 'done' },
      },
    ],
    [
      'Assign Bogus to Mark Rai',
      {
        kind: 'assign-todo',
        target: { kind: 'title', text: 'Bogus' },
        assignee: { kind: 'name', text: 'Mark Rai' },
      },
    ],
    [
      'Delete Bogus',
      { kind: 'delete-todo', target: { kind: 'title', text: 'Bogus' } },
    ],
    [
      'Change its title to Fix the login race condition',
      {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
    ],
    [
      'Rename Fixed Radical Login to Fixed Login',
      {
        kind: 'update-todo-title',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        title: 'Fixed Login',
      },
    ],
  ])('accepts a strict semantic action for %s', (transcript, intent) => {
    expect(parseVoiceInterpretationEnvelope(envelope(intent), transcript))
      .toEqual({ kind: 'semantic', intent });
  });

  it('accepts an explicit local ID only when the transcript supplied it', () => {
    const intent = {
      kind: 'move-todo',
      target: { kind: 'local-id', localId: 355 },
      destination: { kind: 'name', text: 'done' },
    };
    expect(parseVoiceInterpretationEnvelope(envelope(intent), 'Move story number 355 to done'))
      .toEqual({ kind: 'semantic', intent });
    expect(() => parseVoiceInterpretationEnvelope(envelope(intent), 'Move Bogus to done'))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    [
      'renamed todo',
      'Move Fixed Radical Login to done',
      {
        kind: 'move-todo',
        target: { kind: 'title', text: 'Radical Login Fix' },
        destination: { kind: 'name', text: 'done' },
      },
    ],
    [
      'invented lane',
      'Move Bogus somewhere',
      {
        kind: 'move-todo',
        target: { kind: 'title', text: 'Bogus' },
        destination: { kind: 'name', text: 'done' },
      },
    ],
    [
      'invented member',
      'Assign Bogus please',
      {
        kind: 'assign-todo',
        target: { kind: 'title', text: 'Bogus' },
        assignee: { kind: 'name', text: 'Mark Rai' },
      },
    ],
  ])('rejects existing-entity source violation: %s', (_name, transcript, intent) => {
    expect(() => parseVoiceInterpretationEnvelope(envelope(intent), transcript))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('requires an explicit contextual phrase for current outside pending-slot context', () => {
    const intent = {
      kind: 'open-todo',
      target: { kind: 'current' },
    };
    expect(parseVoiceInterpretationEnvelope(envelope(intent), 'Open it'))
      .toEqual({ kind: 'semantic', intent });
    expect(() => parseVoiceInterpretationEnvelope(envelope(intent), 'Open Bogus'))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('allows update-title to identify an implicit current target on the first turn', () => {
    const intent = {
      kind: 'update-todo-title',
      target: { kind: 'current' },
      title: null,
    };
    expect(parseVoiceInterpretationEnvelope(envelope(intent), 'Change the title'))
      .toEqual({ kind: 'semantic', intent });
  });

  it.each([
    [
      'Change its title to Fix the login race condition',
      {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
      true,
    ],
    [
      'Rename this todo to Fix the login race condition',
      {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
      true,
    ],
    [
      'Rename Fixed Radical Login to Fixed Login',
      {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fixed Login',
      },
      false,
    ],
    [
      'Rename Fixed Radical Login to Fixed Login',
      {
        kind: 'update-todo-title',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        title: 'Fixed Login',
      },
      true,
    ],
    [
      'Rename story 355 to Fixed Login',
      {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fixed Login',
      },
      false,
    ],
    [
      'Rename story 355 to Fixed Login',
      {
        kind: 'update-todo-title',
        target: { kind: 'local-id', localId: 355 },
        title: 'Fixed Login',
      },
      true,
    ],
  ])('enforces update-title target provenance for %s', (transcript, intent, accepted) => {
    const parse = () => parseVoiceInterpretationEnvelope(envelope(intent), transcript);
    if (accepted) {
      expect(parse()).toEqual({ kind: 'semantic', intent });
    } else {
      expect(parse).toThrow(expect.objectContaining({ code: 'output_rejected' }));
    }
  });

  it('uses only bounded pending-slot context for a replacement-title answer', async () => {
    const local = capability(undefined, envelope({
      kind: 'update-todo-title',
      target: { kind: 'current' },
      title: 'Fix the login race condition',
    }));
    const conversation = {
      pending: { action: 'todo.update_title' as const, slot: 'title' as const },
    };

    await expect(interpretVoiceCommand({
      transcript: 'Fix the login race condition',
      capability: local,
      locale: 'en',
      conversation,
    })).resolves.toEqual({
      kind: 'semantic',
      intent: {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
    });
    const request = local.generate.mock.calls[0][0];
    expect(request.instructions).toContain('waiting only for the replacement title');
    expect(JSON.stringify(request)).not.toContain('553');
  });

  it('does not let pending title context erase an explicitly supplied todo target', () => {
    const conversation = {
      pending: { action: 'todo.update_title' as const, slot: 'title' as const },
    };
    expect(() => parseVoiceInterpretationEnvelope(envelope({
      kind: 'update-todo-title',
      target: { kind: 'current' },
      title: 'Fixed Login',
    }), 'Rename Fixed Radical Login to Fixed Login', conversation))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    ['extra envelope field', { intent: null, unrepresented: [], reason: 'no' }],
    ['unknown action', { intent: { kind: 'archive-todo', target: { kind: 'current' } }, unrepresented: [] }],
    ['extra intent field', { intent: { kind: 'open-todo', target: { kind: 'current' }, projectId: 1 }, unrepresented: [] }],
    ['internal lane key', {
      intent: {
        kind: 'move-todo',
        target: { kind: 'title', text: 'Bogus' },
        destination: { kind: 'name', text: 'done', columnKey: 'done' },
      },
      unrepresented: [],
    }],
    ['member ID', {
      intent: {
        kind: 'assign-todo',
        target: { kind: 'title', text: 'Bogus' },
        assignee: { kind: 'name', text: 'Mark', userId: 7 },
      },
      unrepresented: [],
    }],
  ])('rejects strict protocol violation: %s', (_name, output) => {
    expect(() => parseVoiceInterpretationEnvelope(
      JSON.stringify(output),
      'Move Bogus to done and assign it to Mark',
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('fails closed when exact unsupported residue remains', () => {
    const intent = { kind: 'create-todo', title: 'Fix the bathroom' };
    expect(parseVoiceInterpretationEnvelope(
      envelope(intent, ['by 6 PM']),
      'Create a todo to fix the bathroom by 6 PM',
    )).toEqual({ kind: 'refused' });
    expect(parseVoiceInterpretationEnvelope(
      envelope(null, ['Open and delete Bogus']),
      'Open and delete Bogus',
    )).toEqual({ kind: 'refused' });
  });

  it('accepts only a whole-output JSON fence', () => {
    const output = envelope({ kind: 'delete-todo', target: { kind: 'title', text: 'Bogus' } });
    expect(parseVoiceInterpretationEnvelope(`\`\`\`json\n${output}\n\`\`\``, 'Delete Bogus'))
      .toMatchObject({ kind: 'semantic' });
    expect(() => parseVoiceInterpretationEnvelope(`Result:\n\`\`\`json\n${output}\n\`\`\``, 'Delete Bogus'))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('bounds transcript, envelope, output tokens, and provider request identity', async () => {
    const oversizedTranscript = 'x'.repeat(VOICE_INTERPRETATION_LIMITS.transcriptCodeUnits + 1);
    const local = capability();
    await expect(interpretVoiceCommand({
      transcript: oversizedTranscript,
      capability: local,
      locale: 'en',
    })).rejects.toMatchObject({ code: 'input_too_large' });
    expect(local.generate).not.toHaveBeenCalled();

    expect(() => parseVoiceInterpretationEnvelope(
      `${envelope(null)}${' '.repeat(VOICE_INTERPRETATION_LIMITS.envelopeCodeUnits)}`,
      'No action',
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));

    const stale = capability();
    stale.generate.mockResolvedValue({ requestId: 'stale', text: envelope(null) });
    await expect(interpretVoiceCommand({
      transcript: 'No action',
      capability: stale,
      locale: 'en',
      requestIdFactory: () => 'owned',
    })).rejects.toMatchObject({ code: 'output_rejected' });
  });
});
