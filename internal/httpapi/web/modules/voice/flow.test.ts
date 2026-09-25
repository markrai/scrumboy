// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';

const callMcpToolMock = vi.hoisted(() => vi.fn());
const executeCommandIRMock = vi.hoisted(() => vi.fn());
const startOneShotRecognitionMock = vi.hoisted(() => vi.fn());
const speakMock = vi.hoisted(() => vi.fn());
const showConfirmDialogMock = vi.hoisted(() => vi.fn());
const getVoiceInterpretationAvailabilityMock = vi.hoisted(() => vi.fn());
const prepareVoiceInterpretationMock = vi.hoisted(() => vi.fn());
const interpretVoiceCommandMock = vi.hoisted(() => vi.fn());
const deterministicInterpretMock = vi.hoisted(() => vi.fn());
const localAiInterpretMock = vi.hoisted(() => vi.fn());
const createLocalAiInterpreterMock = vi.hoisted(() => vi.fn());
const createVoiceConversationSessionMock = vi.hoisted(() => vi.fn());
const runtimeCapabilityMock = vi.hoisted(() => vi.fn());
const localTextGenerationCapability = vi.hoisted(() => ({
  status: vi.fn(),
  prepare: vi.fn(),
  generate: vi.fn(),
}));

vi.mock('./mcp-client.js', () => ({ callMcpTool: callMcpToolMock }));
vi.mock('./execute.js', () => ({ executeCommandIR: executeCommandIRMock }));
vi.mock('./speech.js', () => ({ startOneShotRecognition: startOneShotRecognitionMock }));
vi.mock('./speech-output.js', () => ({ speak: speakMock }));
vi.mock('./local-interpretation.js', () => ({
  getVoiceInterpretationAvailability: getVoiceInterpretationAvailabilityMock,
  prepareVoiceInterpretation: prepareVoiceInterpretationMock,
  interpretVoiceCommand: interpretVoiceCommandMock,
}));
vi.mock('./deterministic-interpreter.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./deterministic-interpreter.js')>();
  deterministicInterpretMock.mockImplementation(
    (input: string, options?: { signal?: AbortSignal }) =>
      actual.deterministicVoiceCommandInterpreter.interpret(input, options),
  );
  return {
    ...actual,
    deterministicVoiceCommandInterpreter: {
      interpret: deterministicInterpretMock,
    },
  };
});
vi.mock('./local-ai-interpreter.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./local-ai-interpreter.js')>();
  createLocalAiInterpreterMock.mockImplementation(
    (dependencies: Parameters<typeof actual.createLocalAiVoiceCommandInterpreter>[0]) => {
      const interpreter = actual.createLocalAiVoiceCommandInterpreter(dependencies);
      return {
        interpret(input: string, options?: { signal?: AbortSignal }) {
          localAiInterpretMock(input, options);
          return interpreter.interpret(input, options);
        },
      };
    },
  );
  return {
    ...actual,
    createLocalAiVoiceCommandInterpreter: createLocalAiInterpreterMock,
  };
});
vi.mock('./conversation-session.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./conversation-session.js')>();
  createVoiceConversationSessionMock.mockImplementation(() => {
    const session = actual.createVoiceConversationSession();
    return {
      ...session,
      setActiveTodo: vi.fn(session.setActiveTodo),
      clearActiveTodo: vi.fn(session.clearActiveTodo),
    };
  });
  return {
    ...actual,
    createVoiceConversationSession: createVoiceConversationSessionMock,
  };
});
vi.mock('../platform/runtime.js', () => ({
  getAppRuntime: () => ({ capability: runtimeCapabilityMock }),
}));
vi.mock('../utils.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../utils.js')>();
  return { ...actual, showConfirmDialog: showConfirmDialogMock };
});

import {
  openVoiceCommandDialog,
  parseAlternatives,
  parseAndResolveCommand,
  parseConfirmationAlternatives,
  parseDisambiguationAlternatives,
  type OpenVoiceCommandOptions,
  type VoiceCommandDialogContext,
} from './flow.js';
import {
  VOICE_FLOW_CONTINUE_CONVERSATION_STORAGE_KEY,
  VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY,
  VOICE_FLOW_MODE_STORAGE_KEY,
} from '../core/voiceflow-preferences.js';
import { LocalTextGenerationError } from '../platform/local-text-generation.js';

const members: BoardMember[] = [
  { userId: 7, name: 'Ada Lovelace', email: 'ada@example.com', role: 'maintainer' },
];

function makeBoard(overrides: Partial<Board> = {}): Board {
  return {
    project: {
      id: 1,
      name: 'Alpha',
      slug: 'alpha',
      dominantColor: '#123456',
      creatorUserId: 1,
    },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: {
      backlog: [],
      doing: [{ id: 10, localId: 56, title: 'Fix login', status: 'doing' }],
      done: [],
    },
    ...overrides,
  };
}

function makeAmbiguousLoginBoard(): Board {
  return makeBoard({
    columns: {
      backlog: [
        { id: 1, localId: 12, title: 'Fix login redirect', status: 'backlog' },
        { id: 2, localId: 13, title: 'Fix login validation', status: 'backlog' },
        { id: 3, localId: 14, title: 'Fix login button style', status: 'backlog' },
      ],
      doing: [],
      done: [],
    },
  });
}

function makeContext(board = makeBoard()): VoiceCommandDialogContext {
  return {
    userId: 7,
    projectId: 1,
    projectSlug: 'alpha',
    board,
    members,
    role: 'maintainer',
  };
}

function makeOptions(getContext: () => VoiceCommandDialogContext | null): OpenVoiceCommandOptions {
  return {
    initialUserId: 7,
    initialProjectId: 1,
    initialProjectSlug: 'alpha',
    getContext,
    refreshBoard: vi.fn().mockResolvedValue(undefined),
    openTodo: vi.fn().mockResolvedValue(undefined),
    recordMutation: vi.fn(),
    showMessage: vi.fn(),
  };
}

async function flushAsync(): Promise<void> {
  for (let i = 0; i < 30; i += 1) {
    await Promise.resolve();
  }
}

function useLocalTextGenerationStatus(
  status: Awaited<ReturnType<typeof localTextGenerationCapability.status>>,
): void {
  runtimeCapabilityMock.mockReturnValue(localTextGenerationCapability);
  localTextGenerationCapability.status.mockResolvedValue(status);
}

function useReadyLocalAi(): void {
  useLocalTextGenerationStatus({ state: 'ready', maximumOutputTokens: 96 });
}

