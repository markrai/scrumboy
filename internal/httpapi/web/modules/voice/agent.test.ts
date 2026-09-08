// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';
import { initI18n, setLocale } from '../i18n/index.js';
import en from '../i18n/locales/en.json';
import de from '../i18n/locales/de.json';

const controller = vi.hoisted(() => ({
  getView: vi.fn(),
  getConversationState: vi.fn(),
  matchesContext: vi.fn().mockReturnValue(false),
  startListening: vi.fn().mockResolvedValue(undefined),
  submitTranscript: vi.fn(),
  stopListening: vi.fn(),
  confirm: vi.fn(),
  cancelConfirmation: vi.fn(),
  chooseClarification: vi.fn(),
  cancelClarification: vi.fn(),
  setContinuationEnabled: vi.fn(),
  invalidate: vi.fn(),
  close: vi.fn(),
}));
const agentView = vi.hoisted(() => ({
  render: null as null | ((view: Record<string, unknown>) => void),
}));

vi.mock('./local-agent-controller.js', () => ({
  createVoiceAgentController: vi.fn((options) => {
    agentView.render = options.onView;
    options.onView({
      phase: 'ready',
      status: { key: 'voice.status.ready', fallback: 'Ready' },
      activity: 'idle',
      activityStatus: null,
      confirmation: null,
      clarification: null,
    });
    return controller;
  }),
}));

import { closeVoiceAgent, openVoiceAgent } from './agent.js';

const localTextGeneration: LocalTextGenerationCapability = {
  status: vi.fn(),
  prepare: vi.fn(),
  generate: vi.fn(),
};
const speechInput: SpeechInputCapability = {
  status: vi.fn(),
  listen: vi.fn(),
};

function open() {
  openVoiceAgent({
    initialUserId: 7,
    initialProjectId: 1,
    initialProjectSlug: 'alpha',
    getContext: vi.fn(),
    refreshBoard: vi.fn(),
    openTodo: vi.fn(),
    localTextGeneration,
    speechInput,
    onUseBasic: vi.fn(),
  });
}

beforeEach(async () => {
  localStorage.removeItem('scrumboy_voice_create_v2');
  await initI18n({ locale: 'en', loadLocale: async locale => locale === 'de' ? de : en });
  document.body.replaceChildren();
  controller.startListening.mockClear();
  controller.close.mockClear();
  controller.invalidate.mockClear();
  controller.chooseClarification.mockClear();
  controller.cancelClarification.mockClear();
});

afterEach(() => { closeVoiceAgent(); localStorage.removeItem('scrumboy_voice_create_v2'); });

