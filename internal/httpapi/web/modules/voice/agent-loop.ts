import { createVoiceFlowTrace } from './trace.js';
import { voiceText } from './i18n.js';
import { AGENT_LIMITS, AgentProtocolError, agentRepairInstruction, completeSkillClarification, interpretAgentEnvelope, type AgentEnvelope, type AgentState, type AgentStateKind, type SkillCall, type SkillClarification } from './agent-protocol.js';
import { VoiceAgentResourceHandles } from './agent-resources.js';
import { VoiceAgentProposalStore, type BatchExecution } from './agent-proposals.js';
import { renderAgentSkillResult, type AgentSession, type AgentSkillContext, type AgentSkillResult, type VoiceAgentSkillRegistry } from './agent-skills.js';
import type { VoiceAgentModel } from './agent-model.js';
import { extractHighConfidenceMoveCall, isCompleteStandaloneMove, recoverMoveAfterParseFailure, recoverPresentMoveArguments } from './agent-move-extraction.js';
import { normalizeLookup } from './normalize.js';

type TraceEntry = { user: string } | { agent: AgentEnvelope } | { skillResult: { skill: SkillCall['skill']; result: AgentSkillResult } };
export type AgentTask = AgentSkillContext & {
  goal: string; trace: TraceEntry[]; proposals: VoiceAgentProposalStore; confirmation: boolean; clarification: boolean;
  pendingSkillClarification: SkillClarification | null;
  modelSteps: number; skillCalls: number; results: AgentSkillResult[];
};
export type AgentLoopView = { text: string; choices?: { id: string; label: string }[]; danger?: boolean; confirmLabel?: string } & (
  | { phase: 'confirmation'; speechText: string | null }
  | { phase: 'question' | 'success' | 'error' }
);
export const agentSafeFailure = () => voiceText('voice.agent.safeFailure', 'I could not finish that safely. Please try again.');
function batchText(result: BatchExecution): string {
  if (!result.failed && !result.refreshFailed) return `${voiceText('voice.status.done', 'Done.')} ${result.succeeded.join('; ')}`;
  return voiceText('voice.agent.batchResult', 'Succeeded: {succeeded}. Failed or unconfirmed: {failed}. Not attempted: {remaining}. Refresh failed: {refresh}.', {
    succeeded: result.succeeded.join('; ') || '—', failed: result.failed || '—', remaining: result.unattempted.join('; ') || '—', refresh: result.refreshFailed ? '✓' : '—',
  });
}
const choiceFillers = new Set(['the', 'one', 'story', 'todo', 'card', 'task', 'item', 'option', 'please', 'i', 'choose', 'select', 'offered', 'number', 'in']);
const choiceOrdinals = new Map([['first', 0], ['second', 1], ['third', 2], ['fourth', 3], ['fifth', 4]]);
export type AgentSafeFailureStage =
  | 'model_interpretation_failure'
  | 'protocol_repair_exhaustion'
  | 'target_resolution'
  | 'lane_resolution'
  | 'proposal_preparation'
  | 'proposals_ready_completion'
  | 'confirmation_preflight'
  | 'stale_context';
