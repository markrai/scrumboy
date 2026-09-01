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

  it('sends the versioned v8 semantic contract after explicit invocation', async () => {
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

    expect(VOICE_INTERPRETATION_PROMPT_VERSION).toBe('voice-semantic-v8');
    expect(local.generate).toHaveBeenCalledWith({
      requestId: 'voice-request-1',
      input: 'Could you move login over to testing?',
      instructions: VOICE_INTERPRETATION_INSTRUCTIONS,
      maximumOutputTokens: 144,
      signal: undefined,
    });
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Contract: voice-semantic-v8.');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Do not translate the request into a textual command');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Move Fixed Radical Login to done');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Assign Bogus to Mark Rai');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Open and delete Bogus');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Never emit projectId');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('Append notes and replace notes are different operations');
    expect(VOICE_INTERPRETATION_INSTRUCTIONS).toContain('count-completed-todos');
  });

  it.each([
    [
      'Number 353',
      { kind: 'select-choice', selector: { kind: 'local-id', localId: 353 } },
      { pending: { kind: 'todo-choice' } } as const,
    ],
    [
      'The one in the backlog',
      { kind: 'select-choice', selector: { kind: 'lane', text: 'backlog' } },
      { pending: { kind: 'todo-choice' } } as const,
    ],
    [
      'The second one',
      { kind: 'select-choice', selector: { kind: 'ordinal', index: 2 } },
      { pending: { kind: 'todo-choice' } } as const,
    ],
    [
      'Ada',
      { kind: 'select-choice', selector: { kind: 'text', text: 'Ada' } },
      { pending: { kind: 'member-choice' } } as const,
    ],
    [
      'backend',
      { kind: 'select-choice', selector: { kind: 'text', text: 'backend' } },
      { pending: { kind: 'tag-choice' } } as const,
    ],
    [
      'Yeah go ahead',
      { kind: 'confirm' },
      { pending: { kind: 'confirmation', operation: 'todo.move' } } as const,
    ],
    [
      'Actually no',
      { kind: 'decline' },
      { pending: { kind: 'confirmation', operation: 'todo.move' } } as const,
    ],
    [
      'Never mind',
      { kind: 'cancel' },
      { pending: { kind: 'missing-slot', operation: 'todo.move', slot: 'destination' } } as const,
    ],
  ])('accepts only a state-bounded dialogue envelope for %s', (transcript, intent, context) => {
    expect(parseVoiceInterpretationEnvelope(envelope(intent), transcript, context)).toEqual({
      kind: 'dialogue',
      intent,
    });
  });

  it.each([
    [
      'I will be there',
      { kind: 'provide-slot', operation: 'todo.append_notes', slot: 'notes', value: 'I will be there' },
      { pending: { kind: 'missing-slot', operation: 'todo.append_notes', slot: 'notes' } } as const,
    ],
    [
      'Done',
      {
        kind: 'provide-slot',
        operation: 'todo.move',
        slot: 'destination',
        value: { kind: 'name', text: 'Done' },
      },
      { pending: { kind: 'missing-slot', operation: 'todo.move', slot: 'destination' } } as const,
    ],
    [
      'Mark',
      {
        kind: 'provide-slot',
        operation: 'todo.assign',
        slot: 'assignee',
        value: { kind: 'name', text: 'Mark' },
      },
      { pending: { kind: 'missing-slot', operation: 'todo.assign', slot: 'assignee' } } as const,
    ],
    [
      'backend',
      {
        kind: 'provide-slot',
        operation: 'todo.add_tag',
        slot: 'tag',
        value: { kind: 'name', text: 'backend' },
      },
      { pending: { kind: 'missing-slot', operation: 'todo.add_tag', slot: 'tag' } } as const,
    ],
  ])('validates a generalized missing-slot response for %s', (transcript, intent, context) => {
    expect(parseVoiceInterpretationEnvelope(envelope(intent), transcript, context)).toEqual({
      kind: 'dialogue',
      intent,
    });
  });

  it('rejects invented IDs and dialogue categories outside their pending state', () => {
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'select-choice', selector: { kind: 'local-id', localId: 353 } }),
      'The first one',
      { pending: { kind: 'todo-choice' } },
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'confirm' }),
      'Yes',
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'confirm' }),
      'Yes',
      { pending: { kind: 'todo-choice' } },
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'provide-slot', operation: 'todo.assign', slot: 'assignee', value: { kind: 'name', text: 'Mark' } }),
      'Mark',
      { pending: { kind: 'missing-slot', operation: 'todo.move', slot: 'destination' } },
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('accepts bounded confirmation corrections and rejects operation replacement', () => {
    expect(parseVoiceInterpretationEnvelope(
      envelope({ kind: 'correct-choice', selector: { kind: 'lane', text: 'Backlog' } }),
      'No use the one in Backlog',
      { pending: { kind: 'confirmation', operation: 'todo.move' } },
    )).toMatchObject({ kind: 'dialogue', intent: { kind: 'correct-choice' } });
    expect(parseVoiceInterpretationEnvelope(
      envelope({
        kind: 'correct-value',
        operation: 'todo.append_notes',
        slot: 'notes',
        value: "I'll arrive at six.",
      }),
      "Actually make the note I'll arrive at six.",
      { pending: { kind: 'confirmation', operation: 'todo.append_notes' } },
    )).toMatchObject({ kind: 'dialogue', intent: { kind: 'correct-value' } });
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'delete-todo', target: { kind: 'current' } }),
      'No delete it instead',
      { pending: { kind: 'confirmation', operation: 'todo.move' } },
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
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

  it.each([
    [
      'Open Current Affairs',
      { kind: 'open-todo', target: { kind: 'current' } },
      false,
    ],
    [
      'Open Current Affairs',
      { kind: 'open-todo', target: { kind: 'title', text: 'Current Affairs' } },
      true,
    ],
    [
      'Move Current Affairs to Done',
      {
        kind: 'move-todo',
        target: { kind: 'current' },
        destination: { kind: 'name', text: 'Done' },
      },
      false,
    ],
    [
      'Move this to Done',
      {
        kind: 'move-todo',
        target: { kind: 'current' },
        destination: { kind: 'name', text: 'Done' },
      },
      true,
    ],
    [
      'Delete This Is Fine',
      { kind: 'delete-todo', target: { kind: 'current' } },
      false,
    ],
    [
      'Delete this',
      { kind: 'delete-todo', target: { kind: 'current' } },
      true,
    ],
    [
      'Assign That One Bug to Mark Rai',
      {
        kind: 'assign-todo',
        target: { kind: 'current' },
        assignee: { kind: 'name', text: 'Mark Rai' },
      },
      false,
    ],
    [
      'Assign this to Mark Rai',
      {
        kind: 'assign-todo',
        target: { kind: 'current' },
        assignee: { kind: 'name', text: 'Mark Rai' },
      },
      true,
    ],
    [
      'Add current tag to Fixed Radical Login',
      {
        kind: 'add-todo-tag',
        target: { kind: 'current' },
        tag: { kind: 'name', text: 'current' },
      },
      false,
    ],
    [
      'Add current tag to Fixed Radical Login',
      {
        kind: 'add-todo-tag',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        tag: { kind: 'name', text: 'current' },
      },
      true,
    ],
    [
      'Remove current tag from Fixed Radical Login',
      {
        kind: 'remove-todo-tag',
        target: { kind: 'current' },
        tag: { kind: 'name', text: 'current' },
      },
      false,
    ],
    [
      'Remove current tag from this',
      {
        kind: 'remove-todo-tag',
        target: { kind: 'current' },
        tag: { kind: 'name', text: 'current' },
      },
      true,
    ],
    [
      'Unassign Same One Bug',
      { kind: 'unassign-todo', target: { kind: 'current' } },
      false,
    ],
    [
      'Unassign this story',
      { kind: 'unassign-todo', target: { kind: 'current' } },
      true,
    ],
    [
      'Who is assigned to This Is Fine?',
      {
        kind: 'inspect-todo',
        target: { kind: 'current' },
        aspect: 'assignee',
      },
      false,
    ],
    [
      'Who is assigned to this?',
      {
        kind: 'inspect-todo',
        target: { kind: 'current' },
        aspect: 'assignee',
      },
      true,
    ],
  ])('enforces target-position provenance for %s', (transcript, intent, accepted) => {
    const parse = () => parseVoiceInterpretationEnvelope(envelope(intent), transcript);
    if (accepted) {
      expect(parse()).toEqual({ kind: 'semantic', intent });
    } else {
      expect(parse).toThrow(expect.objectContaining({ code: 'output_rejected' }));
    }
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
    ['Change its title to fixing the login race condition'],
    ["Change this todo's title to fixing the login race condition"],
  ])('grounds normalized replacement-title output in contextual target provenance for %s', (transcript) => {
    const intent = {
      kind: 'update-todo-title',
      target: { kind: 'current' },
      title: 'Fix the login race condition',
    };
    expect(parseVoiceInterpretationEnvelope(envelope(intent), transcript))
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
      kind: 'provide-slot',
      operation: 'todo.update_title',
      slot: 'title',
      value: 'Fix the login race condition',
    }));
    const conversation = {
      pending: {
        kind: 'missing-slot' as const,
        operation: 'todo.update_title' as const,
        slot: 'title' as const,
      },
    };

    await expect(interpretVoiceCommand({
      transcript: 'Fix the login race condition',
      capability: local,
      locale: 'en',
      conversation,
    })).resolves.toEqual({
      kind: 'dialogue',
      intent: {
        kind: 'provide-slot',
        operation: 'todo.update_title',
        slot: 'title',
        value: 'Fix the login race condition',
      },
    });
    const request = local.generate.mock.calls[0][0];
    expect(request.instructions).toContain('todo.update_title/title');
    expect(JSON.stringify(request)).not.toContain('553');
  });

  it('does not let pending title context erase an explicitly supplied todo target', () => {
    const conversation = {
      pending: {
        kind: 'missing-slot' as const,
        operation: 'todo.update_title' as const,
        slot: 'title' as const,
      },
    };
    expect(() => parseVoiceInterpretationEnvelope(envelope({
      kind: 'update-todo-title',
      target: { kind: 'current' },
      title: 'Fixed Login',
    }), 'Rename Fixed Radical Login to Fixed Login', conversation))
      .toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    [
      'Add investigate retry timeout to the notes',
      {
        kind: 'append-todo-notes',
        target: { kind: 'current' },
        notes: 'Investigate retry timeout',
      },
    ],
    [
      'Replace the notes with blocked by API migration',
      {
        kind: 'replace-todo-notes',
        target: { kind: 'current' },
        notes: 'Blocked by API migration',
      },
    ],
    [
      'Add backend tag to Bogus',
      {
        kind: 'add-todo-tag',
        target: { kind: 'title', text: 'Bogus' },
        tag: { kind: 'name', text: 'backend' },
      },
    ],
    [
      'Remove backend tag from Bogus',
      {
        kind: 'remove-todo-tag',
        target: { kind: 'title', text: 'Bogus' },
        tag: { kind: 'name', text: 'backend' },
      },
    ],
    [
      'Unassign Bogus',
      { kind: 'unassign-todo', target: { kind: 'title', text: 'Bogus' } },
    ],
    [
      'Remove Mark from Bogus',
      {
        kind: 'unassign-todo',
        target: { kind: 'title', text: 'Bogus' },
        assignee: { kind: 'name', text: 'Mark' },
      },
    ],
    [
      'Remove the assignee from this story',
      { kind: 'unassign-todo', target: { kind: 'current' } },
    ],
    [
      'Who is assigned to Bogus?',
      {
        kind: 'inspect-todo',
        target: { kind: 'title', text: 'Bogus' },
        aspect: 'assignee',
      },
    ],
    [
      'What lane is this in?',
      { kind: 'inspect-todo', target: { kind: 'current' }, aspect: 'lane' },
    ],
    [
      'How many stories did we complete this week?',
      { kind: 'count-completed-todos', period: { kind: 'this-week' } },
    ],
  ])('accepts the bounded v7 operation for %s', (transcript, intent) => {
    expect(parseVoiceInterpretationEnvelope(envelope(intent), transcript))
      .toEqual({ kind: 'semantic', intent });
  });

  it('rejects an unassign interpretation that discards an explicitly named member', () => {
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({ kind: 'unassign-todo', target: { kind: 'title', text: 'Bogus' } }),
      'Remove Mark from Bogus',
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it('does not treat a contextual word inside authored notes as current-target provenance', () => {
    const transcript = 'Add current retry data to the notes of Fixed Radical Login';
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({
        kind: 'append-todo-notes',
        target: { kind: 'current' },
        notes: 'Current retry data',
      }),
      transcript,
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));

    expect(parseVoiceInterpretationEnvelope(
      envelope({
        kind: 'append-todo-notes',
        target: { kind: 'title', text: 'Fixed Radical Login' },
        notes: 'Current retry data',
      }),
      transcript,
    )).toMatchObject({
      kind: 'semantic',
      intent: { target: { kind: 'title', text: 'Fixed Radical Login' } },
    });
  });

  it('does not treat a contextual word inside a replacement title as target provenance', () => {
    const transcript = 'Rename Fixed Radical Login to Fix the current retry bug';
    expect(() => parseVoiceInterpretationEnvelope(
      envelope({
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the current retry bug',
      }),
      transcript,
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
  });

  it.each([
    {
      kind: 'add-todo-tag',
      target: { kind: 'title', text: 'Bogus' },
      tag: { kind: 'name', text: 'backend', tagId: 9 },
    },
    {
      kind: 'inspect-todo',
      target: { kind: 'title', text: 'Bogus' },
      aspect: 'lane',
      columnKey: 'done',
    },
    {
      kind: 'count-completed-todos',
      period: { kind: 'this-week' },
      count: 12,
    },
  ])('rejects model-supplied authoritative v7 facts', (intent) => {
    expect(() => parseVoiceInterpretationEnvelope(
      envelope(intent),
      'Add backend tag to Bogus',
    )).toThrow(expect.objectContaining({ code: 'output_rejected' }));
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
