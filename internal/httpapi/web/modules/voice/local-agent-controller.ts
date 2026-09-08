import type { VoiceAgentMessage, VoiceAgentView } from './agent-controller.js';
export type { VoiceAgentMessage, VoiceAgentView } from './agent-controller.js';
import type { VoiceCommandOptions } from './command-resolution.js';
import type { SpeechInputCapability } from '../platform/speech-input.js';
import { SPEECH_INPUT_MAX_DURATION_MS, SpeechInputError } from '../platform/speech-input.js';
import type { SpeechOutputCapability } from '../platform/speech-output.js';
import { SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS } from '../platform/speech-output.js';
import { voiceText } from './i18n.js';
import { VoiceAgentLoop, agentSafeFailure, type AgentLoopView } from './agent-loop.js';
import { VoiceAgentSkillRegistry } from './agent-skills.js';
import type { VoiceAgentModel } from './agent-model.js';

type ControllerOptions = VoiceCommandOptions & {
  model: VoiceAgentModel; speechInput: SpeechInputCapability; speechOutput?: SpeechOutputCapability | null;
  continuationEnabled: boolean; onView(view: VoiceAgentView): void;
  loop?: VoiceAgentLoop;
};
const literal = (text: string): VoiceAgentMessage => ({ kind: 'literal', text });
/** Owns only UI, microphone/TTS sequencing, cancellation and lifecycle. */
export function createVoiceAgentController(options: ControllerOptions) {
  const loop = options.loop ?? new VoiceAgentLoop(options.model, new VoiceAgentSkillRegistry(options), options.continuationEnabled);
  let view: VoiceAgentView = { phase: 'ready', status: { key: 'voice.agent.ready', fallback: 'Ready' }, activity: 'idle', activityStatus: null, confirmation: null, clarification: null };
  let operation: AbortController | null = null;
  let closed = false;
  let voice = false;
  let keepListening = options.continuationEnabled;
  const emit = (patch: Partial<VoiceAgentView>) => { view = Object.freeze({ ...view, ...patch }); options.onView(view); };
  const owns = (owner: AbortController) => !closed && operation === owner && !owner.signal.aborted;
  const contextCurrent = () => {
    try { loop.registry.context(new AbortController().signal); return true; } catch { return false; }
  };
  const abort = () => { operation?.abort(); operation = null; };
  const speak = async (text: string | null, owner: AbortController): Promise<boolean> => {
    if (!text?.trim() || text.length > SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS || !options.speechOutput || !owns(owner)) return false;
    try {
      const status = await options.speechOutput.status({ signal: owner.signal });
      if (!owns(owner) || status.state !== 'ready') return false;
      emit({ activity: 'speaking', activityStatus: null });
      await options.speechOutput.speak({ text, language: 'en-US', signal: owner.signal });
      if (!owns(owner)) return false;
      emit({ activity: 'idle', activityStatus: null });
      return true;
    } catch { if (owns(owner)) emit({ activity: 'idle', activityStatus: null }); return false; }
  };
  const show = async (result: AgentLoopView, owner: AbortController) => {
    if (!owns(owner)) return;
    emit({ phase: result.phase, status: literal(result.text), activity: 'idle', activityStatus: null,
      confirmation: result.phase === 'confirmation' ? { summary: result.text, confirmLabel: voiceText('common.confirm', 'Confirm'), danger: !!result.danger } : null,
      clarification: result.phase === 'question' ? { options: result.choices ?? [] } : null });
    // Confirmation speech must cover the whole batch. Null leaves visual/tap/manual Listen available.
    const speechText = result.phase === 'confirmation' ? result.speechText : result.text.slice(0, SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS);
    const spoken = await speak(speechText, owner);
    if (!owns(owner)) return;
    const terminal = result.phase === 'success' || result.phase === 'error';
    // Each result schedules at most one bounded window. Clarification/confirmation do not depend on the toggle.
    if (voice && spoken && (!terminal || keepListening)) await listen(owner, true);
    else if (terminal) voice = false;
  };
  const interpret = async (text: string, owner: AbortController) => {
    emit({ activity: 'processing', activityStatus: { key: 'voice.agent.processing', fallback: 'Processing…' } });
    const result = await loop.submit(text, owner.signal);
    if (owns(owner)) await show(result, owner);
  };
  const listen = async (owner: AbortController, automatic: boolean) => {
    if (!owns(owner)) return;
    loop.trace();
    try {
      if (automatic) {
        const status = await options.speechInput.status({ signal: owner.signal });
        if (!owns(owner)) return;
        if (status.state !== 'ready') throw new SpeechInputError('not_ready');
      }
      emit({ activity: 'starting-microphone', activityStatus: { key: 'voice.agent.startingMicrophone', fallback: 'Starting microphone…' } });
      const result = await options.speechInput.listen({ maxDurationMs: SPEECH_INPUT_MAX_DURATION_MS, language: globalThis.navigator?.language || 'en-US', signal: owner.signal,
        onListening: () => { if (owns(owner)) emit({ activity: 'listening', activityStatus: { key: 'voice.agent.listening', fallback: 'Listening…' } }); } });
      if (owns(owner)) {
        loop.trace().emit('asr_final', { modality: 'voice', transcript: result.transcript.trim(), transcriptLength: result.transcript.trim().length, provider: result.provider });
        await interpret(result.transcript, owner);
      }
    } catch (error) {
      if (!owns(owner)) return;
      const code = error instanceof SpeechInputError ? error.code : 'recognition_failed';
      loop.trace().emit(code === 'cancelled' ? 'cancel' : 'failure', { source: 'speech', code, interactionRetained: loop.pending });
      if (!loop.pending) loop.endTrace(code === 'cancelled' ? 'speech_cancelled' : 'speech_failure');
      const status: VoiceAgentMessage = code === 'permission_denied' ? { key: 'voice.agent.permissionDenied', fallback: 'Microphone permission was denied.' }
        : code === 'permission_denied_permanently' ? { key: 'voice.agent.permissionBlocked', fallback: 'Microphone permission is blocked. Enable it in system settings.' }
        : code === 'no_speech' || code === 'timeout' ? { key: 'voice.agent.noSpeech', fallback: "I didn't hear a response." }
        : code === 'cancelled' ? { key: 'voice.agent.stopped', fallback: 'Listening stopped.' }
        : { key: 'voice.agent.speechFailed', fallback: 'Speech recognition failed. Try again.' };
      emit({ activity: 'idle', activityStatus: status });
    }
  };
  const run = async (work: (owner: AbortController) => Promise<void>) => {
    if (closed || operation) return;
    if (!contextCurrent()) { loop.endTrace('stale_context'); loop.invalidate(); emit({ phase: 'error', status: literal(agentSafeFailure()), confirmation: null, clarification: null }); return; }
    const owner = new AbortController(); operation = owner;
    try { await work(owner); }
    catch { if (owns(owner)) { loop.invalidate(); emit({ phase: 'error', status: literal(agentSafeFailure()), activity: 'idle', activityStatus: null, confirmation: null, clarification: null }); } }
    finally { if (operation === owner) operation = null; }
  };
  const interruptAcquisition = async () => {
    if (view.activity === 'speaking') { loop.cancelTrace('superseded', loop.pending); abort(); await options.speechOutput?.stop().catch(() => undefined); }
    else if (view.activity === 'starting-microphone' || view.activity === 'listening') { loop.cancelTrace('superseded', loop.pending); abort(); }
  };
  const controller = {
    getView: () => view,
    matchesContext(context: Pick<VoiceCommandOptions, 'initialUserId' | 'initialProjectId' | 'initialProjectSlug'>) {
      return context.initialUserId === options.initialUserId && context.initialProjectId === options.initialProjectId && context.initialProjectSlug === options.initialProjectSlug;
    },
    async startListening() { if (closed) return; await interruptAcquisition(); voice = true; await run(owner => listen(owner, false)); },
    async submitTranscript(text: string) { if (closed) return; await interruptAcquisition(); voice = false; await run(owner => { loop.trace().emit('transcript_input', { modality: 'typed', transcript: text.trim(), transcriptLength: text.trim().length }); return interpret(text, owner); }); },
    stopListening() { if (closed) return; loop.cancelTrace('microphone_stopped', loop.pending); abort(); emit({ activity: 'idle', activityStatus: { key: 'voice.agent.stopped', fallback: 'Listening stopped.' } }); },
    async confirm() { if (!loop.confirmationPending || closed) return; await interruptAcquisition(); await run(async owner => { emit({ activity: 'processing', activityStatus: null }); await show(await loop.confirm(owner.signal), owner); }); },
    cancelConfirmation() { if (closed || operation && view.activity === 'processing') return; void (async () => { await interruptAcquisition(); await run(owner => show(loop.cancel(), owner)); })(); },
    async chooseClarification(index: number) { if (closed || !Number.isInteger(index) || index < 0) return; await interruptAcquisition(); await run(async owner => { emit({ activity: 'processing', activityStatus: null }); await show(await loop.choose(index, owner.signal), owner); }); },
    cancelClarification() { controller.cancelConfirmation(); },
    setContinuationEnabled(enabled: boolean) { keepListening = !!enabled; loop.setKeepListening(keepListening); },
    invalidate(_options: { clearConversation?: boolean } = {}) {
      if (closed) return; loop.cancelTrace('controller_invalidated'); abort(); voice = false; loop.invalidate();
      void options.speechOutput?.invalidate().catch(() => undefined);
      emit({ phase: 'ready', status: { key: 'voice.agent.ready', fallback: 'Ready' }, activity: 'idle', activityStatus: null, confirmation: null, clarification: null });
    },
    close() { if (closed) return; loop.cancelTrace('controller_closed'); controller.invalidate(); closed = true; globalThis.clearInterval(contextMonitor); emit({ phase: 'closed', status: { key: 'voice.agent.closed', fallback: 'VoiceFlow closed.' } }); },
  };
  // Pending tasks and retained references must expire even while the UI is waiting for a typed reply.
  const contextMonitor = globalThis.setInterval(() => {
    if (closed || (!operation && !loop.pending && !loop.session.activeTodo) || contextCurrent()) return;
    loop.endTrace('stale_context');
    controller.invalidate();
    emit({ phase: 'error', status: { key: 'voice.errors.staleContext', fallback: 'The board changed before the command could run.' } });
  }, 100);
  options.onView(view);
  return Object.freeze(controller);
}
export type VoiceAgentController = ReturnType<typeof createVoiceAgentController>;
