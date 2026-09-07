import { voiceFlowDiagnostic } from '../platform/voiceflow-diagnostics.js';
import { voiceText } from './i18n.js';
import { AGENT_LIMITS, AgentProtocolError, parseAgentEnvelope, type AgentEnvelope, type SkillCall } from './agent-protocol.js';
import { VoiceAgentResourceHandles } from './agent-resources.js';
import { VoiceAgentProposalStore, type BatchExecution } from './agent-proposals.js';
import { renderAgentSkillResult, type AgentSession, type AgentSkillContext, type AgentSkillResult, type VoiceAgentSkillRegistry } from './agent-skills.js';
import type { VoiceAgentModel } from './agent-model.js';

type TraceEntry = { user: string } | { agent: AgentEnvelope } | { skillResult: { skill: SkillCall['skill']; result: AgentSkillResult } };
export type AgentTask = AgentSkillContext & {
  goal: string; trace: TraceEntry[]; proposals: VoiceAgentProposalStore; confirmation: boolean;
  modelSteps: number; skillCalls: number; results: AgentSkillResult[];
};
export type AgentLoopView = { phase: 'question' | 'confirmation' | 'success' | 'error'; text: string; choices?: { id: string; label: string }[]; danger?: boolean };
export const agentSafeFailure = () => voiceText('voice.agent.safeFailure', 'I could not finish that safely. Please try again.');
function batchText(result: BatchExecution): string {
  if (!result.failed && !result.refreshFailed) return `${voiceText('voice.status.done', 'Done.')} ${result.succeeded.join('; ')}`;
  return voiceText('voice.agent.batchResult', 'Succeeded: {succeeded}. Failed or unconfirmed: {failed}. Not attempted: {remaining}. Refresh failed: {refresh}.', {
    succeeded: result.succeeded.join('; ') || '—', failed: result.failed || '—', remaining: result.unattempted.join('; ') || '—', refresh: result.refreshFailed ? '✓' : '—',
  });
}
export class VoiceAgentLoop {
  private task: AgentTask | null = null;
  readonly session: AgentSession;
  constructor(private readonly model: VoiceAgentModel, readonly registry: VoiceAgentSkillRegistry, keepListening: boolean) {
    this.session = { activeTodo: null, keepListening };
  }
  get pending(): boolean { return !!this.task; }
  get confirmationPending(): boolean { return !!this.task?.confirmation; }
  private append(task: AgentTask, value: TraceEntry): void {
    task.trace.push(value);
    if (task.trace.length > AGENT_LIMITS.trace) task.trace.shift();
  }
  invalidate(): void {
    this.task?.handles.clear(); this.task?.proposals.clear();
    if (this.task) { this.task.goal = ''; this.task.confirmation = false; this.task.trace.length = 0; this.task.results.length = 0; this.task.pendingChoice = null; }
    this.task = null; this.session.activeTodo = null;
  }
  setKeepListening(enabled: boolean): void {
    this.session.keepListening = enabled;
    if (!enabled && !this.task) this.session.activeTodo = null;
  }
  private finish(task: AgentTask, mutations: number): void {
    voiceFlowDiagnostic('VoiceAgent task complete', { modelSteps: task.modelSteps, skillCalls: task.skillCalls, mutationsExecuted: mutations });
    task.handles.clear(); task.proposals.clear(); task.goal = ''; task.confirmation = false; task.trace.length = 0; task.results.length = 0; task.pendingChoice = null;
    this.task = null;
    if (!this.session.keepListening) this.session.activeTodo = null;
  }
  cancel(): AgentLoopView {
    if (this.task) this.finish(this.task, 0);
    return { phase: 'success', text: voiceText('voice.status.cancelled', 'Cancelled.') };
  }
  async confirm(signal: AbortSignal): Promise<AgentLoopView> {
    const task = this.task;
    if (!task?.confirmation) return { phase: 'error', text: agentSafeFailure() };
    task.confirmation = false;
    try {
      const result = await task.proposals.confirm(this.registry, task, signal);
      const view: AgentLoopView = { phase: result.failed || result.refreshFailed ? 'error' : 'success', text: batchText(result) };
      this.finish(task, result.succeeded.length);
      return view;
    } catch { this.invalidate(); return { phase: 'error', text: agentSafeFailure() }; }
  }
  async choose(index: number, signal: AbortSignal): Promise<AgentLoopView> {
    const choice = this.task?.pendingChoice?.result.choices[index];
    if (!choice) return { phase: 'error', text: agentSafeFailure() };
    return this.submit(`I select the offered option ${choice.handle}: ${choice.label}`, signal);
  }
  async submit(utterance: string, signal: AbortSignal): Promise<AgentLoopView> {
    try {
      this.registry.context(signal);
      if (!utterance.trim() || utterance.length > AGENT_LIMITS.utterance) throw new AgentProtocolError('Utterance limit');
      if (!this.task) {
        this.task = { goal: utterance, trace: [], handles: new VoiceAgentResourceHandles(), session: this.session, proposals: new VoiceAgentProposalStore(), pendingChoice: null, choiceAnswered: false, confirmation: false, modelSteps: 0, skillCalls: 0, results: [] };
        voiceFlowDiagnostic('VoiceAgent task start');
      } else if (this.task.pendingChoice) this.task.choiceAnswered = true;
      const task = this.task;
      this.append(task, { user: utterance });
      while (true) {
        let envelope: AgentEnvelope | undefined;
        let repair: string | undefined;
        for (let attempt = 0; attempt < 2; attempt++) {
          if (task.modelSteps >= AGENT_LIMITS.modelSteps || task.skillCalls >= AGENT_LIMITS.skillCalls) throw new AgentProtocolError('Task limit');
          task.modelSteps++;
          const input = JSON.stringify({ goal: task.goal, activeTodoAvailable: !!this.session.activeTodo, trace: task.trace,
            pending: task.confirmation ? { kind: 'confirmation', proposalCount: task.proposals.count } : task.pendingChoice ? { kind: 'choice', ...task.pendingChoice.result } : null,
            ...(repair ? { repair: `Previous response violated protocol: ${repair}. Return one valid envelope.` } : {}),
          });
          const raw = await this.model(input, signal);
          this.registry.context(signal);
          if (this.task !== task) throw new AgentProtocolError('Task expired');
          try { envelope = parseAgentEnvelope(raw, task.confirmation); break; }
          catch (error) { if (!(error instanceof AgentProtocolError) || attempt === 1) throw error; repair = error.message; }
        }
        if (!envelope) throw new AgentProtocolError('Missing envelope');
        voiceFlowDiagnostic('VoiceAgent model step', { step: task.modelSteps, kind: envelope.kind, ...(envelope.kind === 'skill_call' ? { skill: envelope.skill } : {}) });
        this.append(task, { agent: envelope });
        if (envelope.kind === 'confirm') return this.confirm(signal);
        if (envelope.kind === 'decline' || envelope.kind === 'cancel') return this.cancel();
        if (envelope.kind === 'ask_user') {
          voiceFlowDiagnostic('VoiceAgent ask');
          const pending = task.pendingChoice?.result;
          return { phase: 'question', text: pending ? renderAgentSkillResult(pending) : envelope.text,
            ...(pending ? { choices: pending.choices.map(choice => ({ id: choice.handle, label: `${choice.number ? `#${choice.number} · ` : ''}${choice.label}${choice.lane ? ` · ${choice.lane}` : ''}` })) } : {}) };
        }
        if (envelope.kind === 'finish') {
          if (task.pendingChoice) throw new AgentProtocolError('Choice unresolved');
          if (task.proposals.count) {
            await task.proposals.preflight(this.registry, task, signal);
            task.confirmation = true;
            voiceFlowDiagnostic('VoiceAgent confirmation pending', { proposalCount: task.proposals.count });
            return { phase: 'confirmation', text: `${task.proposals.summaries().join('; ')}?`, danger: task.proposals.danger };
          }
          const text = task.results.filter(result => result.status !== 'choices').map(renderAgentSkillResult).join('; ') || voiceText('voice.agent.noChanges', 'No changes needed.');
          this.finish(task, 0);
          return { phase: 'success', text };
        }
        if (envelope.kind === 'skill_call') {
          task.skillCalls++;
          // A correction/addition invalidates the old confirmation before any new work.
          task.confirmation = false;
          const outcome = await this.registry.run(envelope, task, signal);
          this.registry.context(signal);
          if (this.task !== task) throw new AgentProtocolError('Task expired');
          if (outcome.prepared) {
            const ref = task.proposals.add(outcome.prepared);
            if (outcome.result.status === 'prepared') outcome.result.proposalRef = ref;
          }
          if (JSON.stringify(outcome.result).length > AGENT_LIMITS.resultText) throw new AgentProtocolError('Result limit');
          if (['stale', 'denied', 'invalid', 'not_found'].includes(outcome.result.status)) {
            const text = renderAgentSkillResult(outcome.result);
            this.finish(task, 0);
            return { phase: 'error', text };
          }
          this.append(task, { skillResult: { skill: envelope.skill, result: outcome.result } });
          task.results.push(outcome.result);
          voiceFlowDiagnostic('VoiceAgent skill result', { skill: envelope.skill, status: outcome.result.status, choiceCount: outcome.result.status === 'choices' ? outcome.result.choices.length : 0, proposalCount: task.proposals.count });
        }
      }
    } catch { this.invalidate(); return { phase: 'error', text: agentSafeFailure() }; }
  }
}
