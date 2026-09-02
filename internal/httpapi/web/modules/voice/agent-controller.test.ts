// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { VoiceCommandInterpreter } from './interpreter.js';
import type { VoiceSemanticIntent } from './semantic-intent.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';
import { SpeechInputError } from '../platform/speech-input.js';
import type { SpeechOutputCapability } from '../platform/speech-output.js';
import { createVoiceConversationSession } from './conversation-session.js';
import type { VoicePendingInteraction } from './conversation-state.js';

const executeCommandIRMock = vi.hoisted(() => vi.fn().mockResolvedValue({ ok: true }));
const callMcpToolMock = vi.hoisted(() => vi.fn().mockResolvedValue({ items: [] }));
vi.mock('./execute.js', () => ({ executeCommandIR: executeCommandIRMock }));
vi.mock('./mcp-client.js', () => ({
  callMcpTool: callMcpToolMock,
}));

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

function makeAmbiguousContext() {
  const board = makeBoard();
  board.columnOrder = [
    { key: 'backlog', name: 'Backlog', isDone: false },
    { key: 'doing', name: 'In Progress', isDone: false },
    { key: 'done', name: 'Done', isDone: true },
  ];
  board.columns.backlog = [
    { id: 354, localId: 354, title: 'Bogus', status: 'backlog' },
  ];
  board.columns.doing = [];
  board.columns.done = [
    { id: 353, localId: 353, title: 'Bogus', status: 'done' },
  ];
  return { ...makeContext(), board };
}

