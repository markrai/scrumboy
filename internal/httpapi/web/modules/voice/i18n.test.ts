// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';
import enCatalog from '../i18n/locales/en.json';
import deCatalog from '../i18n/locales/de.json';
import {
  I18N_LOCALE_CHANGED,
  initI18n,
  resetI18nForTests,
  setLocale,
} from '../i18n/index.js';

const callMcpToolMock = vi.hoisted(() => vi.fn());
const executeCommandIRMock = vi.hoisted(() => vi.fn());
const startOneShotRecognitionMock = vi.hoisted(() => vi.fn());
const speakMock = vi.hoisted(() => vi.fn());
const showConfirmDialogMock = vi.hoisted(() => vi.fn());
const getVoiceInterpretationAvailabilityMock = vi.hoisted(() => vi.fn());
const prepareVoiceInterpretationMock = vi.hoisted(() => vi.fn());
const interpretVoiceCommandMock = vi.hoisted(() => vi.fn());
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
vi.mock('../platform/runtime.js', () => ({
  getAppRuntime: () => ({ capability: runtimeCapabilityMock }),
}));
vi.mock('../utils.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../utils.js')>();
  return { ...actual, showConfirmDialog: showConfirmDialogMock };
});

import {
  openVoiceCommandDialog,
  parseConfirmationAlternatives,
  parseDisambiguationAlternatives,
  type OpenVoiceCommandOptions,
  type VoiceCommandDialogContext,
} from './flow.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import { validateCommandIR } from './schema.js';
import { resolveTodoTarget } from './target-resolver.js';

const en = enCatalog as Record<string, string>;
const de = deCatalog as Record<string, string>;

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

async function initLocale(locale: 'en' | 'de' = 'en') {
  resetI18nForTests();
  await initI18n({
    locale,
    loadLocale: vi.fn(async (nextLocale: 'en' | 'de') => (nextLocale === 'de' ? de : en)),
  });
}

async function flushAsync(): Promise<void> {
  for (let i = 0; i < 30; i += 1) {
    await Promise.resolve();
  }
}