describe('floating VoiceFlow agent surface', () => {
  it('explicitly selects Create v2 and can return to the existing agent without a routing model', () => {
    open();
    expect(document.getElementById('voiceAgent')?.dataset.experience).toBe('agent');
    const mode = document.querySelector<HTMLSelectElement>('[data-voice-agent-mode]')!;
    mode.value = 'create-v2'; mode.dispatchEvent(new Event('change'));
    expect(localStorage.getItem('scrumboy_voice_create_v2')).toBe('1');
    expect(document.getElementById('voiceAgent')?.dataset.experience).toBe('create-v2');
    expect(controller.close).toHaveBeenCalled();
    expect(localTextGeneration.generate).not.toHaveBeenCalled();
    const back = document.querySelector<HTMLSelectElement>('[data-voice-agent-mode]')!;
    back.value = 'agent'; back.dispatchEvent(new Event('change'));
    expect(document.getElementById('voiceAgent')?.dataset.experience).toBe('agent');
  });
  it('renders captured transcript as literal text, retaining whitespace', () => {
    open();
    agentView.render!({ phase: 'confirmation', status: { kind: 'literal', text: 'Review' }, activity: 'idle', activityStatus: null,
      capturedTranscript: '  Create <img src=x>\nplease  ', confirmation: null, clarification: null });
    const text = document.querySelector<HTMLElement>('[data-voice-agent-transcript]')!;
    expect(text.hidden).toBe(false); expect(text.textContent).toBe('  Create <img src=x>\nplease  '); expect(text.querySelector('img')).toBeNull();
  });
  it('hydrates Keep Listening and follows locale changes', async () => {
    controller.getView.mockReturnValue({ phase: 'ready', status: { key: 'voice.agent.ready', fallback: 'Ready' }, activity: 'idle', activityStatus: null, confirmation: null, clarification: null });
    open();
    expect(document.querySelector('[data-i18n-text="voice.keepListening"]')?.textContent).toBe('Keep Listening');
    await setLocale('de');
    expect(document.querySelector('[data-i18n-text="voice.keepListening"]')?.textContent).toBe('Weiter zuhören');
  });
  it('is a non-modal section and does not make ordinary application controls inert', () => {
    const appButton = document.createElement('button');
    const clicked = vi.fn();
    appButton.addEventListener('click', clicked);
    document.body.appendChild(appButton);

    open();

    const surface = document.getElementById('voiceAgent')!;
    expect(surface.tagName).toBe('SECTION');
    expect(surface.closest('dialog')).toBeNull();
    expect(document.querySelector('#voiceAgent dialog')).toBeNull();
    expect(appButton.inert).toBe(false);
    appButton.click();
    expect(clicked).toHaveBeenCalledOnce();
    expect(controller.startListening).toHaveBeenCalledOnce();
  });

  it('stays in the interactive tree when an existing app dialog opens', async () => {
    open();
    const appDialog = document.createElement('dialog');
    document.body.appendChild(appDialog);
    appDialog.setAttribute('open', '');

    await new Promise((resolve) => globalThis.setTimeout(resolve, 0));

    expect(document.getElementById('voiceAgent')?.parentElement).toBe(appDialog);
    expect(document.getElementById('voiceAgent')?.tagName).toBe('SECTION');
  });

  it('invalidates listening ownership on background without closing the session', () => {
    open();

    window.dispatchEvent(new Event('scrumboy:native-background'));

    expect(controller.invalidate).toHaveBeenCalledOnce();
    expect(controller.close).not.toHaveBeenCalled();
  });

  it('renders authoritative clarification choices as clickable non-modal controls', () => {
    open();
    agentView.render?.({
      phase: 'question',
      status: { key: 'voice.prompt.whichOne', fallback: 'Which one?' },
      activity: 'listening',
      activityStatus: { key: 'voice.agent.listening', fallback: 'Listening…' },
      confirmation: null,
      clarification: {
        options: [
          { id: 'todo:351', label: '#351 · Bogus · Backlog' },
          { id: 'todo:352', label: '#352 · Bogus · In Progress' },
        ],
      },
    });

    const choices = Array.from(document.querySelectorAll<HTMLButtonElement>('[data-voice-agent-choices] button'));
    expect(choices.map((choice) => choice.textContent)).toEqual([
      '#351 · Bogus · Backlog',
      '#352 · Bogus · In Progress',
    ]);
    expect(document.querySelector('[data-voice-agent-clarification]')?.hasAttribute('hidden')).toBe(false);
    expect(document.querySelector('[data-voice-agent-activity]')?.textContent).toBe('Listening…');
    expect(document.getElementById('voiceAgent')?.dataset.activity).toBe('listening');
    choices[1].click();
    expect(controller.chooseClarification).toHaveBeenCalledWith(1);

    document.querySelector<HTMLButtonElement>('[data-voice-agent-cancel-clarification]')!.click();
    expect(controller.cancelClarification).toHaveBeenCalledOnce();

    agentView.render?.({
      phase: 'question',
      status: { key: 'voice.prompt.whichOne', fallback: 'Which one?' },
      activity: 'idle',
      activityStatus: { key: 'voice.agent.noSpeech', fallback: "I didn't hear a response." },
      confirmation: null,
      clarification: {
        options: [
          { id: 'todo:351', label: '#351 · Bogus · Backlog' },
          { id: 'todo:352', label: '#352 · Bogus · In Progress' },
        ],
      },
    });
    expect(document.querySelector('[data-voice-agent-clarification]')?.hasAttribute('hidden')).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-voice-agent-listen]')?.hidden).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-voice-agent-listen]')?.disabled).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-voice-agent-stop]')?.hidden).toBe(true);
  });
});
