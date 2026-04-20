// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';

const callMcpToolMock = vi.hoisted(() => vi.fn());
const executeCommandIRMock = vi.hoisted(() => vi.fn());
const startOneShotRecognitionMock = vi.hoisted(() => vi.fn());
const speakMock = vi.hoisted(() => vi.fn());
const showConfirmDialogMock = vi.hoisted(() => vi.fn());

vi.mock('./mcp-client.js', () => ({ callMcpTool: callMcpToolMock }));
vi.mock('./execute.js', () => ({ executeCommandIR: executeCommandIRMock }));
vi.mock('./speech.js', () => ({ startOneShotRecognition: startOneShotRecognitionMock }));
vi.mock('./speech-output.js', () => ({ speak: speakMock }));
vi.mock('../utils.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../utils.js')>();
  return { ...actual, showConfirmDialog: showConfirmDialogMock };
});

import {
  openVoiceCommandDialog,
  parseAlternatives,
  parseAndResolveCommand,
  parseConfirmationAlternatives,
  type OpenVoiceCommandOptions,
  type VoiceCommandDialogContext,
} from './flow.js';
import { VOICE_FLOW_MODE_STORAGE_KEY } from '../core/voiceflow-preferences.js';

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

function makeContext(board = makeBoard()): VoiceCommandDialogContext {
  return {
    projectId: 1,
    projectSlug: 'alpha',
    board,
    members,
    role: 'maintainer',
  };
}

function makeOptions(getContext: () => VoiceCommandDialogContext | null): OpenVoiceCommandOptions {
  return {
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
  for (let i = 0; i < 14; i += 1) {
    await Promise.resolve();
  }
}

beforeEach(() => {
  document.body.innerHTML = '';
  localStorage.clear();
  callMcpToolMock.mockReset();
  executeCommandIRMock.mockReset();
  startOneShotRecognitionMock.mockReset();
  speakMock.mockReset().mockResolvedValue(undefined);
  showConfirmDialogMock.mockReset().mockResolvedValue(true);
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
    expect(callMcpToolMock).toHaveBeenCalledWith('todos.get', { projectSlug: 'alpha', localId: 99 }, { signal: undefined });
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

  it('opens as VoiceFlow and writes canonical todo text after Safe-Mode speech', async () => {
    startOneShotRecognitionMock.mockResolvedValueOnce({ alternatives: ['delete to do 56'] });

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    expect(document.querySelector('.dialog__title')?.textContent).toBe('VoiceFlow');
    document.getElementById('voiceListenBtn')?.click();
    await flushAsync();

    expect((document.getElementById('voiceTranscript') as HTMLTextAreaElement).value).toBe('delete todo 56');
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Delete todo #56: Fix login');
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

    expect(showConfirmDialogMock).toHaveBeenCalledWith('Delete todo #56: Fix login', 'Confirm command', 'Delete');
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

  it('opens non-destructive todos in Hands-Free mode without spoken confirmation', async () => {
    localStorage.setItem(VOICE_FLOW_MODE_STORAGE_KEY, 'hands-free');
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
});