describe('VoiceFlow i18n', () => {
  beforeEach(async () => {
    document.body.innerHTML = '';
    localStorage.clear();
    callMcpToolMock.mockReset();
    executeCommandIRMock.mockReset();
    startOneShotRecognitionMock.mockReset();
    speakMock.mockReset().mockResolvedValue(undefined);
    showConfirmDialogMock.mockReset().mockResolvedValue(true);
    getVoiceInterpretationAvailabilityMock.mockReset().mockResolvedValue({ state: 'absent' });
    prepareVoiceInterpretationMock.mockReset().mockResolvedValue(undefined);
    interpretVoiceCommandMock.mockReset().mockResolvedValue({ kind: 'refused' });
    runtimeCapabilityMock.mockReset().mockReturnValue(null);
    localTextGenerationCapability.status.mockReset();
    localTextGenerationCapability.prepare.mockReset();
    localTextGenerationCapability.generate.mockReset();
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
    await initLocale('en');
  });

  afterEach(() => {
    document.getElementById('voiceCommandDialog')?.dispatchEvent(new Event('voice-command:close'));
    resetI18nForTests();
    document.body.innerHTML = '';
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('renders localized VoiceFlow chrome and state labels in English and German', async () => {
    openVoiceCommandDialog(makeOptions(() => makeContext()));

    expect(document.getElementById('voiceModeSafe')?.textContent).toBe(en['voice.mode.safe']);
    expect(document.getElementById('voiceListenBtn')?.textContent).toBe(en['voice.action.listen']);
    expect(document.getElementById('voiceFlowState')?.textContent).toBe(en['voice.state.idle']);

    await setLocale('de');
    await flushAsync();

    expect(document.getElementById('voiceModeSafe')?.textContent).toBe(de['voice.mode.safe']);
    expect(document.getElementById('voiceListenBtn')?.textContent).toBe(de['voice.action.listen']);
    expect(document.getElementById('voiceFlowState')?.textContent).toBe(de['voice.state.idle']);
  });

  it('localizes enhanced recovery controls and returns a non-English turn to deterministic VoiceFlow', async () => {
    runtimeCapabilityMock.mockReturnValue(localTextGenerationCapability);
    localTextGenerationCapability.status.mockResolvedValue({ state: 'action-required', action: 'download' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(document.getElementById('voiceUseBasicBtn')?.textContent).toBe(en['voice.ai.useBasic']);
    expect((document.getElementById('voiceUseBasicBtn') as HTMLButtonElement).hidden).toBe(false);

    await setLocale('de');
    await flushAsync();

    expect(document.getElementById('voiceUseBasicBtn')?.textContent).toBe(de['voice.ai.useBasic']);
    expect((document.getElementById('voiceInterpretationPanel') as HTMLElement).hidden).toBe(true);
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(document.getElementById('voiceReviewStatus')?.textContent).toBe(de['voice.errors.unsupportedCommand']);
    expect((document.getElementById('voiceInterpretationPanel') as HTMLElement).hidden).toBe(true);
    expect(interpretVoiceCommandMock).not.toHaveBeenCalled();
  });

  it('removes interpretation locale and foreground listeners when the dialog closes', async () => {
    runtimeCapabilityMock.mockReturnValue(localTextGenerationCapability);
    localTextGenerationCapability.status.mockResolvedValue({ state: 'action-required', action: 'download' });
    const context = makeContext();
    openVoiceCommandDialog(makeOptions(() => context));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'Could you show me the login card?';
    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();
    expect(localTextGenerationCapability.status).toHaveBeenCalledTimes(1);

    document.getElementById('voiceCommandClose')?.click();
    await setLocale('de');
    window.dispatchEvent(new Event('scrumboy:native-foreground'));
    await flushAsync();

    expect(localTextGenerationCapability.status).toHaveBeenCalledTimes(1);
  });

  it('updates an open dialog after locale change without reopening or re-resolving', async () => {
    const getContext = vi.fn(() => makeContext());
    openVoiceCommandDialog(makeOptions(getContext));
    const transcript = document.getElementById('voiceTranscript') as HTMLTextAreaElement;
    transcript.value = 'open todo 56';

    document.getElementById('voiceReviewBtn')?.click();
    await flushAsync();

    expect(getContext).toHaveBeenCalledTimes(1);
    expect(document.getElementById('voiceSummary')?.textContent).toBe('Open todo #56: Fix login');
    expect(document.getElementById('voiceExecuteBtn')?.textContent).toBe('Open');
    expect(document.getElementById('voiceFlowState')?.textContent).toBe(en['voice.state.resolvedTarget']);

    await setLocale('de');
    await flushAsync();

    expect(getContext).toHaveBeenCalledTimes(1);
    expect(callMcpToolMock).not.toHaveBeenCalled();
    expect(document.getElementById('voiceSummary')?.textContent).toBe(de['voice.summary.open']
      .replace('{localId}', '56')
      .replace('{title}', 'Fix login'));
    expect(document.getElementById('voiceExecuteBtn')?.textContent).toBe(de['voice.action.open']);
    expect(document.getElementById('voiceFlowState')?.textContent).toBe(de['voice.state.resolvedTarget']);
  });

  it('returns localized parser, resolver, target resolver, and schema errors', async () => {
    await setLocale('de');

    expect(parseCommand('move number to testing')).toMatchObject({
      ok: false,
      code: 'invalid_id',
      message: de['voice.errors.invalidId'],
    });

    const parsed = parseCommand('move todo 56 to missing');
    if (!parsed.ok) throw new Error('parse failed');
    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: 1,
      projectSlug: 'alpha',
      board: makeBoard(),
      members,
    });
    expect(resolved).toMatchObject({
      ok: false,
      code: 'unknown_status',
      message: de['voice.errors.statusNotFound'],
    });

    const target = await resolveTodoTarget(
      { kind: 'id', localId: 99, ambiguousId: false, display: '99' },
      { projectSlug: 'alpha', board: makeBoard() },
    );
    expect(target).toMatchObject({
      ok: false,
      code: 'unknown_story',
      message: de['voice.errors.todoNotFound'].replace('{localId}', '99'),
    });

    const schema = validateCommandIR({
      intent: 'todos.delete',
      projectId: 2,
      projectSlug: 'beta',
      entities: { localId: 56 },
    }, { projectId: 1, projectSlug: 'alpha', board: makeBoard() });
    expect(schema).toMatchObject({
      ok: false,
      code: 'stale_context',
      message: de['voice.errors.staleContext'],
    });
  });

  it('keeps English fallback messages before the full i18n catalog is initialized', () => {
    resetI18nForTests();

    expect(parseCommand('')).toMatchObject({
      ok: false,
      code: 'unsupported',
      message: 'Command is required.',
    });

    expect(validateCommandIR({
      intent: 'todos.delete',
      projectId: 2,
      projectSlug: 'beta',
      entities: { localId: 56 },
    }, { projectId: 1, projectSlug: 'alpha', board: makeBoard() })).toMatchObject({
      ok: false,
      code: 'stale_context',
      message: 'The board changed before the command could run.',
    });
  });

  it('keeps speech recognition vocabulary English-only', async () => {
    await setLocale('de');

    expect(parseConfirmationAlternatives(['yes'])).toEqual({ ok: true, value: 'yes' });
    expect(parseConfirmationAlternatives(['ja'])).toMatchObject({
      ok: false,
      code: 'unsupported',
      message: de['voice.errors.confirmationRequired'],
    });
    expect(parseDisambiguationAlternatives(['one'], 3)).toEqual({ ok: true, value: 0 });
    expect(parseDisambiguationAlternatives(['eins'], 3)).toMatchObject({
      ok: false,
      code: 'unsupported',
      message: de['voice.errors.choiceRequired'],
    });
  });

  it('cleans up locale listeners across open and close cycles', async () => {
    const addSpy = vi.spyOn(document, 'addEventListener');
    const removeSpy = vi.spyOn(document, 'removeEventListener');
    const localeAdds = () => addSpy.mock.calls.filter(([type]) => type === I18N_LOCALE_CHANGED).length;
    const localeRemoves = () => removeSpy.mock.calls.filter(([type]) => type === I18N_LOCALE_CHANGED).length;

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    expect(localeAdds()).toBe(1);

    document.getElementById('voiceCancelBtn')?.click();
    await flushAsync();
    expect(localeRemoves()).toBe(1);
    expect(document.getElementById('voiceCommandDialog')).toBeNull();

    openVoiceCommandDialog(makeOptions(() => makeContext()));
    expect(localeAdds()).toBe(2);
    document.getElementById('voiceCancelBtn')?.click();
    await setLocale('de');
    await flushAsync();

    expect(localeRemoves()).toBe(2);
    expect(document.getElementById('voiceCommandDialog')).toBeNull();
  });
});
