import {
  getVoiceFlowContinueConversationPreference,
  setVoiceFlowContinueConversationPreference,
} from '../core/voiceflow-preferences.js';
import { NATIVE_BACKGROUND_EVENT } from '../core/realtime.js';
import { getLocale } from '../i18n/index.js';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';
import type { SpeechOutputCapability } from '../platform/speech-output.js';
import {
  createVoiceAgentController,
  type VoiceAgentController,
  type VoiceAgentMessage,
  type VoiceAgentView,
} from './agent-controller.js';
import type { VoiceCommandOptions } from './command-resolution.js';
import { renderVoiceMessage } from './i18n.js';
import { createLocalAiVoiceCommandInterpreter } from './local-ai-interpreter.js';

const CHANGE_SERVER_EVENT = 'scrumboy:mobile-change-server';

export type OpenVoiceAgentOptions = VoiceCommandOptions & Readonly<{
  localTextGeneration: LocalTextGenerationCapability;
  speechInput: SpeechInputCapability;
  speechOutput: SpeechOutputCapability | null;
  onUseBasic(): void;
}>;

export type OpenVoiceAgentNotReadyOptions = Readonly<{
  status: string;
  onUseBasic(): void;
}>;

type ActiveAgent = Readonly<{
  root: HTMLElement;
  controller: VoiceAgentController | null;
  close(): void;
}>;

let activeAgent: ActiveAgent | null = null;

function renderMessage(message: VoiceAgentMessage): string {
  return 'kind' in message ? message.text : renderVoiceMessage(message);
}

function createSurface(): HTMLElement {
  const root = document.createElement('section');
  root.id = 'voiceAgent';
  root.className = 'voice-agent';
  root.dataset.state = 'ready';
  root.setAttribute('aria-label', 'AI VoiceFlow');
  root.innerHTML = `
    <div class="voice-agent__topline">
      <div class="voice-agent__identity">
        <span class="voice-agent__wave" aria-hidden="true">
          <span></span><span></span><span></span><span></span><span></span>
        </span>
        <span class="voice-agent__name">AI <span data-i18n-text="voice.title" data-i18n-fallback="VoiceFlow">VoiceFlow</span></span>
      </div>
      <button type="button" class="voice-agent__icon-button" data-voice-agent-close aria-label="Close" data-i18n-aria-label="common.close" data-i18n-fallback-aria-label="Close">&times;</button>
    </div>
    <div class="voice-agent__status" data-voice-agent-status role="status" aria-live="polite" aria-atomic="true"></div>
    <div class="voice-agent__activity" data-voice-agent-activity role="status" aria-live="polite" aria-atomic="true" hidden></div>
    <div class="voice-agent__clarification" data-voice-agent-clarification hidden>
      <div class="voice-command__candidate-list" data-voice-agent-choices></div>
      <button type="button" class="btn btn--ghost" data-voice-agent-cancel-clarification data-i18n-text="common.cancel" data-i18n-fallback="Cancel">Cancel</button>
    </div>
    <div class="voice-agent__confirmation" data-voice-agent-confirmation hidden>
      <div class="voice-agent__summary" data-voice-agent-summary></div>
      <div class="voice-agent__confirmation-actions">
        <button type="button" class="btn btn--ghost" data-voice-agent-cancel data-i18n-text="common.cancel" data-i18n-fallback="Cancel">Cancel</button>
        <button type="button" class="btn" data-voice-agent-confirm></button>
      </div>
    </div>
    <div class="voice-agent__controls">
      <button type="button" class="btn" data-voice-agent-listen data-i18n-text="voice.action.listen" data-i18n-fallback="Listen">Listen</button>
      <button type="button" class="btn btn--ghost" data-voice-agent-stop data-i18n-text="voice.action.stop" data-i18n-fallback="Stop" hidden>Stop</button>
      <button type="button" class="btn btn--ghost" data-voice-agent-basic data-i18n-text="voice.ai.useBasic" data-i18n-fallback="Use basic commands">Use basic commands</button>
    </div>
    <label class="voice-command__switch voice-agent__continue">
      <input type="checkbox" role="switch" data-voice-agent-continue />
      <span class="voice-command__switch-track" aria-hidden="true"><span class="voice-command__switch-thumb"></span></span>
      <span data-i18n-text="voice.continueConversation" data-i18n-fallback="Continue conversation">Continue conversation</span>
    </label>
  `;
  return root;
}

function removeCurrentAgent(): void {
  activeAgent?.close();
  activeAgent = null;
}

export function closeVoiceAgent(): void {
  removeCurrentAgent();
}