function words(value: string): string[] { return normalizeLookup(value).match(/[\p{L}\p{N}]+/gu) ?? []; }
function authoritativeFailureResource(error: unknown): 'todo' | 'lane' | 'member' | 'tag' | undefined {
  const read = (value: unknown): 'todo' | 'lane' | 'member' | 'tag' | undefined => (
    value === 'todo' || value === 'lane' || value === 'member' || value === 'tag' ? value : undefined
  );
  if (!error || typeof error !== 'object') return undefined;
  if ('resource' in error) {
    const resource = read((error as { resource?: unknown }).resource);
    if (resource) return resource;
  }
  if (error instanceof AgentProtocolError) return read((error.diagnostic as { resource?: unknown }).resource);
  return undefined;
}
function classifySafeFailure(
  stage: string,
  stateKind: AgentStateKind | 'none',
  error: unknown,
  repaired: boolean,
): { safeFailureStage: AgentSafeFailureStage; reason: string } {
  const reason = error instanceof AgentProtocolError ? error.message : `${stage}_exception`;
  if (reason === 'Context invalidated' || reason === 'Stale todo' || reason === 'Stale target'
    || reason === 'Title changed during resolution' || reason === 'Proposal changed; start again'
    || reason === 'Task expired' || reason === 'Task cancelled') {
    return { safeFailureStage: 'stale_context', reason };
  }
  const resource = authoritativeFailureResource(error);
  if (resource === 'lane') return { safeFailureStage: 'lane_resolution', reason };
  if (resource) return { safeFailureStage: 'target_resolution', reason };
  if (stage === 'proposal_preparation' || stage === 'confirmation_preflight' || stage === 'target_resolution') {
    return { safeFailureStage: stage, reason };
  }
  if (stage === 'lane_resolution') return { safeFailureStage: 'target_resolution', reason };
  if (stage === 'interpret') {
    if (stateKind === 'proposals_ready') return { safeFailureStage: 'proposals_ready_completion', reason };
    return { safeFailureStage: repaired ? 'protocol_repair_exhaustion' : 'model_interpretation_failure', reason };
  }
  if (stage === 'resolve') {
    if (/todo|title|target|reference/i.test(reason)) return { safeFailureStage: 'target_resolution', reason };
    return { safeFailureStage: 'proposal_preparation', reason };
  }
  return { safeFailureStage: 'model_interpretation_failure', reason };
}
function isStandaloneNamedOpenGoal(goal: string): boolean {
  const normalized = normalizeLookup(goal);
  if (!/^(?:please )?(?:open|find(?: me)?|search for|look up)\s+\S/.test(normalized)) return false;
  if (/\b(?:all|every|everything|multiple|tagged|assigned to)\b/.test(normalized)) return false;
  // Ambiguous punctuation/connectors bias toward continuing the agent so additional work is never dropped.
  return !/[&,;\n]/.test(goal) && !/\b(?:and|then|also|plus)\b/.test(normalized);
}
/** Resolve only an unambiguous reference to the authoritative choices already on screen. */
function localChoiceCall(task: AgentTask, utterance: string): SkillCall | null {
  const pending = task.pendingChoice;
  if (!pending) return null;
  const choices = pending.result.choices;
  const normalized = normalizeLookup(utterance);
  const exactHandle = choices.findIndex(choice => normalized === normalizeLookup(choice.handle));
  let selectedIndex = exactHandle;
  if (selectedIndex < 0) {
    const constraints: number[] = [];
    const ordinals = [...new Set(words(normalized).map(word => choiceOrdinals.get(word)).filter((index): index is number => index !== undefined && index < choices.length))];
    if (ordinals.length > 1) return null;
    if (ordinals.length === 1) constraints.push(ordinals[0]);

    const numerals = [...new Set([...normalized.matchAll(/(?:^|\s|#)(\d+)(?=$|\s)/g)].map(match => Number(match[1])))];
    for (const numeral of numerals) {
      const numbered = choices.map((choice, index) => choice.number === numeral ? index : -1).filter(index => index >= 0);
      if (numbered.length === 1) constraints.push(numbered[0]);
      else if (!numbered.length && numeral >= 1 && numeral <= choices.length) constraints.push(numeral - 1);
      else return null;
    }

    const terms = words(normalized).filter(word => !choiceFillers.has(word) && !choiceOrdinals.has(word) && !/^\d+$/.test(word));
    if (terms.length) {
      const labeled = choices.map((choice, index) => {
        const candidate = new Set(words(`${choice.label} ${choice.lane ?? ''}`));
        return terms.every(term => candidate.has(term)) ? index : -1;
      }).filter(index => index >= 0);
      if (labeled.length !== 1) return null;
      constraints.push(labeled[0]);
    }
    if (!constraints.length || new Set(constraints).size !== 1) return null;
    selectedIndex = constraints[0];
  }
  const choice = choices[selectedIndex];
  const args = { ...pending.call.arguments } as Record<string, unknown>;
  if (pending.result.resource === 'todo') { delete args.reference; delete args.todoRef; args.todoRef = choice.handle; }
  else args[pending.result.resource] = choice.handle;
  return { ...pending.call, arguments: args } as SkillCall;
}
export class VoiceAgentLoop {
  private task: AgentTask | null = null;
  private diagnostic: ReturnType<typeof createVoiceFlowTrace> | null = null;
  trace() {
    if (!this.diagnostic || this.diagnostic.ended) this.diagnostic = createVoiceFlowTrace();
    return this.diagnostic;
  }
  adoptTrace(trace: ReturnType<typeof createVoiceFlowTrace>): void { this.diagnostic = trace; }
  endTrace(reason: string) { this.diagnostic?.end(reason); }
  cancelTrace(reason: string, retained = false) {
    this.diagnostic?.emit('cancel', { reason, interactionRetained: retained });
    if (!retained) this.diagnostic?.end(reason);
  }
  readonly session: AgentSession;
  constructor(private readonly model: VoiceAgentModel, readonly registry: VoiceAgentSkillRegistry, keepListening: boolean) {
    this.session = { activeTodo: null, keepListening };
  }
  get pending(): boolean { return !!this.task; }
  get confirmationPending(): boolean { return !!this.task?.confirmation; }
  get currentState(): AgentState | null { return this.task ? this.state(this.task) : null; }
  /** Canonical state: the model input, envelope legality and repair guidance are all derived from it. */
  private state(task: AgentTask): AgentState {
    if (task.confirmation) return { kind: 'confirmation', proposalCount: task.proposals.count };
    if (task.pendingChoice) return { kind: 'choice' };
    if (task.pendingSkillClarification) {
      const { skill, arguments: args, missing } = task.pendingSkillClarification;
      return { kind: 'clarification', skillClarification: { skill, arguments: args, missing } };
    }
    if (task.clarification) return { kind: 'clarification' };
    if (task.proposals.count) return { kind: 'proposals_ready', proposalCount: task.proposals.count };
    return { kind: 'idle' };
  }
  private append(task: AgentTask, value: TraceEntry): void {
    task.trace.push(value);
    if (task.trace.length > AGENT_LIMITS.trace) task.trace.shift();
  }
  invalidate(): void {
    this.diagnostic?.end('controller_invalidated');
    this.task?.handles.clear(); this.task?.proposals.clear();
    if (this.task) { this.task.goal = ''; this.task.confirmation = false; this.task.clarification = false; this.task.trace.length = 0; this.task.results.length = 0; this.task.pendingChoice = null; this.task.pendingSkillClarification = null; }
    this.task = null; this.session.activeTodo = null;
  }
  setKeepListening(enabled: boolean): void {
    this.session.keepListening = enabled;
    if (!enabled && !this.task) this.session.activeTodo = null;
  }
  private finish(task: AgentTask, mutations: number): void {
    task.diagnostic?.end('success', { modelSteps: task.modelSteps, skillCalls: task.skillCalls, mutationsExecuted: mutations });
    task.handles.clear(); task.proposals.clear(); task.goal = ''; task.confirmation = false; task.clarification = false; task.trace.length = 0; task.results.length = 0; task.pendingChoice = null; task.pendingSkillClarification = null;
    this.task = null;
    if (!this.session.keepListening) this.session.activeTodo = null;
  }
  cancel(): AgentLoopView {
    this.cancelTrace('cancelled_by_user');
    if (this.task) this.finish(this.task, 0);
    return { phase: 'success', text: voiceText('voice.status.cancelled', 'Cancelled.') };
  }
  async confirm(signal: AbortSignal): Promise<AgentLoopView> {
    const task = this.task;
    if (!task?.confirmation) return { phase: 'error', text: agentSafeFailure() };
    task.confirmation = false;
    try {
      const result = await task.proposals.confirm(this.registry, task, signal);
      task.diagnostic?.end(result.failed ? 'execution_failure' : result.refreshFailed ? 'refresh_failure' : 'success', { succeededCount: result.succeeded.length, unattemptedCount: result.unattempted.length });
      const view: AgentLoopView = { phase: result.failed || result.refreshFailed ? 'error' : 'success', text: batchText(result) };
      this.finish(task, result.succeeded.length);
      return view;
    } catch (error) { task.diagnostic?.emit('failure', { source: 'confirm_revalidation', reason: error instanceof AgentProtocolError ? error.message : 'revalidation_exception' }); task.diagnostic?.end('confirmation_failed'); this.invalidate(); return { phase: 'error', text: agentSafeFailure() }; }
  }
  async choose(index: number, signal: AbortSignal): Promise<AgentLoopView> {
    const choice = this.task?.pendingChoice?.result.choices[index];
    if (!choice) return { phase: 'error', text: agentSafeFailure() };
    return this.submit(choice.handle, signal);
  }
  async submit(utterance: string, signal: AbortSignal): Promise<AgentLoopView> {
    const diagnostic = this.trace();
    let stage = 'interpret';
    let interpretState: AgentStateKind | 'none' = 'none';
    let repaired = false;
    try {
      const context = this.registry.context(signal);
      if (!utterance.trim() || utterance.length > AGENT_LIMITS.utterance) throw new AgentProtocolError('Utterance limit');
      let localSelection: AgentEnvelope | null = null;
      let localClarification: SkillClarification | null = null;
      let localSource: 'local_clarification' | 'local_choice' | 'local_high_confidence_move' | 'local_standalone_finish' | null = null;
      if (!this.task) {
        this.task = { diagnostic, goal: utterance, trace: [], handles: new VoiceAgentResourceHandles(), session: this.session, proposals: new VoiceAgentProposalStore(), pendingChoice: null, pendingSkillClarification: null, choiceAnswered: false, confirmation: false, clarification: false, modelSteps: 0, skillCalls: 0, results: [] };
        const extracted = extractHighConfidenceMoveCall(utterance, context.board);
        if (extracted) {
          localSelection = extracted;
          localSource = 'local_high_confidence_move';
        }
      } else if (this.task.pendingChoice) {
        this.task.choiceAnswered = true;
        localSelection = localChoiceCall(this.task, utterance);
        if (localSelection) localSource = 'local_choice';
      } else if (this.task.pendingSkillClarification) {
        localClarification = this.task.pendingSkillClarification;
        localSelection = completeSkillClarification(localClarification, utterance);
        localSource = 'local_clarification';
      }
      const task = this.task;
      this.append(task, { user: utterance });
      while (true) {
        let envelope: AgentEnvelope | undefined;
        let repair: string | undefined;
        repaired = false;
        const state = this.state(task);
        interpretState = state.kind;
        let sourcedLocally = false;
        if (localSelection) {
          envelope = localSelection;
          const source = localSource;
          sourcedLocally = source === 'local_clarification' || source === 'local_choice' || source === 'local_high_confidence_move';
          localSelection = null;
          diagnostic.emit('interpret', localClarification && source === 'local_clarification'
            ? { step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind, source, clarificationKind: 'skill_argument', skill: localClarification.skill, missing: localClarification.missing, clarificationResolved: true }
            : { step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind, source, ...(envelope.kind === 'skill_call' ? { skill: envelope.skill, ...Object.fromEntries(Object.entries(envelope.arguments).filter(([key]) => ['reference', 'todoRef', 'lane', 'member', 'tag', 'title'].includes(key))) } : {}) });
          localClarification = null;
          localSource = null;
        }
        for (let attempt = 0; !envelope && attempt < 2; attempt++) {
          if (task.modelSteps >= AGENT_LIMITS.modelSteps || task.skillCalls >= AGENT_LIMITS.skillCalls) throw new AgentProtocolError('Task limit');
          task.modelSteps++;
          const input = JSON.stringify({ goal: task.goal, activeTodoAvailable: !!this.session.activeTodo, trace: task.trace,
            pending: state.kind === 'choice' ? { ...state, ...task.pendingChoice!.result } : state,
            ...(repair ? { repair: agentRepairInstruction(state, repair) } : {}),
          });
          stage = 'interpret';
          const raw = await this.model(input, signal);
          this.registry.context(signal);
          if (this.task !== task) throw new AgentProtocolError('Task expired');
          try {
            const parsed = interpretAgentEnvelope(raw, state);
            const recovered = recoverPresentMoveArguments(utterance, parsed.envelope, context.board, state.kind);
            envelope = recovered?.envelope ?? parsed.envelope;
            diagnostic.emit('interpret', {
              step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind,
              ...(parsed.recoveredFrom ? { recoveredFrom: parsed.recoveredFrom } : {}),
              ...(recovered ? { semanticGuard: recovered.recoveredFrom, modelInterpretationKind: parsed.envelope.kind } : {}),
              ...(envelope.kind === 'skill_call' ? { skill: envelope.skill, ...Object.fromEntries(Object.entries(envelope.arguments).filter(([key]) => ['reference', 'todoRef', 'lane', 'member', 'tag', 'title'].includes(key))) } : {}),
              ...(envelope.kind === 'clarify_skill' ? { skill: envelope.skill, missing: envelope.missing } : {}),
            });
            break;
          }
          catch (error) {
            const recovered = recoverMoveAfterParseFailure(utterance, context.board, state.kind);
            if (recovered) {
              envelope = recovered.envelope;
              sourcedLocally = envelope.kind === 'skill_call';
              diagnostic.emit('interpret', {
                step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind,
                semanticGuard: recovered.recoveredFrom,
                ...(envelope.kind === 'skill_call' ? { skill: envelope.skill, ...Object.fromEntries(Object.entries(envelope.arguments).filter(([key]) => ['reference', 'todoRef', 'lane', 'member', 'tag', 'title'].includes(key))) } : {}),
                ...(envelope.kind === 'clarify_skill' ? { skill: envelope.skill, missing: envelope.missing } : {}),
              });
              break;
            }
            if (!(error instanceof AgentProtocolError) || attempt === 1) throw error;
            repair = error.message;
            repaired = true;
          }
        }
        if (!envelope) throw new AgentProtocolError('Missing envelope');
        this.append(task, { agent: envelope });
        // Confirmation and clarification are exclusive; the next model request must see exactly one pending.kind.
        task.clarification = envelope.kind === 'ask_user';
        if (task.clarification) task.confirmation = false;
        if (envelope.kind === 'confirm') return this.confirm(signal);
        if (envelope.kind === 'decline' || envelope.kind === 'cancel') return this.cancel();
        if (envelope.kind === 'ask_user') {
          diagnostic.emit('resolve', { phase: 'initial', result: 'question', choiceCount: task.pendingChoice?.result.choices.length ?? 0 });
          const pending = task.pendingChoice?.result;
          return { phase: 'question', text: pending ? renderAgentSkillResult(pending) : envelope.text,
            ...(pending ? { choices: pending.choices.map(choice => ({ id: choice.handle, label: `${choice.number ? `#${choice.number} · ` : ''}${choice.label}${choice.lane ? ` · ${choice.lane}` : ''}` })) } : {}) };
        }
        if (envelope.kind === 'clarify_skill') {
          task.confirmation = false;
          task.clarification = false;
          task.pendingSkillClarification = Object.freeze({ ...envelope, arguments: Object.freeze({ ...envelope.arguments }) }) as SkillClarification;
          diagnostic.emit('resolve', { phase: 'initial', result: 'question', choiceCount: 0, clarificationKind: 'skill_argument', skill: envelope.skill, missing: envelope.missing });
          return { phase: 'question', text: envelope.text };
        }
        if (envelope.kind === 'finish') {
          if (task.pendingChoice) throw new AgentProtocolError('Choice unresolved');
          if (task.proposals.count) {
            stage = 'confirmation_preflight';
            await task.proposals.preflight(this.registry, task, signal, 'confirmation_preflight');
            task.confirmation = true;
            task.clarification = false;
            diagnostic.emit('confirmation', { phase: 'initial', required: true, ...task.proposals.diagnosticSummary() });
            return { phase: 'confirmation', text: `${task.proposals.summaries().join('; ')}?`, speechText: task.proposals.confirmationSpeech(), danger: task.proposals.danger, confirmLabel: task.proposals.confirmationLabel() };
          }
          const text = task.results.filter(result => result.status !== 'choices').map(renderAgentSkillResult).join('; ') || voiceText('voice.agent.noChanges', 'No changes needed.');
          this.finish(task, 0);
          return { phase: 'success', text };
        }
        if (envelope.kind === 'skill_call') {
          task.skillCalls++;
          // A correction/addition invalidates the old confirmation before any new work.
          task.confirmation = false;
          stage = 'target_resolution';
          const outcome = await this.registry.run(envelope, task, signal);
          task.pendingSkillClarification = null;
          this.registry.context(signal);
          if (this.task !== task) throw new AgentProtocolError('Task expired');
          if (outcome.prepared) {
            stage = 'proposal_preparation';
            diagnostic.command(outcome.prepared.command, 'initial');
          } else if (outcome.result.status !== 'opened') {
            const resolution = outcome.result.status === 'not_found' && 'resource' in outcome.result
              ? (outcome.result.resource === 'lane' ? 'lane_resolution' : 'target_resolution')
              : outcome.result.status === 'choices' ? 'target_resolution' : 'proposal_preparation';
            diagnostic.emit('resolve', { phase: 'initial', skill: envelope.skill, result: outcome.result.status, resolution, ...('number' in outcome.result ? { localId: outcome.result.number } : {}), ...(outcome.result.status === 'choices' ? { choiceCount: outcome.result.choices.length } : {}), ...(outcome.result.status === 'not_found' && 'resource' in outcome.result ? { resource: outcome.result.resource } : {}) });
          }
          if (outcome.prepared) {
            const ref = task.proposals.add(outcome.prepared);
            if (outcome.result.status === 'prepared') outcome.result.proposalRef = ref;
          }
          if (JSON.stringify(outcome.result).length > AGENT_LIMITS.resultText) throw new AgentProtocolError('Result limit');
          if (['stale', 'denied', 'invalid', 'not_found'].includes(outcome.result.status)) {
            const resolution = outcome.result.status === 'not_found' && 'resource' in outcome.result
              ? (outcome.result.resource === 'lane' ? 'lane_resolution' : 'target_resolution')
              : outcome.result.status === 'stale' ? 'stale_context' : 'proposal_preparation';
            diagnostic.emit('failure', { source: 'resolve', reason: outcome.result.status, safeFailureStage: resolution, ...(outcome.result.status === 'not_found' && 'resource' in outcome.result ? { resource: outcome.result.resource } : {}) });
            diagnostic.end('resolution_failure', { code: outcome.result.status, safeFailureStage: resolution });
            const text = renderAgentSkillResult(outcome.result);
            this.finish(task, 0);
            return { phase: 'error', text };
          }
          this.append(task, { skillResult: { skill: envelope.skill, result: outcome.result } });
          task.results.push(outcome.result);
          const standaloneOpenComplete = outcome.result.status === 'opened' && isStandaloneNamedOpenGoal(task.goal)
            && !task.proposals.count && task.results.every(result => ['choices', 'resolved', 'opened'].includes(result.status));
          if (standaloneOpenComplete) {
            const text = renderAgentSkillResult(outcome.result);
            this.finish(task, 0);
            return { phase: 'success', text };
          }
          if ((outcome.prepared || outcome.result.status === 'no_op') && !task.pendingChoice
            && (sourcedLocally || (envelope.skill === 'todos.move' && isCompleteStandaloneMove(task.goal, context.board)))) {
            localSelection = { kind: 'finish' };
            localSource = 'local_standalone_finish';
          }
        }
      }
    } catch (error) {
      const classified = classifySafeFailure(stage, interpretState, error, repaired);
      diagnostic.emit(stage === 'interpret' ? 'interpret' : 'failure', {
        result: 'failure', source: stage, safeFailureStage: classified.safeFailureStage, reason: classified.reason,
        ...(error instanceof AgentProtocolError ? error.diagnostic : {}),
      });
      diagnostic.end(`${classified.safeFailureStage}_failure`, { source: stage, reason: classified.reason });
      this.invalidate();
      return { phase: 'error', text: agentSafeFailure() };
    }
  }
}
