// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';

const controller = vi.hoisted(() => ({
  getView: vi.fn(),
  getConversationState: vi.fn(),
  matchesContext: vi.fn().mockReturnValue(false),
  startListening: vi.fn().mockResolvedValue(undefined),
  submitTranscript: vi.fn(),
  stopListening: vi.fn(),
  confirm: vi.fn(),
  cancelConfirmation: vi.fn(),
  setContinuationEnabled: vi.fn(),
  invalidate: vi.fn(),
  close: vi.fn(),
}));

vi.mock('./agent-controller.js', () => ({
  createVoiceAgentController: vi.fn((options) => {
    options.onView({
      phase: 'ready',
      status: { key: 'voice.status.ready', fallback: 'Ready' },
      confirmation: null,
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

beforeEach(() => {
  document.body.replaceChildren();
  controller.startListening.mockClear();
  controller.close.mockClear();
  controller.invalidate.mockClear();
});

afterEach(() => closeVoiceAgent());

describe('floating VoiceFlow agent surface', () => {
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
});