beforeEach(() => {
  document.body.innerHTML = '';
  localStorage.clear();
  callMcpToolMock.mockReset();
  executeCommandIRMock.mockReset();
  startOneShotRecognitionMock.mockReset();
  speakMock.mockReset().mockResolvedValue(undefined);
  showConfirmDialogMock.mockReset().mockResolvedValue(true);
  getVoiceInterpretationAvailabilityMock.mockReset().mockResolvedValue({ state: 'absent' });
  prepareVoiceInterpretationMock.mockReset().mockResolvedValue(undefined);
  interpretVoiceCommandMock.mockReset().mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
  deterministicInterpretMock.mockClear();
  localAiInterpretMock.mockClear();
  createLocalAiInterpreterMock.mockClear();
  createVoiceConversationSessionMock.mockClear();
  runtimeCapabilityMock.mockReset().mockReturnValue(null);
  localTextGenerationCapability.status.mockReset().mockResolvedValue({
    state: 'ready',
    maximumOutputTokens: 96,
  });
  localTextGenerationCapability.prepare.mockReset().mockResolvedValue(undefined);
  localTextGenerationCapability.generate.mockReset();
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true;
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false;
    },
  });
});

describe('voice command flow', () => {
  it('rejects differing speech alternatives before context or MCP resolution', async () => {
    const getContext = vi.fn(() => {
      throw new Error('context should not be read');
    });

    const result = await parseAlternatives([
      'move story 56 to done',
      'delete story 56',
    ], makeOptions(getContext));

    expect(result).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Speech matched more than one command. Review the text and try again.',
    });
    expect(getContext).not.toHaveBeenCalled();
    expect(callMcpToolMock).not.toHaveBeenCalled();
  });

  it('uses the top create command when speech alternatives differ only by title', async () => {
    const getContext = vi.fn(() => makeContext());

    const result = await parseAlternatives([
      'create story fix login',
      'create story fixed login',
    ], makeOptions(getContext));

    expect(result.ok).toBe(true);
    expect(getContext).toHaveBeenCalledTimes(1);
    if (result.ok) {
      expect(result.value.transcript).toBe('create todo fix login');
      expect(result.value.resolved.ir).toMatchObject({
        intent: 'todos.create',
        entities: { title: 'fix login' },
      });
    }
  });

  it('resolves spoken ID introducers before command execution', async () => {
    const board = makeBoard({
      columnOrder: [
        { key: 'backlog', name: 'Backlog', isDone: false },
        { key: 'testing', name: 'Testing', isDone: false },
        { key: 'done', name: 'Done', isDone: true },
      ],
      columns: {
        backlog: [{ id: 1, localId: 1, title: 'Fix login', status: 'backlog' }],
        testing: [],
        done: [],
      },
    });

    const result = await parseAlternatives(['move number one to testing'], makeOptions(() => makeContext(board)));

    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.value.transcript).toBe('move todo 1 to testing');
      expect(result.value.resolved.ir).toMatchObject({
        intent: 'todos.move',
        entities: { localId: 1, toColumnKey: 'testing' },
      });
    }
  });

  it('accepts structured alternatives that resolve to the same command IR', async () => {
    const result = await parseAlternatives([
      'move todo 56 to in progress',
      'move todo 56 to doing',
    ], makeOptions(() => makeContext()));

    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.value.resolved.ir).toMatchObject({
        intent: 'todos.move',
        entities: { localId: 56, toColumnKey: 'doing' },
      });
    }
  });

  it('accepts title speech alternatives that resolve to the same command IR', async () => {
    const board = makeBoard({
      columns: {
        backlog: [{ id: 11, localId: 11, title: 'Little Duck', status: 'backlog' }],
        doing: [],
        done: [],
      },
    });

    const result = await parseAlternatives([
      'move little duck to in progress',
      'move Little Duck to doing',
    ], makeOptions(() => makeContext(board)));

    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.value.transcript).toBe('move todo little duck to in progress');
      expect(result.value.resolved.ir).toMatchObject({
        intent: 'todos.move',
        entities: { localId: 11, toColumnKey: 'doing' },
      });
      expect(result.value.resolved.summary).toBe('Move todo #11: Little Duck to In Progress');
    }
  });

  it('rejects structured alternatives that resolve to different command IRs', async () => {
    const board = makeBoard({
      columnOrder: [
        { key: 'backlog', name: 'Backlog', isDone: false },
        { key: 'testing', name: 'Testing', isDone: false },
        { key: 'done', name: 'Done', isDone: true },
      ],
      columns: {
        backlog: [
          { id: 1, localId: 1, title: 'Fix login', status: 'backlog' },
          { id: 2, localId: 2, title: 'Fix logout', status: 'backlog' },
        ],
        testing: [],
        done: [],
      },
    });

    const result = await parseAlternatives([
      'move todo one to testing',
      'move todo two to testing',
    ], makeOptions(() => makeContext(board)));

    expect(result).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Speech matched more than one command. Review the text and try again.',
    });
  });

  it('resolves equivalent speech alternatives once', async () => {
    callMcpToolMock.mockResolvedValue({
      todo: { id: 99, localId: 99, title: 'Deferred story', status: 'backlog' },
    });
    const getContext = vi.fn(() => makeContext());

    const result = await parseAlternatives([
      'delete story 99',
      'delete story #99',
    ], makeOptions(getContext));

    expect(result.ok).toBe(true);
    expect(getContext).toHaveBeenCalledTimes(1);
    expect(callMcpToolMock).toHaveBeenCalledTimes(1);
    expect(callMcpToolMock).toHaveBeenCalledWith('todos_get', { projectSlug: 'alpha', localId: 99 }, { signal: undefined });
  });

  it('rejects title alternatives that resolve to different command IRs', async () => {
    const getContext = vi.fn(() => makeContext(makeAmbiguousLoginBoard()));

    const result = await parseAlternatives([
      'open login redirect',
      'open login validation',
    ], makeOptions(getContext));

    expect(result).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Speech matched more than one command. Review the text and try again.',
    });
    expect(getContext).toHaveBeenCalledTimes(1);
    expect(callMcpToolMock).toHaveBeenCalledWith('todos_search', { projectSlug: 'alpha', query: 'login redirect', limit: 10 }, { signal: undefined });
    expect(callMcpToolMock).toHaveBeenCalledWith('todos_search', { projectSlug: 'alpha', query: 'login validation', limit: 10 }, { signal: undefined });
  });

  it('normalizes to do speech alternatives into canonical todo text', async () => {
    const options = makeOptions(() => makeContext());

    const spoken = await parseAlternatives(['delete to do 56'], options);
    const canonical = await parseAlternatives(['delete todo 56'], options);

    expect(spoken.ok).toBe(true);
    expect(canonical.ok).toBe(true);
    if (spoken.ok && canonical.ok) {
      expect(spoken.value.transcript).toBe('delete todo 56');
      expect(spoken.value.resolved.ir).toEqual(canonical.value.resolved.ir);
    }
  });

  it('uses the same resolved pipeline for typed and speech commands', async () => {
    const options = makeOptions(() => makeContext());

    const typed = await parseAndResolveCommand('story 56 is done', options);
    const speech = await parseAlternatives(['story 56 is done'], options);

    expect(typed.ok).toBe(true);
    expect(speech.ok).toBe(true);
    if (typed.ok && speech.ok) {
      expect(speech.value.resolved.ir).toEqual(typed.value.ir);
    }
  });

  it('normalizes constrained confirmation alternatives only', () => {
    expect(parseConfirmationAlternatives(['yes'])).toEqual({ ok: true, value: 'yes' });
    expect(parseConfirmationAlternatives(['yeah', 'yep'])).toEqual({ ok: true, value: 'yes' });
    expect(parseConfirmationAlternatives(['nope'])).toEqual({ ok: true, value: 'no' });
    expect(parseConfirmationAlternatives(['cancel'])).toEqual({ ok: true, value: 'cancel' });
    expect(parseConfirmationAlternatives(['yes', 'no'])).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Confirmation was ambiguous.',
    });
    expect(parseConfirmationAlternatives(['maybe']).ok).toBe(false);
  });

  it('normalizes constrained disambiguation alternatives only', () => {
    expect(parseDisambiguationAlternatives(['first one'], 3)).toEqual({ ok: true, value: 0 });
    expect(parseDisambiguationAlternatives(['number two'], 3)).toEqual({ ok: true, value: 1 });
    expect(parseDisambiguationAlternatives(['3'], 3)).toEqual({ ok: true, value: 2 });
    expect(parseDisambiguationAlternatives(['three'], 2)).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Please say one, two, or three.',
    });
    expect(parseDisambiguationAlternatives(['one', 'two'], 3)).toEqual({
      ok: false,
      code: 'unsupported',
      message: 'Choice was ambiguous.',
    });
  });

  it('aborts dialog-local speech recognition on cancel', async () => {
    let aborted = false;
    startOneShotRecognitionMock.mockImplementation(({ signal }: { signal: AbortSignal }) =>
      new Promise(() => {
        signal.addEventListener('abort', () => {
          aborted = true;
        });
      })
    );

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    document.getElementById('voiceListenBtn')?.click();
    await flushAsync();
    document.getElementById('voiceCancelBtn')?.click();
    await flushAsync();

    expect(aborted).toBe(true);
    expect(document.getElementById('voiceCommandDialog')).toBeNull();
  });

  it('disposes conversation state on close and reopens with a fresh empty session', () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const firstSession = createVoiceConversationSessionMock.mock.results[0].value;
    firstSession.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 553,
    });

    document.getElementById('voiceCommandClose')?.click();

    expect(firstSession.getState().activeTodo).toBeNull();
    expect(() => firstSession.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 553,
    })).toThrow(/disposed/i);

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const reopenedSession = createVoiceConversationSessionMock.mock.results[1].value;
    expect(reopenedSession).not.toBe(firstSession);
    expect(reopenedSession.getState()).toEqual({
      activeProject: null,
      activeTodo: null,
      pending: null,
      lastInteraction: null,
      continuationEnabled: false,
    });
  });

  it.each([
    ['open', 'open todo 56'],
    ['move', 'move todo 56 to done'],
    ['assign', 'assign todo 56 to Ada Lovelace'],
  ])('sets active todo from the successfully executed authoritative %s resolution', async (_label, command) => {
    executeCommandIRMock.mockResolvedValue({});
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = command;

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(session.setActiveTodo).toHaveBeenCalledWith({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 56,
    });
  });

  it('clears an active todo only after that target is successfully deleted', async () => {
    executeCommandIRMock.mockResolvedValue({});
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 56,
    });
    session.setActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'delete todo 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(session.clearActiveTodo).toHaveBeenCalledTimes(1);
    expect(session.setActiveTodo).not.toHaveBeenCalled();
  });

  it('does not guess a new active todo after successful create', async () => {
    executeCommandIRMock.mockResolvedValue({});
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 56,
    });
    session.setActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'create todo Fix logout';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(session.setActiveTodo).not.toHaveBeenCalled();
    expect(session.clearActiveTodo).not.toHaveBeenCalled();
  });

  it('does not change active todo when fresh entity resolution fails', async () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    const existing = {
      kind: 'todo' as const,
      projectId: 1,
      projectSlug: 'alpha',
      localId: 56,
    };
    session.setActiveTodo(existing);
    session.setActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'open todo 999';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(session.getState().activeTodo).toEqual(existing);
    expect(session.setActiveTodo).not.toHaveBeenCalled();
    expect(session.clearActiveTodo).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('requires review again when fresh execute context resolves to a different IR', async () => {
    let context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'story 56 is done';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    context = makeContext(makeBoard({
      columnOrder: [{ key: 'complete', name: 'Complete', isDone: true }],
      columns: {
        complete: [{ id: 10, localId: 56, title: 'Fix login', status: 'complete' }],
      },
    }));
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(document.getElementById('voiceReviewStatus')?.textContent).toBe('Command changed. Review again before running.');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
  });

  it('routes typed review through the deterministic interpreter and reparses its canonical candidate', async () => {
    deterministicInterpretMock.mockResolvedValueOnce({ kind: 'candidate', command: 'open todo 56' });
    const getContext = vi.fn(() => makeContext());
    openVoiceCommandDialog(makeOptions(getContext));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Please show me the login card';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).toHaveBeenCalledTimes(1);
    expect(deterministicInterpretMock).toHaveBeenCalledWith(
      'Please show me the login card',
      { signal: expect.any(AbortSignal) },
    );
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect(transcript.value).toBe('Please show me the login card');
    expect(getContext).toHaveBeenCalledTimes(1);
    expect(callMcpToolMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('preserves a canonicalizing typed alias while reviewing through the interpreter seam', async () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'move story fifty six to in progress';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).toHaveBeenCalledWith(
      'move story fifty six to in progress',
      { signal: expect.any(AbortSignal) },
    );
    expect(document.getElementById('voiceSummary')?.textContent)
      .toBe('Move todo #56: Fix login to In Progress');
    expect(transcript.value).toBe('move story fifty six to in progress');
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('opens as VoiceFlow and writes canonical todo text after Safe-Mode speech', async () => {
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['delete to do 56'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    expect(document.querySelector('.dialog__title')?.textContent).toBe('VoiceFlow');
    document.getElementById('voiceListenBtn')?.click();
    await flushAsync();

    expect((document.getElementById('voiceTranscript') as HTMLTextAreaElement).value).toBe('delete todo 56');
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Delete todo #56: Fix login');
  });

  it('shows Safe-Mode title disambiguation candidates and executes the selected todo', async () => {
    executeCommandIRMock.mockResolvedValue({});
    openVoiceCommandDialog(makeOptions(() => makeContext(makeAmbiguousLoginBoard())));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'open login';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceDisambiguation')?.textContent).toContain('1. #12 Fix login redirect');
    expect(document.getElementById('voiceDisambiguation')?.textContent).toContain('2. #13 Fix login validation');

    document.querySelectorAll<HTMLButtonElement>('.voice-command__candidate')[1].click();
    await flushAsync();

    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #13: Fix login validation');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(executeCommandIRMock).toHaveBeenCalledWith(
      {
        intent: 'open_todo',
        projectId: 1,
        projectSlug: 'alpha',
        entities: { localId: 13 },
      },
      expect.any(Object),
    );
  });

  it('hides the Hands-Free confirmation toggle in Safe-Mode', () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));

    expect((document.getElementById('voiceHandsFreeConfirmPolicy') as HTMLElement).hidden).toBe(true);
  });

  it('shows and persists the Hands-Free confirmation toggle below the transcript', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    startOneShotRecognitionMock.mockImplementation(() => new Promise<never>(() => {}));

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const policy = document.getElementById('voiceHandsFreeConfirmPolicy') as HTMLElement;
    const toggle = document.getElementById('voiceHandsFreeConfirmToggle') as HTMLInputElement;
    const label = document.getElementById('voiceHandsFreeConfirmLabel') as HTMLElement;

    expect(policy.hidden).toBe(false);
    expect(transcript.compareDocumentPosition(policy) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(toggle.checked).toBe(false);
    expect(label.textContent).toBe('Confirm only deletes');

    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));

    expect(label.textContent).toBe('Confirm every action before execution');
    expect(localStorage.getItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY)).toBe('mutations');
  });

  it('uses a confirmation modal for destructive Safe-Mode execution', async () => {
    executeCommandIRMock.mockResolvedValue({});
    const options = makeOptions(() => makeContext());
    openVoiceCommandDialog(options);
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'delete todo 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).toHaveBeenCalledWith('Delete todo #56: Fix login', 'Confirm command', 'Delete', 'danger');
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
  });

  it('auto-listens in Hands-Free mode and executes after spoken yes confirmation', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: ['delete todo 56'] })
      .mockResolvedValueOnce({ alternatives: ['yes'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(2);
    expect(speakMock).toHaveBeenCalledWith('Delete todo #56: Fix login. Confirm?', expect.any(Object));
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['create', 'create story fix login', 'todos.create'],
    ['move', 'move story 56 to done', 'todos.move'],
    ['assign', 'assign story 56 to Ada Lovelace', 'todos.assign'],
  ])('executes %s in Hands-Free default confirmation mode without spoken confirmation', async (_label, command, intent) => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: [command] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(1);
    expect(speakMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({ intent });
  });

  it.each([
    ['create', 'create story fix login', 'Create todo "fix login". Confirm?'],
    ['move', 'move story 56 to done', 'Move todo #56: Fix login to Done. Confirm?'],
    ['assign', 'assign story 56 to Ada Lovelace', 'Assign todo #56: Fix login to Ada Lovelace. Confirm?'],
    ['delete', 'delete story 56', 'Delete todo #56: Fix login. Confirm?'],
  ])('asks for spoken confirmation before %s when Hands-Free confirmation covers mutations', async (_label, command, prompt) => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    localStorage.setItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY, 'mutations');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: [command] })
      .mockResolvedValueOnce({ alternatives: ['yes'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(2);
    expect(speakMock).toHaveBeenCalledWith(prompt, expect.any(Object));
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
  });

  it('preserves deterministic Hands-Free execution when local AI is ready', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    getVoiceInterpretationAvailabilityMock.mockResolvedValue({ state: 'ready', maximumOutputTokens: 96 });
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['open todo 56'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(1);
    expect(speakMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({
      intent: 'open_todo',
      entities: { localId: 56 },
    });
    expect(getVoiceInterpretationAvailabilityMock).not.toHaveBeenCalled();
    expect(runtimeCapabilityMock).not.toHaveBeenCalled();
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('uses Hands-Free spoken disambiguation before opening a title match', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: ['open login'] })
      .mockResolvedValueOnce({ alternatives: ['second one'] });

    openVoiceCommandDialog(makeOptions(() => makeContext(makeAmbiguousLoginBoard())));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(2);
    expect(speakMock).toHaveBeenCalledWith(
      'Which one? Option 1: Fix login redirect. Option 2: Fix login validation. Option 3: Fix login button style.',
      expect.any(Object),
    );
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({
      intent: 'open_todo',
      entities: { localId: 13 },
    });
  });

  it('aborts Hands-Free disambiguation listening when the user stops the run', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    let disambiguationAborted = false;
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: ['open login'] })
      .mockImplementationOnce(({ signal }: { signal: AbortSignal }) =>
        new Promise(() => {
          signal.addEventListener('abort', () => {
            disambiguationAborted = true;
          });
        })
      );

    openVoiceCommandDialog(makeOptions(() => makeContext(makeAmbiguousLoginBoard())));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(2);
    document.getElementById('voiceStopBtn')?.click();
    await flushAsync();

    expect(disambiguationAborted).toBe(true);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(document.getElementById('voiceFlowState')?.textContent).toBe('cancelled');
  });

  it('supersedes an in-flight Hands-Free disambiguation run cleanly', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    executeCommandIRMock.mockResolvedValue({});
    let firstDisambiguationAborted = false;
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: ['open login'] })
      .mockImplementationOnce(({ signal }: { signal: AbortSignal }) =>
        new Promise(() => {
          signal.addEventListener('abort', () => {
            firstDisambiguationAborted = true;
          });
        })
      )
      .mockResolvedValueOnce({ alternatives: ['open todo 56'] });

    let context = makeContext(makeAmbiguousLoginBoard());
    openVoiceCommandDialog(makeOptions(() => context));
    await flushAsync();

    context = makeContext();
    document.getElementById('voiceModeHandsFree')?.click();
    await flushAsync();

    expect(firstDisambiguationAborted).toBe(true);
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({
      intent: 'open_todo',
      entities: { localId: 56 },
    });
  });

  it('keeps Hands-Free delete confirmation after title disambiguation', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock
      .mockResolvedValueOnce({ alternatives: ['delete login'] })
      .mockResolvedValueOnce({ alternatives: ['second one'] })
      .mockResolvedValueOnce({ alternatives: ['yes'] });

    openVoiceCommandDialog(makeOptions(() => makeContext(makeAmbiguousLoginBoard())));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(3);
    expect(speakMock).toHaveBeenCalledWith('Delete todo #13: Fix login validation. Confirm?', expect.any(Object));
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({
      intent: 'todos.delete',
      entities: { localId: 13 },
    });
  });

  it('opens todos in Hands-Free mutation confirmation mode without spoken confirmation', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    localStorage.setItem(VOICE_FLOW_HANDS_FREE_CONFIRMATION_STORAGE_KEY, 'mutations');
    executeCommandIRMock.mockResolvedValue({});
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['open todo 56'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(startOneShotRecognitionMock).toHaveBeenCalledTimes(1);
    expect(speakMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({
      intent: 'open_todo',
      entities: { localId: 56 },
    });
  });

  it.each(['capability absent', 'capability unsupported'])
  ('keeps the same deterministic result with %s and bypasses local AI', async (state) => {
    if (state === 'capability unsupported') {
      useLocalTextGenerationStatus({ state: 'unsupported', reason: 'device' });
    }
    executeCommandIRMock.mockResolvedValue({});
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    const executeBtn = document.getElementById('voiceExecuteBtn') as HTMLButtonElement;
    transcript.value = 'open story 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(transcript.value).toBe('open story 56');
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect(executeBtn.disabled).toBe(false);
    expect((document.getElementById('voiceInterpretationPanel') as HTMLElement).hidden).toBe(true);
    expect(deterministicInterpretMock).toHaveBeenCalledWith(
      'open story 56',
      { signal: expect.any(AbortSignal) },
    );
    expect(createLocalAiInterpreterMock).not.toHaveBeenCalled();
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toEqual({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    });
  });

  it.each([
    ['unknown status', 'move story 56 to parked'],
    ['unknown member', 'assign story 56 to Grace Hopper'],
  ])('does not offer AI for downstream %s resolution failures', async (_name, command) => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = command;

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(getVoiceInterpretationAvailabilityMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('preserves browser/no-capability behavior after an unsupported deterministic command', async () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const executeBtn = document.getElementById('voiceExecuteBtn') as HTMLButtonElement;
    transcript.value = 'Could you open the login card for me?';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(runtimeCapabilityMock).toHaveBeenCalledTimes(1);
    expect((document.getElementById('voiceInterpretationPanel') as HTMLElement).hidden).toBe(true);
    expect((document.getElementById('voiceInterpretBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(document.getElementById('voiceReviewStatus')?.textContent).toBe('Unsupported command.');
    expect((document.getElementById('voiceSummary') as HTMLElement).hidden).toBe(true);
    expect(executeBtn.disabled).toBe(true);
    expect(transcript.readOnly).toBe(false);
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect(transcript.value).toBe('Could you open the login card for me?');

    transcript.value = 'open story 56';
    transcript.dispatchEvent(new Event('input', { bubbles: true }));
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect(executeBtn.disabled).toBe(false);
    expect(runtimeCapabilityMock).toHaveBeenCalledTimes(2);
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('uses ready local AI as the primary raw-input interpreter and confirms even a non-mutating action', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
    executeCommandIRMock.mockResolvedValue({});
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Could you show me the login card?';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect((document.getElementById('voiceInterpretBtn') as HTMLButtonElement).hidden).toBe(true);
    expect(deterministicInterpretMock).not.toHaveBeenCalledWith(
      'Could you show me the login card?',
      expect.anything(),
    );

    expect(interpretVoiceCommandMock).toHaveBeenCalledWith(expect.objectContaining({
      transcript: 'Could you show me the login card?',
      signal: expect.any(AbortSignal),
    }));
    expect(runtimeCapabilityMock).toHaveBeenCalledWith('local-text-generation');
    expect(createLocalAiInterpreterMock).toHaveBeenCalledWith({
      capability: localTextGenerationCapability,
      locale: 'en',
    });
    expect(localAiInterpretMock).toHaveBeenCalledWith(
      'Could you show me the login card?',
      { signal: expect.any(AbortSignal) },
    );
    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(false);
    expect(document.getElementById('voiceInterpretationOriginal')?.textContent)
      .toBe('Could you show me the login card?');
    expect(document.getElementById('voiceInterpretationCandidate')?.textContent).toBe('open story 56');
    expect(document.getElementById('voiceInterpretationAction')?.textContent).toContain('Fix login');
    expect(transcript.value).toBe('Could you show me the login card?');
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();
    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      expect.stringContaining('Fix login'),
      'Confirm interpreted command',
      expect.any(String),
      'default',
    );
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toMatchObject({ intent: 'open_todo' });
  });

  it('resolves open-current against fresh active-todo state and reuses the reviewed open path', async () => {
    useReadyLocalAi();
    let finish: ((result: {
      kind: 'semantic';
      intent: { kind: 'open-todo'; target: { kind: 'current' } };
    }) => void) | undefined;
    interpretVoiceCommandMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    executeCommandIRMock.mockResolvedValue({});
    const board = makeBoard({
      columns: {
        backlog: [],
        doing: [{ id: 10, localId: 553, title: 'Original title', status: 'doing' }],
        done: [],
      },
    });
    const options = makeOptions(() => makeContext(board));
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 553,
    });
    session.setActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Open it';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    board.columns.doing[0].title = 'Fresh authoritative title';
    finish?.({
      kind: 'semantic',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    await flushAsync();

    expect(document.getElementById('voiceInterpretationOriginal')?.textContent).toBe('Open it');
    expect(document.getElementById('voiceInterpretationCandidate')?.textContent).toBe('Open it');
    expect(document.getElementById('voiceInterpretationAction')?.textContent)
      .toBe('Open todo #553: Fresh authoritative title');
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      'Open todo #553: Fresh authoritative title',
      'Confirm interpreted command',
      'Open',
      'default',
    );
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toEqual({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 553 },
    });
    expect(session.setActiveTodo).toHaveBeenCalledWith({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 553,
    });
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
  });

  it('keeps completed turns only when Continue conversation is on and clears context when turned off', async () => {
    executeCommandIRMock.mockResolvedValue({});
    const context = makeContext();
    const options = makeOptions(() => context);
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    const toggle = document.getElementById('voiceContinueConversationToggle') as HTMLInputElement;
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;

    expect(toggle.checked).toBe(false);
    expect(localStorage.getItem(VOICE_FLOW_CONTINUE_CONVERSATION_STORAGE_KEY)).toBeNull();
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    expect(session.getState().continuationEnabled).toBe(true);

    transcript.value = 'open todo 56';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(document.getElementById('voiceCommandDialog')).not.toBeNull();
    expect(transcript.value).toBe('');
    expect(session.getState().activeTodo).toMatchObject({ localId: 56, projectSlug: 'alpha' });

    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    transcript.value = 'Open it';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(executeCommandIRMock).toHaveBeenCalledTimes(2);
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
    expect(document.getElementById('voiceCommandDialog')).not.toBeNull();
    expect(session.getState().activeTodo).toMatchObject({ localId: 56 });

    toggle.checked = false;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    expect(session.getState()).toMatchObject({
      activeTodo: null,
      pending: null,
      continuationEnabled: false,
    });
    expect(localStorage.getItem(VOICE_FLOW_CONTINUE_CONVERSATION_STORAGE_KEY)).toBe('false');
  });

  it('asks for a missing title, binds the answer to the concrete pending target, confirms, and executes once', async () => {
    useReadyLocalAi();
    executeCommandIRMock.mockResolvedValue({});
    interpretVoiceCommandMock
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      })
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: {
          kind: 'update-todo-title',
          target: { kind: 'current' },
          title: 'Fix the login race condition',
        },
      });
    const context = makeContext();
    const options = makeOptions(() => context);
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56,
    });
    const toggle = document.getElementById('voiceContinueConversationToggle') as HTMLInputElement;
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;

    transcript.value = 'Change the title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceSemanticInteraction')?.textContent)
      .toBe('What would you like to change the title to?');
    expect((document.getElementById('voiceSemanticInteraction') as HTMLElement).hidden).toBe(false);
    expect(transcript.value).toBe('');
    expect(session.getState().pending).toEqual({
      kind: 'missing-slot',
      operation: 'todo.update_title',
      slot: 'title',
      intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      selection: {},
      target: { kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 },
    });
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    transcript.value = 'Fix the login race condition';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(localAiInterpretMock.mock.calls[1]).toEqual([
      'Fix the login race condition',
      {
        signal: expect.any(AbortSignal),
        conversation: {
          pending: {
            kind: 'missing-slot',
            operation: 'todo.update_title',
            slot: 'title',
          },
        },
      },
    ]);
    expect(document.getElementById('voiceSummary')?.textContent)
      .toBe('Change the title of #56 to "Fix the login race condition"');

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      'Change the title of #56 to "Fix the login race condition"',
      'Confirm interpreted command',
      'Change title',
      'default',
    );
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toEqual({
      intent: 'todos.update_title',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56, title: 'Fix the login race condition' },
    });
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(2);
    expect(session.getState().pending).toBeNull();
    expect(session.getState().activeTodo).toEqual({
      kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56,
    });
    expect(JSON.stringify(session.getState().activeTodo)).not.toContain('Fix the login race condition');
    expect(document.getElementById('voiceSemanticInteraction')?.textContent)
      .toBe('Title updated successfully.');
    expect(document.getElementById('voiceCommandDialog')).not.toBeNull();
  });

  it('keeps a missing-title clarification alive with Continue conversation off, then closes after success', async () => {
    useReadyLocalAi();
    executeCommandIRMock.mockResolvedValue({});
    interpretVoiceCommandMock
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      })
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: 'New title' },
      });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 });
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;

    transcript.value = 'Change the title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(document.getElementById('voiceCommandDialog')).not.toBeNull();
    expect(session.getState().continuationEnabled).toBe(false);

    transcript.value = 'New title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(document.getElementById('voiceCommandDialog')).toBeNull();
  });

  it('cancels a reviewed pending title without mutation while preserving the active todo', async () => {
    useReadyLocalAi();
    showConfirmDialogMock.mockResolvedValue(false);
    interpretVoiceCommandMock
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      })
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: 'New title' },
      });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 });
    const toggle = document.getElementById('voiceContinueConversationToggle') as HTMLInputElement;
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;

    transcript.value = 'Change the title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    transcript.value = 'New title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(session.getState().pending).toBeNull();
    expect(session.getState().activeTodo).toMatchObject({ localId: 56 });
    expect(document.getElementById('voiceCommandDialog')).not.toBeNull();
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
  });

  it('fails a pending title answer stale when its concrete todo disappeared', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
      })
      .mockResolvedValueOnce({
        kind: 'semantic',
        intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: 'New title' },
      });
    const sourceBoard = makeBoard();
    openVoiceCommandDialog(makeOptions(() => makeContext(sourceBoard)));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 });
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;

    transcript.value = 'Change the title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    sourceBoard.columns.doing = [];
    transcript.value = 'New title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(session.getState().activeTodo).toBeNull();
    expect(session.getState().pending).toBeNull();
    expect(document.getElementById('voiceReviewStatus')?.textContent)
      .toBe('The board changed before the command could run.');
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('reviews a direct current-title value without creating a missing-slot question', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: {
        kind: 'update-todo-title',
        target: { kind: 'current' },
        title: 'Fix the login race condition',
      },
    });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 });
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Change its title to Fix the login race condition';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(session.getState().pending).toBeNull();
    expect((document.getElementById('voiceSemanticInteraction') as HTMLElement).hidden).toBe(true);
    expect(document.getElementById('voiceSummary')?.textContent)
      .toBe('Change the title of #56 to "Fix the login race condition"');
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('clears pending and active conversational context when the project changes', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: { kind: 'update-todo-title', target: { kind: 'current' }, title: null },
    });
    let context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({ kind: 'todo', projectId: 1, projectSlug: 'alpha', localId: 56 });
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Change the title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(session.getState().pending).not.toBeNull();

    context = {
      ...makeContext(),
      projectId: 2,
      projectSlug: 'beta',
    };
    transcript.value = 'New title';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(session.getState().activeTodo).toBeNull();
    expect(session.getState().pending).toBeNull();
    expect(document.getElementById('voiceReviewStatus')?.textContent)
      .toBe('The board changed before the command could run.');
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('fails open-current safely when no active todo exists', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    const context = makeContext();
    const options = makeOptions(() => context);
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Open this card';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceReviewStatus')?.textContent)
      .toBe('Todo reference is required.');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
    expect(session.clearActiveTodo).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(options.openTodo).not.toHaveBeenCalled();
  });

  it('clears a project-mismatched active todo instead of opening it', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    const context = makeContext();
    const options = makeOptions(() => context);
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo',
      projectId: 2,
      projectSlug: 'beta',
      localId: 553,
    });
    session.clearActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Open that one';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(session.clearActiveTodo).toHaveBeenCalledTimes(1);
    expect(session.getState().activeTodo).toBeNull();
    expect(document.getElementById('voiceReviewStatus')?.textContent)
      .toBe('The board changed before the command could run.');
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(options.openTodo).not.toHaveBeenCalled();
  });

  it('rechecks open-current at execution and clears a todo that disappeared after review', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({
      kind: 'semantic',
      intent: { kind: 'open-todo', target: { kind: 'current' } },
    });
    const board = makeBoard({
      columns: {
        backlog: [],
        doing: [{ id: 10, localId: 553, title: 'Will disappear', status: 'doing' }],
        done: [],
      },
    });
    const options = makeOptions(() => makeContext(board));
    openVoiceCommandDialog(options);
    const session = createVoiceConversationSessionMock.mock.results[0].value;
    session.setActiveTodo({
      kind: 'todo',
      projectId: 1,
      projectSlug: 'alpha',
      localId: 553,
    });
    session.clearActiveTodo.mockClear();
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Open it';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(document.getElementById('voiceSummary')?.textContent)
      .toBe('Open todo #553: Will disappear');

    board.columns.doing = [];
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(session.clearActiveTodo).toHaveBeenCalledTimes(1);
    expect(session.getState().activeTodo).toBeNull();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(options.openTodo).not.toHaveBeenCalled();
  });

  it('executes an AI-primary create in the deterministic default lane with success confirmation semantics', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'create todo Clean the garage' });
    executeCommandIRMock.mockResolvedValue({});
    const sourceBoard = makeBoard({
      columnOrder: [
        { key: 'todo', name: 'To Do', isDone: false },
        { key: 'done', name: 'Done', isDone: true },
      ],
      columns: { todo: [], done: [] },
    });
    const context = makeContext(sourceBoard);
    const options = makeOptions(() => context);
    const deterministic = await parseAndResolveCommand('create todo Clean the garage', options);
    if (!deterministic.ok) throw new Error('deterministic create did not resolve');

    openVoiceCommandDialog(options);
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Create a to-do about cleaning the garage today';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(interpretVoiceCommandMock).toHaveBeenCalledWith(expect.objectContaining({
      transcript: 'Create a to-do about cleaning the garage today',
      signal: expect.any(AbortSignal),
    }));
    expect(deterministicInterpretMock).not.toHaveBeenCalledWith(
      'Create a to-do about cleaning the garage today',
      expect.anything(),
    );
    expect(document.getElementById('voiceInterpretationCandidate')?.textContent)
      .toBe('create todo Clean the garage');

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      'Create todo "Clean the garage"',
      'Confirm interpreted command',
      'Create',
      'success',
    );
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
    expect(executeCommandIRMock.mock.calls[0][0]).toEqual(deterministic.value.ir);
    expect(executeCommandIRMock.mock.calls[0][0]).toEqual({
      intent: 'todos.create',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { title: 'Clean the garage', columnKey: 'todo' },
    });
  });

  it('keeps AI-derived delete confirmation destructive', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'delete todo 56' });
    executeCommandIRMock.mockResolvedValue({});
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Please delete the login todo';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(localAiInterpretMock).toHaveBeenCalledWith(
      'Please delete the login todo',
      { signal: expect.any(AbortSignal) },
    );
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Delete todo #56: Fix login');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(false);
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      'Delete todo #56: Fix login',
      'Confirm interpreted command',
      'Delete',
      'danger',
    );
    expect(executeCommandIRMock).toHaveBeenCalledTimes(1);
  });

  it('refuses a local-AI create with unrepresented meaning without proposal, resolution, or mutation', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'refused' });
    const context = makeContext();
    const options = makeOptions(() => context);
    openVoiceCommandDialog(options);
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Create a to-do about cleaning the garage today';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(localAiInterpretMock).toHaveBeenCalledWith(
      'Create a to-do about cleaning the garage today',
      { signal: expect.any(AbortSignal) },
    );
    expect(document.getElementById('voiceInterpretationStatus')?.textContent)
      .toBe('That request could not be converted into one supported command. Edit it and try again.');
    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(true);
    expect((document.getElementById('voiceSummary') as HTMLElement).hidden).toBe(true);
    expect((document.getElementById('voiceUseBasicBtn') as HTMLButtonElement).hidden).toBe(false);
    expect(deterministicInterpretMock).not.toHaveBeenCalled();
    expect(transcript.value).toBe('Create a to-do about cleaning the garage today');
    expect(callMcpToolMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(showConfirmDialogMock).not.toHaveBeenCalled();
    expect(options.recordMutation).not.toHaveBeenCalled();
  });

  it('cannot execute an AI proposal when the original input changes during confirmation', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
    let finishConfirmation: ((confirmed: boolean) => void) | undefined;
    showConfirmDialogMock.mockImplementation(() => new Promise((resolve) => { finishConfirmation = resolve; }));
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    const form = document.getElementById('voiceCommandForm') as HTMLFormElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushAsync();
    transcript.value = 'a different request';
    transcript.dispatchEvent(new Event('input', { bubbles: true }));
    finishConfirmation?.(true);
    await flushAsync();

    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('never prepares automatically and prepares only after the explicit setup tap', async () => {
    runtimeCapabilityMock.mockReturnValue(localTextGenerationCapability);
    localTextGenerationCapability.status
      .mockResolvedValueOnce({ state: 'action-required', action: 'download' })
      .mockResolvedValueOnce({ state: 'ready', maximumOutputTokens: 96 });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect((document.getElementById('voicePrepareBtn') as HTMLButtonElement).hidden).toBe(false);

    document.getElementById('voicePrepareBtn')?.click();
    await flushAsync();
    expect(prepareVoiceInterpretationMock).toHaveBeenCalledWith(
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    expect(localTextGenerationCapability.status).toHaveBeenCalledTimes(2);
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
  });

  it('rejects a canonical candidate that the deterministic parser cannot accept', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'say hello to the board' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Please greet everybody';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceInterpretationStatus')?.textContent)
      .toContain('could not be safely understood');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it.each(['user', 'project', 'board'] as const)('discards a late inference when the %s context changes', async (changedContext) => {
    useReadyLocalAi();
    let finish: ((result: { kind: 'candidate'; command: string }) => void) | undefined;
    interpretVoiceCommandMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    let context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    if (changedContext === 'user') {
      context = { ...context, userId: 8 };
    } else if (changedContext === 'project') {
      context = { ...context, projectId: 2, projectSlug: 'replacement' };
    } else {
      context = makeContext(makeBoard({ project: { ...makeBoard().project!, name: 'Replacement' } }));
    }
    finish?.({ kind: 'candidate', command: 'open story 56' });
    await flushAsync();

    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(true);
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
  });

  it('resolves the canonical candidate against fresh contents of the still-current board', async () => {
    useReadyLocalAi();
    let finish: ((result: { kind: 'candidate'; command: string }) => void) | undefined;
    interpretVoiceCommandMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const board = makeBoard();
    openVoiceCommandDialog(makeOptions(() => makeContext(board)));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    board.columns.doing[0].title = 'Fresh login title';
    finish?.({ kind: 'candidate', command: 'open story 56' });
    await flushAsync();

    expect(document.getElementById('voiceInterpretationAction')?.textContent)
      .toContain('Fresh login title');
  });

  it('aborts interpretation on dialog close and replacement review', async () => {
    useReadyLocalAi();
    const inferenceSignals: AbortSignal[] = [];
    interpretVoiceCommandMock.mockImplementation(({ signal }) => {
      inferenceSignals.push(signal);
      return new Promise(() => {});
    });
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(inferenceSignals[0]?.aborted).toBe(true);

    const replacementSignal = inferenceSignals[1];
    document.getElementById('voiceCommandClose')?.click();
    expect(replacementSignal.aborted).toBe(true);
  });

  it('aborts and clears an AI proposal when the app backgrounds', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(false);

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));

    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(true);
    expect(transcript.value).toBe('Could you show me the login card?');
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('aborts an active inference when the app backgrounds and never replays it on resume', async () => {
    useReadyLocalAi();
    let inferenceSignal: AbortSignal | undefined;
    interpretVoiceCommandMock.mockImplementation(({ signal }) => {
      inferenceSignal = signal;
      return new Promise(() => {});
    });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    expect(inferenceSignal?.aborted).toBe(true);

    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('scrumboy:native-foreground'));
    await flushAsync();
    expect(localTextGenerationCapability.status).toHaveBeenCalledTimes(1);
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
  });

  it('preserves existing authorization checks for an AI-derived mutation', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'move story 56 to done' });
    const context = { ...makeContext(), role: 'viewer' };
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you put login in done?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(true);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it.each([
    ['busy', 'On-device interpretation is busy'],
    ['quota_exceeded', 'temporarily unavailable'],
  ])('preserves the transcript after %s inference errors', async (code, expectedMessage) => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockRejectedValue(new LocalTextGenerationError(code as 'busy' | 'quota_exceeded'));
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(transcript.value).toBe('Could you show me the login card?');
    expect(document.getElementById('voiceInterpretationStatus')?.textContent).toContain(expectedMessage);
    expect(deterministicInterpretMock).not.toHaveBeenCalled();
    expect((document.getElementById('voiceUseBasicBtn') as HTMLButtonElement).hidden).toBe(false);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('does not silently select deterministic while setup is required and offers an explicit basic turn', async () => {
    useLocalTextGenerationStatus({ state: 'action-required', action: 'download' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'open story 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect((document.getElementById('voicePrepareBtn') as HTMLButtonElement).hidden).toBe(false);
    expect((document.getElementById('voiceUseBasicBtn') as HTMLButtonElement).hidden).toBe(false);

    document.getElementById('voiceUseBasicBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).toHaveBeenCalledTimes(1);
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(false);
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('rechecks temporarily unavailable capability state on an explicit Retry turn', async () => {
    runtimeCapabilityMock.mockReturnValue(localTextGenerationCapability);
    localTextGenerationCapability.status
      .mockResolvedValueOnce({ state: 'temporarily-unavailable', reason: 'provider' })
      .mockResolvedValueOnce({ state: 'ready', maximumOutputTokens: 96 });
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect(document.getElementById('voiceInterpretationStatus')?.textContent)
      .toContain('temporarily unavailable');
    expect((document.getElementById('voiceInterpretRetryBtn') as HTMLButtonElement).hidden).toBe(false);
    expect((document.getElementById('voiceUseBasicBtn') as HTMLButtonElement).hidden).toBe(false);

    document.getElementById('voiceInterpretRetryBtn')?.click();
    await flushAsync();

    expect(localTextGenerationCapability.status).toHaveBeenCalledTimes(2);
    expect(localAiInterpretMock).toHaveBeenCalledTimes(1);
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
  });

  it.each([
    ['project-scoped command', 'move story 56 to done in project beta', 'Project scope is fixed by the current board.'],
    ['invalid identifier', 'move number to testing', 'Todo ID was not recognized.'],
  ])('does not offer AI for a deterministic %s failure', async (_name, command, message) => {
    getVoiceInterpretationAvailabilityMock.mockResolvedValue({ state: 'ready', maximumOutputTokens: 96 });
    openVoiceCommandDialog(makeOptions(() => makeContext()));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = command;

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(deterministicInterpretMock).toHaveBeenCalledWith(
      command,
      { signal: expect.any(AbortSignal) },
    );
    expect(document.getElementById('voiceReviewStatus')?.textContent).toBe(message);
    expect((document.getElementById('voiceInterpretationPanel') as HTMLElement).hidden).toBe(true);
    expect(getVoiceInterpretationAvailabilityMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('has no hidden fallback after AI failure and uses deterministic only after the explicit basic action', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockRejectedValue(new LocalTextGenerationError('internal'));
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'open story 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
    expect(deterministicInterpretMock).not.toHaveBeenCalled();
    expect(transcript.value).toBe('open story 56');
    expect(executeCommandIRMock).not.toHaveBeenCalled();

    document.getElementById('voiceUseBasicBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect((document.getElementById('voiceExecuteBtn') as HTMLButtonElement).disabled).toBe(false);
    expect(deterministicInterpretMock).toHaveBeenCalledTimes(1);
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).toHaveBeenCalledTimes(1);
  });

  it('keeps Hands-Free deterministic-only after an unsupported speech command', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
    getVoiceInterpretationAvailabilityMock.mockResolvedValue({ state: 'ready', maximumOutputTokens: 96 });
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['Could you show me the login card?'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    await flushAsync();

    expect(getVoiceInterpretationAvailabilityMock).not.toHaveBeenCalled();
    expect(prepareVoiceInterpretationMock).not.toHaveBeenCalled();
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
    expect(executeCommandIRMock).not.toHaveBeenCalled();
  });

  it('cannot carry an AI-derived proposal into Hands-Free auto-execution', async () => {
    useReadyLocalAi();
    interpretVoiceCommandMock.mockResolvedValue({ kind: 'candidate', command: 'open story 56' });
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['unsupported hands free request'] });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(false);

    document.getElementById('voiceModeHandsFree')?.click();
    await flushAsync();

    expect((document.getElementById('voiceInterpretationProposal') as HTMLElement).hidden).toBe(true);
    expect(executeCommandIRMock).not.toHaveBeenCalled();
    expect(showConfirmDialogMock).not.toHaveBeenCalled();
  });
});

afterEach(() => {
  document.getElementById('voiceCommandDialog')?.dispatchEvent(new Event('voice-command:close'));
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
});
