// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { VoiceConversationIntent, VoiceCommandInterpreter } from './interpreter.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';
import { createVoiceConversationSession } from './conversation-session.js';

const executeCommandIRMock = vi.hoisted(() => vi.fn().mockResolvedValue({ ok: true }));
vi.mock('./execute.js', () => ({ executeCommandIR: executeCommandIRMock }));

import { createVoiceAgentController } from './agent-controller.js';

function makeBoard(): Board {
  return {
    project: {
      id: 1,
      slug: 'alpha',
      name: 'Alpha',
      dominantColor: '#123456',
      creatorUserId: 7,
    },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: {
      backlog: [{ id: 355, localId: 355, title: 'Fixed Radical Login', status: 'backlog' }],
      done: [],
    },
  };
}

function makeContext() {
  return {
    userId: 7,
    projectId: 1,
    projectSlug: 'alpha',
    board: makeBoard(),
    members: [{ userId: 7, name: 'Owner', email: 'owner@example.com', role: 'maintainer' }],
    role: 'maintainer',
  };
}

function speech(...transcripts: string[]): SpeechInputCapability {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready' }),
    listen: vi.fn(async (options) => {
      options.onListening?.();
      return { transcript: transcripts.shift()! };
    }),
  };
}

function options(
  speechInput: SpeechInputCapability,
  interpreter: VoiceCommandInterpreter,
  overrides: Record<string, unknown> = {},
) {
  return {
    initialUserId: 7,
    initialProjectId: 1,
    initialProjectSlug: 'alpha',
    getContext: vi.fn(() => makeContext()),
    refreshBoard: vi.fn().mockResolvedValue(undefined),
    openTodo: vi.fn().mockResolvedValue(undefined),
    speechInput,
    interpreter,
    continuationEnabled: true,
    onView: vi.fn(),
    ...overrides,
  };
}

beforeEach(() => executeCommandIRMock.mockClear());

describe('VoiceAgentController', () => {
  it('hands one bounded native transcript to AI exactly once and uses normal open resolution', async () => {
    const speechInput = speech('Open story number 355');
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({ kind: 'candidate', command: 'open todo 355' }),
    };
    const controllerOptions = options(speechInput, interpreter);
    const controller = createVoiceAgentController(controllerOptions);

    await controller.startListening();

    expect(speechInput.listen).toHaveBeenCalledOnce();
    expect(speechInput.listen).toHaveBeenCalledWith(expect.objectContaining({ maxDurationMs: 10_000 }));
    expect(interpreter.interpret).toHaveBeenCalledOnce();
    expect(interpreter.interpret).toHaveBeenCalledWith(
      'Open story number 355',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ intent: 'open_todo', entities: { localId: 355 } }),
      expect.any(Object),
    );
    expect(controllerOptions.openTodo).not.toHaveBeenCalled();
    expect(controller.getConversationState().activeTodo).toMatchObject({ localId: 355 });
    expect(controller.getView().phase).toBe('success');
  });

  it('preserves the missing-title dialogue across two speech windows without giving the model an ID', async () => {
    const speechInput = speech('Change the title', 'Fix the login race condition');
    const intents: VoiceConversationIntent[] = [
      { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      { kind: 'update-todo-title', target: { kind: 'current' }, title: 'Fix the login race condition' },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => ({ kind: 'conversation', intent: intents.shift()! })),
    };
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });
    const controller = createVoiceAgentController(options(speechInput, interpreter, { session }));

    await controller.startListening();
    expect(controller.getView().phase).toBe('question');
    expect(controller.getConversationState().pending?.target.localId).toBe(355);
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.startListening();
    expect(controller.getView().phase).toBe('confirmation');
    expect(interpreter.interpret).toHaveBeenCalledTimes(2);
    expect(vi.mocked(interpreter.interpret).mock.calls[0][0]).toBe('Change the title');
    expect(vi.mocked(interpreter.interpret).mock.calls[1]).toEqual([
      'Fix the login race condition',
      expect.objectContaining({
        conversation: { pending: { action: 'todo.update_title', slot: 'title' } },
      }),
    ]);

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({
        intent: 'todos.update_title',
        entities: { localId: 355, title: 'Fix the login race condition' },
      }),
      expect.any(Object),
    );
    expect(controller.getConversationState().pending).toBeNull();
  });

  it('loses turn ownership on Stop and ignores a provider result that arrives late', async () => {
    let resolveSpeech!: (value: { transcript: string }) => void;
    const speechInput: SpeechInputCapability = {
      status: vi.fn(),
      listen: vi.fn(() => new Promise((resolve) => { resolveSpeech = resolve; })),
    };
    const interpreter: VoiceCommandInterpreter = { interpret: vi.fn() };
    const controller = createVoiceAgentController(options(speechInput, interpreter));

    const listening = controller.startListening();
    controller.stopListening();
    resolveSpeech({ transcript: 'Open story number 355' });
    await listening;

    expect(interpreter.interpret).not.toHaveBeenCalled();
    expect(controller.getView().phase).toBe('ready');
  });

  it('agent close aborts an active operation and suppresses all late work', async () => {
    let resolveSpeech!: (value: { transcript: string }) => void;
    const speechInput: SpeechInputCapability = {
      status: vi.fn(),
      listen: vi.fn(() => new Promise((resolve) => { resolveSpeech = resolve; })),
    };
    const interpreter: VoiceCommandInterpreter = { interpret: vi.fn() };
    const controller = createVoiceAgentController(options(speechInput, interpreter));

    const listening = controller.startListening();
    controller.close();
    resolveSpeech({ transcript: 'Open story number 355' });
    await listening;

    expect(interpreter.interpret).not.toHaveBeenCalled();
    expect(controller.getView().phase).toBe('closed');
  });

  it('starts a fresh speech operation after a completed continued turn', async () => {
    const speechInput = speech('Open 355', 'Open it again');
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({ kind: 'candidate', command: 'open todo 355' }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter));

    await controller.startListening();
    await controller.startListening();

    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(interpreter.interpret).toHaveBeenCalledTimes(2);
    expect(controller.getConversationState().activeTodo?.localId).toBe(355);
  });
});