export function openVoiceAgent(options: OpenVoiceAgentOptions): void {
  const existing = activeAgent?.controller;
  if (existing?.matchesContext(options)) {
    void existing.startListening();
    return;
  }
  removeCurrentAgent();

  const root = createSurface();
  document.body.appendChild(root);
  const status = root.querySelector<HTMLElement>('[data-voice-agent-status]')!;
  const activity = root.querySelector<HTMLElement>('[data-voice-agent-activity]')!;
  const clarification = root.querySelector<HTMLElement>('[data-voice-agent-clarification]')!;
  const choices = root.querySelector<HTMLElement>('[data-voice-agent-choices]')!;
  const confirmation = root.querySelector<HTMLElement>('[data-voice-agent-confirmation]')!;
  const summary = root.querySelector<HTMLElement>('[data-voice-agent-summary]')!;
  const listen = root.querySelector<HTMLButtonElement>('[data-voice-agent-listen]')!;
  const stop = root.querySelector<HTMLButtonElement>('[data-voice-agent-stop]')!;
  const close = root.querySelector<HTMLButtonElement>('[data-voice-agent-close]')!;
  const basic = root.querySelector<HTMLButtonElement>('[data-voice-agent-basic]')!;
  const confirm = root.querySelector<HTMLButtonElement>('[data-voice-agent-confirm]')!;
  const cancel = root.querySelector<HTMLButtonElement>('[data-voice-agent-cancel]')!;
  const cancelClarification = root.querySelector<HTMLButtonElement>('[data-voice-agent-cancel-clarification]')!;
  const continuation = root.querySelector<HTMLInputElement>('[data-voice-agent-continue]')!;
  continuation.checked = getVoiceFlowContinueConversationPreference();

  const render = (view: VoiceAgentView) => {
    root.dataset.state = view.phase;
    root.dataset.activity = view.activity;
    status.textContent = renderMessage(view.status);
    activity.hidden = view.activityStatus === null;
    activity.textContent = view.activityStatus ? renderMessage(view.activityStatus) : '';
    const isAcquiring = view.activity === 'starting-microphone' || view.activity === 'listening';
    const isProcessing = view.activity === 'processing';
    listen.hidden = isAcquiring;
    listen.disabled = isProcessing || view.phase === 'closed';
    stop.hidden = !isAcquiring;
    clarification.hidden = view.clarification === null;
    choices.replaceChildren();
    view.clarification?.options.forEach((option, index) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'voice-command__candidate';
      button.dataset.index = String(index);
      button.textContent = option.label;
      choices.appendChild(button);
    });
    confirmation.hidden = view.confirmation === null;
    if (view.confirmation) {
      summary.textContent = view.confirmation.summary;
      confirm.textContent = view.confirmation.confirmLabel;
      confirm.classList.toggle('btn--danger', view.confirmation.danger);
    }
  };

  const controller = createVoiceAgentController({
    ...options,
    interpreter: createLocalAiVoiceCommandInterpreter({
      capability: options.localTextGeneration,
      locale: getLocale(),
    }),
    continuationEnabled: continuation.checked,
    speechOutput: options.speechOutput,
    onView: render,
  });

  let disposed = false;
  const syncInteractiveHost = () => {
    const openDialogs = document.querySelectorAll<HTMLDialogElement>('dialog[open]');
    const host = openDialogs.item(openDialogs.length - 1) ?? document.body;
    if (root.parentElement !== host) host.appendChild(root);
  };
  const dialogObserver = new MutationObserver(syncInteractiveHost);
  dialogObserver.observe(document.body, {
    attributes: true,
    attributeFilter: ['open'],
    childList: true,
    subtree: true,
  });
  const invalidateForBackground = () => controller.invalidate();
  const onVisibilityChange = () => {
    if (document.visibilityState === 'hidden') invalidateForBackground();
  };
  const onServerChange = () => closeAgent();
  const closeAgent = () => {
    if (disposed) return;
    disposed = true;
    document.removeEventListener('visibilitychange', onVisibilityChange);
    window.removeEventListener(NATIVE_BACKGROUND_EVENT, invalidateForBackground);
    window.removeEventListener(CHANGE_SERVER_EVENT, onServerChange);
    dialogObserver.disconnect();
    controller.close();
    root.remove();
    if (activeAgent?.root === root) activeAgent = null;
  };

  listen.addEventListener('click', () => void controller.startListening());
  stop.addEventListener('click', () => controller.stopListening());
  close.addEventListener('click', closeAgent);
  basic.addEventListener('click', () => {
    closeAgent();
    options.onUseBasic();
  });
  confirm.addEventListener('click', () => void controller.confirm());
  cancel.addEventListener('click', () => controller.cancelConfirmation());
  clarification.addEventListener('click', (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>('[data-index]');
    if (!button) return;
    const index = Number(button.dataset.index);
    if (Number.isInteger(index)) void controller.chooseClarification(index);
  });
  cancelClarification.addEventListener('click', () => controller.cancelClarification());
  continuation.addEventListener('change', () => {
    const enabled = continuation.checked;
    controller.setContinuationEnabled(enabled);
    void setVoiceFlowContinueConversationPreference(enabled);
  });
  document.addEventListener('visibilitychange', onVisibilityChange);
  window.addEventListener(NATIVE_BACKGROUND_EVENT, invalidateForBackground);
  window.addEventListener(CHANGE_SERVER_EVENT, onServerChange);

  activeAgent = Object.freeze({ root, controller, close: closeAgent });
  syncInteractiveHost();
  void controller.startListening();
}

export function openVoiceAgentNotReady(options: OpenVoiceAgentNotReadyOptions): void {
  removeCurrentAgent();
  const root = createSurface();
  root.dataset.state = 'error';
  root.querySelector<HTMLElement>('[data-voice-agent-status]')!.textContent = options.status;
  root.querySelector<HTMLButtonElement>('[data-voice-agent-listen]')!.hidden = true;
  root.querySelector<HTMLElement>('.voice-agent__continue')!.hidden = true;
  root.querySelector<HTMLButtonElement>('[data-voice-agent-stop]')!.hidden = true;
  root.querySelector<HTMLButtonElement>('[data-voice-agent-close]')!.addEventListener('click', removeCurrentAgent);
  root.querySelector<HTMLButtonElement>('[data-voice-agent-basic]')!.addEventListener('click', () => {
    removeCurrentAgent();
    options.onUseBasic();
  });
  document.body.appendChild(root);
  const close = () => root.remove();
  activeAgent = Object.freeze({ root, controller: null, close });
}