function speechOutput(): SpeechOutputCapability & {
  status: ReturnType<typeof vi.fn>;
  speak: ReturnType<typeof vi.fn>;
  stop: ReturnType<typeof vi.fn>;
  invalidate: ReturnType<typeof vi.fn>;
} {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready' }),
    speak: vi.fn().mockResolvedValue({ completed: true }),
    stop: vi.fn().mockResolvedValue(undefined),
    invalidate: vi.fn().mockResolvedValue(undefined),
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
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

beforeEach(() => {
  executeCommandIRMock.mockClear();
  callMcpToolMock.mockReset().mockResolvedValue({ items: [] });
});

describe('VoiceAgentController', () => {
  it('hands one bounded native transcript to AI exactly once and uses normal open resolution', async () => {
    const speechInput = speech('Open story number 355');
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'local-id', localId: 355 } },
      }),
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
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'provide-slot',
          operation: 'todo.update_title',
          slot: 'title',
          value: 'Fix the login race condition',
        },
      },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });
    const controller = createVoiceAgentController(options(speechInput, interpreter, { session }));

    await controller.startListening();
    expect(controller.getView().phase).toBe('question');
    expect(controller.getConversationState().pending).toMatchObject({ target: { localId: 355 } });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.startListening();
    expect(controller.getView().phase).toBe('confirmation');
    expect(interpreter.interpret).toHaveBeenCalledTimes(2);
    expect(vi.mocked(interpreter.interpret).mock.calls[0][0]).toBe('Change the title');
    expect(vi.mocked(interpreter.interpret).mock.calls[1]).toEqual([
      'Fix the login race condition',
      expect.objectContaining({
        conversation: {
          pending: {
            kind: 'missing-slot',
            operation: 'todo.update_title',
            slot: 'title',
          },
        },
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

  it('stages and executes the flagship semantic title move exactly once after confirmation', async () => {
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Fixed Radical Login' },
          destination: { kind: 'name', text: 'done' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter));

    await controller.submitTranscript('Move Fixed Radical Login to done');
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      confirmation: { summary: 'Move todo #355: Fixed Radical Login to Done' },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({
        intent: 'todos.move',
        entities: { localId: 355, toColumnKey: 'done' },
      }),
      expect.any(Object),
    );
  });

  it('confirms an append-notes effect with fresh notes and executes exactly one existing mutation', async () => {
    callMcpToolMock.mockResolvedValue({
      todo: {
        id: 355,
        localId: 355,
        title: 'Fixed Radical Login',
        body: 'Existing investigation',
        status: 'backlog',
      },
    });
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'append-todo-notes',
          target: { kind: 'title', text: 'Fixed Radical Login' },
          notes: 'Investigate retry timeout',
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter));

    await controller.submitTranscript('Add investigate retry timeout to the notes of Fixed Radical Login');
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      confirmation: { summary: 'Add to the notes of #355: "Investigate retry timeout"' },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();

    expect(interpreter.interpret).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({
        intent: 'todos.append_notes',
        entities: {
          localId: 355,
          body: 'Existing investigation\nInvestigate retry timeout',
          notes: 'Investigate retry timeout',
        },
      }),
      expect.any(Object),
    );
  });

  it('answers an authoritative assignee read directly without confirmation or a second inference', async () => {
    callMcpToolMock.mockImplementation(async (tool: string) => {
      if (tool === 'todos_get') {
        return {
          todo: {
            id: 355,
            localId: 355,
            title: 'Fixed Radical Login',
            status: 'backlog',
            assigneeUserId: 7,
          },
        };
      }
      if (tool === 'members_list') {
        return { items: makeContext().members };
      }
      return { items: [] };
    });
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'inspect-todo',
          target: { kind: 'title', text: 'Fixed Radical Login' },
          aspect: 'assignee',
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter));

    await controller.submitTranscript('Who is assigned to Fixed Radical Login?');

    expect(controller.getView()).toMatchObject({
      phase: 'success',
      status: {
        key: 'voice.info.todoAssignee',
        values: { title: 'Fixed Radical Login', member: 'Owner' },
      },
      confirmation: null,
    });
    expect(interpreter.interpret).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('renders authoritative semantic choices and freshly resolves the clicked todo before confirmation', async () => {
    const sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [
      { id: 351, localId: 351, title: 'Bogus', status: 'backlog' },
    ];
    sourceBoard.columns.doing = [
      { id: 352, localId: 352, title: 'Bogus', status: 'doing' },
    ];
    sourceBoard.columnOrder = [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ];
    const activeContext = { ...makeContext(), board: sourceBoard };
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'done' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => activeContext),
    }));

    await controller.submitTranscript('Move Bogus to done');
    expect(controller.getView()).toMatchObject({
      phase: 'question',
      clarification: {
        options: [
          { id: 'todo:351', label: '#351 · Bogus · Backlog' },
          { id: 'todo:352', label: '#352 · Bogus · In Progress' },
        ],
      },
    });
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'clarification' });

    await controller.chooseClarification(0);
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      confirmation: { summary: 'Move todo #351: Bogus to Done' },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ entities: { localId: 351, toColumnKey: 'done' } }),
      expect.any(Object),
    );
  });

  it('completes an all-satisfied semantic request without clarification or mutation', async () => {
    const sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [];
    sourceBoard.columns.done = [
      { id: 351, localId: 351, title: 'Bogus', status: 'done' },
      { id: 352, localId: 352, title: 'Bogus', status: 'done' },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'done' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => ({ ...makeContext(), board: sourceBoard })),
    }));

    await controller.submitTranscript('Move Bogus to done');
    expect(controller.getView()).toMatchObject({ phase: 'success', clarification: null });
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('re-resolves a reviewed semantic effect and refuses execution when identity changes', async () => {
    let sourceBoard = makeBoard();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Fixed Radical Login' },
          destination: { kind: 'name', text: 'done' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => ({ ...makeContext(), board: sourceBoard })),
    }));

    await controller.submitTranscript('Move Fixed Radical Login to done');
    expect(controller.getView().phase).toBe('confirmation');

    sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [
      { id: 355, localId: 355, title: 'Renamed card', status: 'backlog' },
      { id: 356, localId: 356, title: 'Fixed Radical Login', status: 'backlog' },
    ];
    await controller.confirm();

    expect(controller.getView()).toMatchObject({
      phase: 'error',
      status: { key: 'voice.status.commandChanged' },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('checks mutation authorization before semantic resolution or confirmation', async () => {
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'delete-todo',
          target: { kind: 'title', text: 'Fixed Radical Login' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => ({ ...makeContext(), role: 'contributor' })),
    }));

    await controller.submitTranscript('Delete Fixed Radical Login');

    expect(controller.getView()).toMatchObject({ phase: 'error' });
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('opens exactly one automatic reply window after a successfully spoken voice question', async () => {
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Move story 355' };
        })
        .mockRejectedValueOnce(new SpeechInputError('no_speech')),
    };
    const output = speechOutput();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
      continuationEnabled: false,
    }));

    await controller.startListening();

    expect(output.speak).toHaveBeenCalledOnce();
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(controller.getConversationState().pending).toMatchObject({
      kind: 'missing-slot',
      operation: 'todo.move',
      slot: 'destination',
    });
    expect(controller.getView().phase).toBe('question');
  });

  it('keeps a reviewed confirmation visible while automatic ASR listens and after recognition failure', async () => {
    const automatic = deferred<{ transcript: string }>();
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Move Bogus back to backlog' };
        })
        .mockImplementationOnce((input) => {
          input.onListening?.();
          return automatic.promise;
        }),
    };
    const output = speechOutput();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'Backlog' },
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    const task = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));

    expect(controller.getConversationState().pending).toMatchObject({
      kind: 'confirmation',
      operation: 'todo.move',
    });
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      activity: 'listening',
      activityStatus: { key: 'voice.agent.listening' },
      confirmation: { summary: expect.stringContaining('Bogus') },
    });

    automatic.reject(new SpeechInputError('recognition_failed', {
      providerCode: 3,
      providerReason: 'audio',
    }));
    await task;

    expect(controller.getConversationState().pending).toMatchObject({ kind: 'confirmation' });
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      activity: 'idle',
      activityStatus: { key: 'voice.agent.speechFailed' },
      confirmation: { summary: expect.stringContaining('Bogus') },
    });
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
  });

  it('keeps authoritative choices visible during automatic ASR and after no speech', async () => {
    const automatic = deferred<{ transcript: string }>();
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Open Bogus' };
        })
        .mockImplementationOnce((input) => {
          input.onListening?.();
          return automatic.promise;
        }),
    };
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'title', text: 'Bogus' } },
      }),
    }, {
      speechOutput: speechOutput(),
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    const task = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));

    const pending = controller.getConversationState().pending;
    expect(pending).toMatchObject({ kind: 'clarification' });
    expect(controller.getView()).toMatchObject({
      phase: 'question',
      activity: 'listening',
      clarification: { options: expect.arrayContaining([
        expect.objectContaining({ label: expect.stringContaining('Done') }),
        expect.objectContaining({ label: expect.stringContaining('Backlog') }),
      ]) },
    });

    automatic.reject(new SpeechInputError('no_speech'));
    await task;

    expect(controller.getConversationState().pending).toBe(pending);
    expect(controller.getView()).toMatchObject({
      phase: 'question',
      activity: 'idle',
      activityStatus: { key: 'voice.agent.noSpeech' },
      clarification: { options: expect.any(Array) },
    });
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('lets the retained confirmation button take over from automatic listening', async () => {
    const automatic = deferred<{ transcript: string }>();
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Move Bogus back to backlog' };
        })
        .mockImplementationOnce((input) => {
          input.onListening?.();
          return automatic.promise;
        }),
    };
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'Backlog' },
        },
      }),
    }, {
      speechOutput: speechOutput(),
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    const listening = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));
    expect(controller.getView()).toMatchObject({ phase: 'confirmation', activity: 'listening' });
    await controller.confirm();

    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    automatic.resolve({ transcript: 'late confirmation' });
    await listening;
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
  });

  it('lets a retained choice button take over from automatic listening', async () => {
    const automatic = deferred<{ transcript: string }>();
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Open Bogus' };
        })
        .mockImplementationOnce((input) => {
          input.onListening?.();
          return automatic.promise;
        }),
    };
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'title', text: 'Bogus' } },
      }),
    }, {
      speechOutput: speechOutput(),
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    const listening = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));
    expect(controller.getView()).toMatchObject({ phase: 'question', activity: 'listening' });
    await controller.chooseClarification(0);

    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ intent: 'open_todo', entities: { localId: 353 } }),
      expect.any(Object),
    );
    automatic.resolve({ transcript: 'late choice' });
    await listening;
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
  });

  it('keeps a missing-slot question visible while its automatic reply window listens', async () => {
    const automatic = deferred<{ transcript: string }>();
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Move story 355' };
        })
        .mockImplementationOnce((input) => {
          input.onListening?.();
          return automatic.promise;
        }),
    };
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    }, { speechOutput: speechOutput(), continuationEnabled: false }));

    const task = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));

    expect(controller.getConversationState().pending).toMatchObject({
      kind: 'missing-slot',
      slot: 'destination',
    });
    expect(controller.getView()).toMatchObject({
      phase: 'question',
      activity: 'listening',
      status: { key: 'voice.question.moveDestination' },
    });

    automatic.reject(new SpeechInputError('no_speech'));
    await task;
  });

  it('shows recognizer busy on the pending question without retrying', async () => {
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'temporarily-unavailable', reason: 'busy' }),
      listen: vi.fn(async (input) => {
        input.onListening?.();
        return { transcript: 'Move story 355' };
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    }, { speechOutput: speechOutput(), continuationEnabled: false }));

    await controller.startListening();

    expect(controller.getConversationState().pending).toMatchObject({ kind: 'missing-slot' });
    expect(controller.getView()).toMatchObject({
      phase: 'question',
      activity: 'idle',
      activityStatus: { key: 'voice.agent.busy' },
    });
    expect(speechInput.listen).toHaveBeenCalledOnce();
  });

  it('does not change any pending semantic state merely because ASR starts', async () => {
    const target = { kind: 'todo' as const, projectId: 1, projectSlug: 'alpha', localId: 355 };
    const pendingCases: readonly Readonly<{ name: string; pending: VoicePendingInteraction }>[] = [
      {
        name: 'confirmation',
        pending: { kind: 'confirmation', operation: 'todo.move' },
      },
      {
        name: 'todo clarification',
        pending: {
          kind: 'clarification',
          intent: { kind: 'open-todo', target: { kind: 'title', text: 'Bogus' } },
          choices: [{ kind: 'todo', reference: target, title: 'Bogus', laneKey: 'backlog', laneName: 'Backlog' }],
          selection: {},
        },
      },
      {
        name: 'member clarification',
        pending: {
          kind: 'clarification',
          intent: {
            kind: 'assign-todo',
            target: { kind: 'local-id', localId: 355 },
            assignee: { kind: 'name', text: 'Alex' },
          },
          choices: [{ kind: 'member', userId: 8, name: 'Alex Smith', email: 'alex@example.com' }],
          selection: {},
        },
      },
      {
        name: 'tag clarification',
        pending: {
          kind: 'clarification',
          intent: {
            kind: 'add-todo-tag',
            target: { kind: 'local-id', localId: 355 },
            tag: { kind: 'name', text: 'back' },
          },
          choices: [{ kind: 'tag', name: 'backend' }],
          selection: {},
        },
      },
      {
        name: 'missing slot',
        pending: {
          kind: 'missing-slot',
          operation: 'todo.create',
          slot: 'title',
          intent: { kind: 'create-todo', title: null },
          selection: {},
        },
      },
    ];

    for (const pendingCase of pendingCases) {
      const acquired = deferred<{ transcript: string }>();
      const speechInput: SpeechInputCapability = {
        status: vi.fn().mockResolvedValue({ state: 'ready' }),
        listen: vi.fn((input) => {
          input.onListening?.();
          return acquired.promise;
        }),
      };
      const session = createVoiceConversationSession();
      session.setPendingInteraction(pendingCase.pending);
      const before = session.getState().pending;
      const controller = createVoiceAgentController(options(speechInput, { interpret: vi.fn() }, { session }));

      const task = controller.startListening();
      await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledOnce());
      expect(controller.getConversationState().pending, pendingCase.name).toBe(before);
      expect(controller.getView().activity, pendingCase.name).toBe('listening');

      controller.stopListening();
      acquired.resolve({ transcript: 'late' });
      await task;
      expect(controller.getConversationState().pending, pendingCase.name).toBe(before);
    }
  });

  it('passes automatic spoken confirmation through pending context and executes exactly once without a button', async () => {
    const speechInput = speech('Move Bogus back to backlog', 'Yeah, go ahead');
    const output = speechOutput();
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'Backlog' },
        },
      },
      { kind: 'dialogue', intent: { kind: 'confirm' } },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.startListening();

    expect(interpreter.interpret).toHaveBeenCalledTimes(2);
    expect(vi.mocked(interpreter.interpret).mock.calls[1]).toEqual([
      'Yeah, go ahead',
      expect.objectContaining({
        conversation: { pending: { kind: 'confirmation', operation: 'todo.move' } },
      }),
    ]);
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({
        intent: 'todos.move',
        entities: { localId: 353, toColumnKey: 'backlog' },
      }),
      expect.any(Object),
    );
    expect(controller.getConversationState().pending).toBeNull();
  });

  it('anchors confirmation through spoken-answer processing and retains it for an invalid reply', async () => {
    const secondTurn = deferred<Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>>();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn()
        .mockResolvedValueOnce({
          kind: 'semantic',
          intent: {
            kind: 'move-todo',
            target: { kind: 'title', text: 'Bogus' },
            destination: { kind: 'name', text: 'Backlog' },
          },
        })
        .mockReturnValueOnce(secondTurn.promise),
    };
    const controller = createVoiceAgentController(options(
      speech('Move Bogus back to backlog', 'The purple one'),
      interpreter,
      {
        speechOutput: speechOutput(),
        continuationEnabled: false,
        getContext: vi.fn(() => makeAmbiguousContext()),
      },
    ));

    const task = controller.startListening();
    await vi.waitFor(() => expect(interpreter.interpret).toHaveBeenCalledTimes(2));

    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      activity: 'processing',
      confirmation: { summary: expect.stringContaining('Bogus') },
    });
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'confirmation' });

    secondTurn.resolve({
      kind: 'unsupported',
      failure: { ok: false, code: 'unsupported', message: 'Unsupported command.' },
    });
    await task;

    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      activity: 'idle',
      status: { key: 'voice.dialogue.invalidResponse' },
      confirmation: { summary: expect.stringContaining('Bogus') },
    });
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'confirmation' });
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('passes an automatic spoken choice through offered-set context and opens only the selected todo', async () => {
    const speechInput = speech('Open Bogus', 'The one in the backlog');
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'title', text: 'Bogus' } },
      },
      {
        kind: 'dialogue',
        intent: { kind: 'select-choice', selector: { kind: 'lane', text: 'Backlog' } },
      },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: speechOutput(),
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.startListening();

    expect(interpreter.interpret).toHaveBeenCalledTimes(2);
    expect(vi.mocked(interpreter.interpret).mock.calls[1]).toEqual([
      'The one in the backlog',
      expect.objectContaining({
        conversation: { pending: { kind: 'todo-choice' } },
      }),
    ]);
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ intent: 'open_todo', entities: { localId: 354 } }),
      expect.any(Object),
    );
    expect(controller.getConversationState().pending).toBeNull();
  });

  it('never automatically starts the microphone for a typed question', async () => {
    const speechInput = speech();
    const output = speechOutput();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
    }));

    await controller.submitTranscript('Move story 355');

    expect(controller.getView().phase).toBe('question');
    expect(speechInput.listen).not.toHaveBeenCalled();
  });

  it('keeps a pending question manual when TTS fails and never auto-retries silence', async () => {
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn(async (input) => {
        input.onListening?.();
        return { transcript: 'Move story 355' };
      }),
    };
    const output = speechOutput();
    output.speak.mockRejectedValue(new Error('TTS unavailable'));
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
    }));

    await controller.startListening();

    expect(speechInput.listen).toHaveBeenCalledOnce();
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'missing-slot' });
    expect(controller.getView().phase).toBe('question');
  });

  it.each([
    { state: 'unsupported', reason: 'no-local-voice' } as const,
    { state: 'not-ready', reason: 'initializing' } as const,
    { state: 'temporarily-unavailable', reason: 'foreground' } as const,
  ])('keeps enhanced dialogue visual and manual when speech output is $state', async (outputStatus) => {
    const speechInput = speech('Move story 355');
    const output = speechOutput();
    output.status.mockResolvedValue(outputStatus);
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
    }));

    await controller.startListening();

    expect(output.speak).not.toHaveBeenCalled();
    expect(speechInput.listen).toHaveBeenCalledOnce();
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'missing-slot' });
    expect(controller.getView().phase).toBe('question');
  });

  it('finishes choice and confirmation windows with Continue off, then does not listen again', async () => {
    const speechInput = speech(
      'Move Bogus to In Progress',
      'The one in the backlog',
      'Yeah go ahead',
    );
    const output = speechOutput();
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      {
        kind: 'dialogue',
        intent: { kind: 'select-choice', selector: { kind: 'lane', text: 'backlog' } },
      },
      { kind: 'dialogue', intent: { kind: 'confirm' } },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.startListening();

    expect(speechInput.listen).toHaveBeenCalledTimes(3);
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ entities: { localId: 354, toColumnKey: 'doing' } }),
      expect.any(Object),
    );
    expect(controller.getConversationState().pending).toBeNull();
  });

  it('opens one new-command listen after a terminal spoken result with Continue on', async () => {
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Open story 355' };
        })
        .mockRejectedValueOnce(new SpeechInputError('no_speech')),
    };
    const output = speechOutput();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'local-id', localId: 355 } },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
      continuationEnabled: true,
    }));

    await controller.startListening();

    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(interpreter.interpret).toHaveBeenCalledOnce();
    expect(controller.getView().phase).toBe('success');
  });

  it('keeps a non-offered choice pending and never executes it', async () => {
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      {
        kind: 'dialogue',
        intent: { kind: 'select-choice', selector: { kind: 'local-id', localId: 999 } },
      },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.submitTranscript('Move Bogus to In Progress');
    await controller.submitTranscript('Number 999');

    expect(controller.getConversationState().pending).toMatchObject({ kind: 'clarification' });
    expect(controller.getView().phase).toBe('question');
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('rebuilds a reviewed target from retained offered choices before allowing confirmation', async () => {
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      {
        kind: 'dialogue',
        intent: { kind: 'select-choice', selector: { kind: 'lane', text: 'done' } },
      },
      {
        kind: 'dialogue',
        intent: { kind: 'correct-choice', selector: { kind: 'lane', text: 'backlog' } },
      },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.submitTranscript('Move Bogus to In Progress');
    await controller.submitTranscript('The one in Done');
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      confirmation: { summary: 'Move todo #353: Bogus to In Progress' },
    });

    await controller.submitTranscript('No use the one in Backlog');
    expect(controller.getView()).toMatchObject({
      phase: 'confirmation',
      confirmation: { summary: 'Move todo #354: Bogus to In Progress' },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ entities: { localId: 354, toColumnKey: 'doing' } }),
      expect.any(Object),
    );
  });

  it('invalidates the old authored value and requires a new confirmation', async () => {
    callMcpToolMock.mockResolvedValue({
      todo: {
        id: 355,
        localId: 355,
        title: 'Fixed Radical Login',
        body: '',
        status: 'backlog',
      },
    });
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'append-todo-notes',
          target: { kind: 'local-id', localId: 355 },
          notes: 'I will be there',
        },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'correct-value',
          operation: 'todo.append_notes',
          slot: 'notes',
          value: "I'll arrive at six.",
        },
      },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter));

    await controller.submitTranscript('Add I will be there to the notes of story 355');
    expect(controller.getView().confirmation?.summary).toContain('I will be there');
    await controller.submitTranscript("Actually make the note I'll arrive at six.");
    expect(controller.getView().confirmation?.summary).toContain("I'll arrive at six.");
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    await controller.confirm();
    expect(executeCommandIRMock).toHaveBeenCalledOnce();
    expect(executeCommandIRMock).toHaveBeenCalledWith(
      expect.objectContaining({ entities: expect.objectContaining({ notes: "I'll arrive at six." }) }),
      expect.any(Object),
    );
  });

  it('treats an attempted operation replacement as decline and executes nothing', async () => {
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      { kind: 'dialogue', intent: { kind: 'decline' } },
    ];
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn(async () => interpretations.shift()!),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter));

    await controller.submitTranscript('Move story 355 to Done');
    await controller.submitTranscript('No delete it instead');

    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(controller.getConversationState().pending).toBeNull();
  });

  it('rejects confirmation outside confirmation and does not advance a choice on yes', async () => {
    const outside: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({ kind: 'dialogue', intent: { kind: 'confirm' } }),
    };
    const outsideController = createVoiceAgentController(options(speech(), outside));
    await outsideController.submitTranscript('Yes');
    expect(outsideController.getView().phase).toBe('error');
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      { kind: 'dialogue', intent: { kind: 'confirm' } },
    ];
    const pendingController = createVoiceAgentController(options(speech(), {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, { getContext: vi.fn(() => makeAmbiguousContext()) }));
    await pendingController.submitTranscript('Move Bogus to In Progress');
    await pendingController.submitTranscript('Yes');
    expect(pendingController.getConversationState().pending).toMatchObject({ kind: 'clarification' });
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('barge-in awaits native TTS stop before one new listen and ignores stale speech completion', async () => {
    const speechInput = speech('Move story 355', 'Done');
    const output = speechOutput();
    const staleSpeech = deferred<{ completed: true }>();
    const nativeStop = deferred<void>();
    output.speak
      .mockReturnValueOnce(staleSpeech.promise)
      .mockRejectedValueOnce(new Error('not available'));
    output.stop.mockReturnValueOnce(nativeStop.promise);
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'provide-slot',
          operation: 'todo.move',
          slot: 'destination',
          value: { kind: 'name', text: 'Done' },
        },
      },
    ];
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, { speechOutput: output }));

    const first = controller.startListening();
    await vi.waitFor(() => expect(output.speak).toHaveBeenCalledOnce());
    const second = controller.startListening();

    await vi.waitFor(() => expect(output.stop).toHaveBeenCalledOnce());
    expect(speechInput.listen).toHaveBeenCalledOnce();

    staleSpeech.resolve({ completed: true });
    await first;
    expect(speechInput.listen).toHaveBeenCalledOnce();

    nativeStop.resolve();
    await Promise.all([first, second]);

    expect(output.stop).toHaveBeenCalledOnce();
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    expect(controller.getView().phase).toBe('confirmation');
  });

  it('background invalidation stops TTS and preserves pending dialogue without a late listen', async () => {
    const speechInput = speech('Move story 355');
    const output = speechOutput();
    output.speak.mockImplementation(({ signal }: { signal?: AbortSignal }) => new Promise((_, reject) => {
      signal?.addEventListener('abort', () => reject(new Error('cancelled')), { once: true });
    }));
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
    }));

    const task = controller.startListening();
    await vi.waitFor(() => expect(output.speak).toHaveBeenCalledOnce());
    controller.invalidate();
    await task;

    expect(output.invalidate).toHaveBeenCalledOnce();
    expect(speechInput.listen).toHaveBeenCalledOnce();
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'missing-slot' });
  });

  it('keeps a pending notes target bound when activeTodo changes before the slot answer', async () => {
    const sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [
      { id: 353, localId: 353, title: 'Bogus', status: 'backlog' },
      { id: 355, localId: 355, title: 'Fixed Radical Login', status: 'backlog' },
    ];
    callMcpToolMock.mockImplementation(async (_tool: string, input: { localId?: number }) => ({
      todo: sourceBoard.columns.backlog.find((candidate) => candidate.localId === input.localId),
    }));
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'append-todo-notes', target: { kind: 'current' }, notes: null },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'provide-slot',
          operation: 'todo.append_notes',
          slot: 'notes',
          value: 'I will be there',
        },
      },
    ];
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 353 });
    const controller = createVoiceAgentController(options(speech(), {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, {
      session,
      getContext: vi.fn(() => ({ ...makeContext(), board: sourceBoard })),
    }));

    await controller.submitTranscript('Add to the notes');
    expect(controller.getConversationState().pending).toMatchObject({ target: { localId: 353 } });
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });
    await controller.submitTranscript('I will be there');

    expect(controller.getView().confirmation?.summary).toContain('#353');
    expect(controller.getView().confirmation?.summary).not.toContain('#355');
  });

  it('keeps a pending move target bound when activeTodo changes before destination', async () => {
    const sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [
      { id: 353, localId: 353, title: 'Bogus', status: 'backlog' },
      { id: 355, localId: 355, title: 'Fixed Radical Login', status: 'backlog' },
    ];
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'move-todo', target: { kind: 'current' }, destination: null },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'provide-slot',
          operation: 'todo.move',
          slot: 'destination',
          value: { kind: 'name', text: 'Done' },
        },
      },
    ];
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 353 });
    const controller = createVoiceAgentController(options(speech(), {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, {
      session,
      getContext: vi.fn(() => ({ ...makeContext(), board: sourceBoard })),
    }));

    await controller.submitTranscript('Move this');
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });
    await controller.submitTranscript('Done');

    expect(controller.getView().confirmation?.summary).toContain('#353');
    expect(controller.getView().confirmation?.summary).not.toContain('#355');
  });

  it('fails closed when a pending authoritative target disappears before the slot answer', async () => {
    let sourceBoard = makeBoard();
    sourceBoard.columns.backlog = [
      { id: 353, localId: 353, title: 'Bogus', status: 'backlog' },
      { id: 355, localId: 355, title: 'Fixed Radical Login', status: 'backlog' },
    ];
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'move-todo', target: { kind: 'current' }, destination: null },
      },
      {
        kind: 'dialogue',
        intent: {
          kind: 'provide-slot',
          operation: 'todo.move',
          slot: 'destination',
          value: { kind: 'name', text: 'Done' },
        },
      },
    ];
    const session = createVoiceConversationSession();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 353 });
    const controller = createVoiceAgentController(options(speech(), {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, {
      session,
      getContext: vi.fn(() => ({ ...makeContext(), board: sourceBoard })),
    }));

    await controller.submitTranscript('Move this');
    sourceBoard = makeBoard();
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 355 });
    await controller.submitTranscript('Done');

    expect(controller.getView().phase).toBe('error');
    expect(controller.getView().confirmation).toBeNull();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('scopes voice modality to the task so a later typed question never starts the microphone', async () => {
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Open story 355' };
        })
        .mockRejectedValueOnce(new SpeechInputError('no_speech')),
    };
    const output = speechOutput();
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'local-id', localId: 355 } },
      },
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      },
    ];
    const controller = createVoiceAgentController(options(speechInput, {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, { speechOutput: output, continuationEnabled: true }));

    await controller.startListening();
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
    await controller.submitTranscript('Move story 355');

    expect(controller.getView().phase).toBe('question');
    expect(speechInput.listen).toHaveBeenCalledTimes(2);
  });

  it('clears retained context when Continue is turned off while idle', async () => {
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: { kind: 'open-todo', target: { kind: 'local-id', localId: 355 } },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      continuationEnabled: true,
    }));

    await controller.submitTranscript('Open story 355');
    expect(controller.getConversationState().activeTodo?.localId).toBe(355);
    controller.setContinuationEnabled(false);

    expect(controller.getConversationState()).toMatchObject({
      activeProject: null,
      activeTodo: null,
      pending: null,
      continuationEnabled: false,
    });
  });

  it('does not clear an active task when Continue is turned off mid-dialogue', async () => {
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speech(), interpreter, {
      continuationEnabled: true,
    }));

    await controller.submitTranscript('Move story 355');
    controller.setContinuationEnabled(false);

    expect(controller.getConversationState().pending).toMatchObject({
      kind: 'missing-slot',
      target: { localId: 355 },
    });
    expect(controller.getConversationState().continuationEnabled).toBe(false);
  });

  it('cancels safely after eight response-requiring dialogue turns', async () => {
    const interpretations: Awaited<ReturnType<VoiceCommandInterpreter['interpret']>>[] = [
      {
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'title', text: 'Bogus' },
          destination: { kind: 'name', text: 'In Progress' },
        },
      },
      ...Array.from({ length: 8 }, () => ({
        kind: 'dialogue' as const,
        intent: { kind: 'confirm' as const },
      })),
    ];
    const controller = createVoiceAgentController(options(speech(), {
      interpret: vi.fn(async () => interpretations.shift()!),
    }, {
      continuationEnabled: false,
      getContext: vi.fn(() => makeAmbiguousContext()),
    }));

    await controller.submitTranscript('Move Bogus to In Progress');
    for (let turn = 0; turn < 8; turn += 1) {
      await controller.submitTranscript('Yes');
    }

    expect(controller.getView()).toMatchObject({
      phase: 'error',
      status: { key: 'voice.dialogue.turnLimit' },
    });
    expect(controller.getConversationState().pending).toBeNull();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('background invalidation during automatic listening suppresses a late transcript', async () => {
    let resolveAutomatic!: (result: { transcript: string }) => void;
    const speechInput: SpeechInputCapability = {
      status: vi.fn().mockResolvedValue({ state: 'ready' }),
      listen: vi.fn()
        .mockImplementationOnce(async (input) => {
          input.onListening?.();
          return { transcript: 'Move story 355' };
        })
        .mockImplementationOnce(() => new Promise((resolve) => {
          resolveAutomatic = resolve;
        })),
    };
    const output = speechOutput();
    const interpreter: VoiceCommandInterpreter = {
      interpret: vi.fn().mockResolvedValue({
        kind: 'semantic',
        intent: {
          kind: 'move-todo',
          target: { kind: 'local-id', localId: 355 },
          destination: null,
        },
      }),
    };
    const controller = createVoiceAgentController(options(speechInput, interpreter, {
      speechOutput: output,
    }));

    const task = controller.startListening();
    await vi.waitFor(() => expect(speechInput.listen).toHaveBeenCalledTimes(2));
    controller.invalidate();
    resolveAutomatic({ transcript: 'Done' });
    await task;

    expect(interpreter.interpret).toHaveBeenCalledOnce();
    expect(controller.getConversationState().pending).toMatchObject({ kind: 'missing-slot' });
  });
});
