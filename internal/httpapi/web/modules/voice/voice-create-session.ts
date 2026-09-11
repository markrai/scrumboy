import { getAppRuntime } from '../platform/runtime.js';
import type { SpeechInputCaptureContext } from '../platform/speech-input.js';
import { SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS } from '../platform/speech-output.js';
import type { AgentLoopView } from './agent-loop.js';
import { getActiveVoiceCommandContext, canRunVoiceMutationInContext, type VoiceCommandOptions } from './command-context.js';
import { executeCommandIR } from './execute.js';
import { callMcpTool } from './mcp-client.js';
import { isCommandFailure } from './schema.js';
import { voiceText } from './i18n.js';
import { createVoiceFlowTrace } from './trace.js';
import { VoiceCreatePlanError, type VoiceCreatePlanResult, type VoiceCreatePlanV1 } from './voice-create-plan.js';
import { VOICE_CREATE_PLANNER_VERSION, type VoiceCreatePlanner } from './voice-create-planner.js';
import { evaluateVoiceCreateSemantics, prepareVoiceCreateAgainstCurrentContext } from './voice-create-evaluation.js';
import { readVoiceCreateMembers } from './voice-create-members.js';
import {
  formatVoiceCreateMember,
  type CreateMemberChoice,
  type CreatePreparation,
  type PreparedVoiceCreate,
  type VoiceCreateTagBinding,
  type VoiceCreateTagSuggestion,
} from './voice-create-prepare.js';
import { readVoiceCreateTags, type VoiceCreateTagsReader } from './voice-create-tags.js';
import { classifyVoiceBinaryDecision, classifyVoiceReviewDecision, type VoiceReviewDecision } from './vocabulary.js';

function wholeUtterance(text: string): string { return text.trim().toLowerCase().replace(/[.!?,]+$/g, '').trim().replace(/\s+/g, ' '); }
export function voiceCreateDecision(text: string): VoiceReviewDecision { return classifyVoiceReviewDecision(text); }
type Options = VoiceCommandOptions & { planner: VoiceCreatePlanner; callTool?: typeof callMcpTool; readTags?: VoiceCreateTagsReader; execute?: typeof executeCommandIR; serverOrigin?: () => string };
type Task = {
  plan: VoiceCreatePlanV1;
  choices: readonly CreateMemberChoice[];
  prepared: PreparedVoiceCreate | null;
  member?: CreateMemberChoice;
  tagBindings: readonly VoiceCreateTagBinding[];
  tagSuggestion: VoiceCreateTagSuggestion | null;
};
function failureText(error: unknown): string {
  const code = error instanceof VoiceCreatePlanError ? error.code : 'network';
  switch (code) {
    case 'missing_title': return voiceText('voice.create.missingTitle', 'Please restate the create request with a title.');
    case 'incomplete_request': return voiceText('voice.create.incomplete', 'The complete request could not be prepared. Create v2 supports one new story with a lane, one assignee, existing tags and notes. Restate it or switch to All commands.');
    case 'lane': return voiceText('voice.create.laneError', 'The requested lane or authoritative lane order is unavailable or ambiguous. No create was prepared.');
    case 'member': return voiceText('voice.errors.assigneeNotFound', 'Assignee was not found in this project.');
    case 'tag': return voiceText('voice.create.tagError', 'A requested tag is unavailable or ambiguous. The complete request was not prepared.');
    case 'unauthorized': return voiceText('voice.errors.unauthorizedMutation', 'Only maintainers can run mutating commands.');
    case 'stale_context': return voiceText('voice.create.stale', 'The reviewed context changed. Restate the request for a fresh review.');
    default: return voiceText('voice.create.failed', 'The create request could not be prepared safely. Please try again.');
  }
}

/** Application interaction, not an agent loop. Only confirm owns the execution port. */
export class VoiceCreateSession {
  private task: Task | null = null;
  private diagnostic: ReturnType<typeof createVoiceFlowTrace> | null = null;
  private binaryClarification: 'tag_suggestion' | 'final_confirmation' | null = null;
  private revision = 0;
  private working = false;
  private readonly origin: string;
  constructor(private readonly options: Options) { this.origin = this.serverOrigin(); }
  private serverOrigin() { return (this.options.serverOrigin ?? (() => getAppRuntime().serverOrigin()))(); }
  context(signal: AbortSignal) {
    const context = getActiveVoiceCommandContext(this.options);
    if (signal.aborted || this.serverOrigin() !== this.origin || isCommandFailure(context)) throw new VoiceCreatePlanError('stale_context');
    if (!canRunVoiceMutationInContext(context.value)) throw new VoiceCreatePlanError('unauthorized');
    return context.value;
  }
  get pending() { return !!this.task || this.working; }
  get confirmationPending() { return !!(this.task?.prepared || this.task?.tagSuggestion) && !this.working; }
  get captureContext(): SpeechInputCaptureContext {
    if (this.binaryClarification) return 'binary_clarification_capture';
    if (this.task?.tagSuggestion) return 'tag_suggestion_capture';
    if (this.task?.prepared) return 'final_confirmation_capture';
    if (this.task?.choices.length) return 'member_clarification_capture';
    return 'initial_create_capture';
  }
  trace() {
    if (!this.diagnostic || this.diagnostic.ended) this.diagnostic = createVoiceFlowTrace();
    return this.diagnostic;
  }
  endTrace(reason: string, fields: Record<string, unknown> = {}) { this.diagnostic?.end(reason, fields); }
  cancelTrace(reason: string, retained = false) { this.diagnostic?.emit('cancel', { reason, interactionRetained: retained }); if (!retained) this.endTrace(reason); }
  setKeepListening(_enabled: boolean) {} // No active-todo/session inference in this slice.
  invalidate() { this.revision++; this.task = null; this.binaryClarification = null; this.endTrace('controller_invalidated'); }
  cancel(): AgentLoopView { this.cancelTrace('cancelled_by_user'); this.revision++; this.task = null; this.binaryClarification = null; return { phase: 'success', text: voiceText('voice.status.cancelled', 'Cancelled.') }; }
  private check(signal: AbortSignal, revision: number) {
    const context = this.context(signal);
    if (revision !== this.revision) throw new VoiceCreatePlanError('stale_context');
    return context;
  }
  private fail(error: unknown): AgentLoopView {
    this.task = null;
    this.binaryClarification = null;
    if (error instanceof VoiceCreatePlanError) {
      const plannerCode = ['invalid_json', 'not_object', 'wrong_version', 'invalid_kind', 'unknown_fields', 'missing_required_field', 'invalid_title', 'invalid_lane', 'invalid_assignee', 'invalid_tags', 'invalid_notes', 'invalid_unhandled', 'output_too_large', 'surrounding_prose'].includes(error.code);
      if (error.code === 'tag') this.trace().emit('resolve', { entityType: 'tag', ...error.details });
      this.trace().emit('failure', { code: error.code, ...(plannerCode ? { plannerVersion: VOICE_CREATE_PLANNER_VERSION, ...error.details } : {}) });
    } else this.trace().emit('failure', { code: 'preparation_failed' });
    this.endTrace('create_failed');
    return { phase: 'error', text: failureText(error) };
  }
  private review(): AgentLoopView {
    const summary = this.task!.prepared!.command.summary;
    return { phase: 'confirmation', text: summary, danger: false, speechText: summary.length <= SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS ? summary : null };
  }
  private binaryQuestion(pendingDecision: 'tag_suggestion' | 'final_confirmation'): AgentLoopView {
    this.binaryClarification = pendingDecision;
    const text = voiceText('voice.create.yesNo', 'Is that a yes or no?');
    this.trace().emit('confirmation', { phase: 'binary_clarification', result: 'ambiguous', pendingDecision });
    return { phase: 'confirmation', text, danger: false, speechText: text };
  }
  private tagSuggestionView(): AgentLoopView {
    const text = voiceText('voice.create.tagSuggestion', 'Did you mean tag `{tag}`?', { tag: this.task!.tagSuggestion!.tag });
    return { phase: 'confirmation', text, danger: false, speechText: text };
  }
  private choiceView(): AgentLoopView {
    return { phase: 'question', text: voiceText('voice.create.whichPerson', 'Which person? Select a name or say its option number.'),
      choices: this.task!.choices.map((member, index) => ({ id: String(index), label: `${index + 1}. ${formatVoiceCreateMember(member)}` })) };
  }
  private tracePlan(plan: VoiceCreatePlanResult) {
    this.trace().emit('plan', { plannerVersion: VOICE_CREATE_PLANNER_VERSION, kind: plan.kind,
      ...(plan.kind === 'create' ? { hasTitle: !!plan.title, explicitLane: plan.lane !== undefined, explicitAssignee: plan.assignee !== undefined,
        tagCount: plan.tags?.length ?? 0, notesLength: plan.notes?.length ?? 0, unhandledCount: plan.unhandled?.length ?? 0 } : {}) });
  }
  private async readMembers(projectSlug: string, signal: AbortSignal) {
    return readVoiceCreateMembers(projectSlug, signal, this.options.callTool ?? callMcpTool);
  }
  private async readTags(projectSlug: string, signal: AbortSignal) {
    return (this.options.readTags ?? readVoiceCreateTags)(projectSlug, signal);
  }
  private async prepare(
    plan: VoiceCreatePlanV1,
    signal: AbortSignal,
    revision: number,
    member?: CreateMemberChoice,
    tagBindings: readonly VoiceCreateTagBinding[] = [],
  ) {
    return prepareVoiceCreateAgainstCurrentContext(plan, signal, {
      context: currentSignal => this.check(currentSignal, revision),
      refreshBoard: this.options.refreshBoard,
      readMembers: (projectSlug, currentSignal) => this.readMembers(projectSlug, currentSignal),
      readTags: (projectSlug, currentSignal) => this.readTags(projectSlug, currentSignal),
    }, member, tagBindings);
  }
  private presentPreparation(task: Task, result: CreatePreparation, member?: CreateMemberChoice): AgentLoopView {
    this.binaryClarification = null;
    if (result.kind === 'member-choice') {
      this.task = { ...task, choices: result.choices, prepared: null, member, tagSuggestion: null };
      this.trace().emit('resolve', { result: 'member-choice', choiceCount: result.choices.length });
      return this.choiceView();
    }
    if (result.kind === 'tag-suggestion') {
      this.task = { ...task, choices: [], prepared: null, member, tagSuggestion: result.suggestion };
      this.trace().emit('resolve', {
        entityType: 'tag',
        result: 'suggestion_pending',
        candidateCount: 1,
        suggestionKind: result.suggestion.kind,
      });
      return this.tagSuggestionView();
    }
    this.task = { ...task, choices: [], prepared: result.value, member: result.value.member, tagSuggestion: null };
    return this.presentReview();
  }
  private declineTagSuggestion(): AgentLoopView {
    this.trace().emit('resolve', { entityType: 'tag', result: 'suggestion_declined', candidateCount: 1 });
    return this.fail(new VoiceCreatePlanError('tag', {
      entityType: 'tag',
      result: 'unavailable',
      candidateCount: 0,
      referenceNormalizationApplied: false,
    }));
  }
  private async acceptTagSuggestion(signal: AbortSignal): Promise<AgentLoopView> {
    const task = this.task;
    const suggestion = task?.tagSuggestion;
    if (!task || !suggestion || this.working) return { phase: 'error', text: voiceText('voice.create.noReview', 'There is no create awaiting confirmation.') };
    this.working = true;
    const revision = this.revision;
    const tagBindings = Object.freeze([...task.tagBindings, Object.freeze({ referenceIndex: suggestion.referenceIndex, tag: suggestion.tag })]);
    try {
      this.trace().emit('resolve', {
        entityType: 'tag',
        result: 'suggestion_accepted',
        candidateCount: 1,
        suggestionKind: suggestion.kind,
      });
      const result = await this.prepare(task.plan, signal, revision, task.member, tagBindings);
      return this.presentPreparation({ ...task, tagBindings, tagSuggestion: null }, result, task.member);
    } catch (error) {
      return revision === this.revision ? this.fail(error) : { phase: 'error', text: failureText(error) };
    } finally { this.working = false; }
  }
  async submit(transcript: string, signal: AbortSignal): Promise<AgentLoopView> {
    if (this.working) return { phase: 'error', text: failureText(new VoiceCreatePlanError('stale_context')) };
    const binaryDecision = classifyVoiceBinaryDecision(transcript);
    const decision = voiceCreateDecision(transcript);
    if (this.task?.tagSuggestion) {
      this.trace().emit('confirmation', { phase: 'decision', pendingDecision: 'tag_suggestion', result: binaryDecision });
      if (binaryDecision === 'yes') return this.acceptTagSuggestion(signal);
      if (binaryDecision === 'no') return this.declineTagSuggestion();
      if (binaryDecision === 'cancel') return this.cancel();
      return this.binaryQuestion('tag_suggestion');
    }
    if (this.task?.prepared) {
      this.trace().emit('confirmation', { phase: 'decision', pendingDecision: 'final_confirmation', result: binaryDecision });
      if (decision === 'confirm') return this.confirm(signal);
      if (decision === 'cancel') return this.cancel();
      return this.binaryQuestion('final_confirmation');
    }
    if (this.task?.choices.length) {
      if (decision === 'cancel') return this.cancel();
      const normalized = wholeUtterance(transcript);
      const number = /^(?:option )?([1-9]\d*)$/.exec(normalized);
      const names = this.task.choices.map((member, index) => ({ member, index })).filter(({ member }) => normalized === wholeUtterance(member.name) || normalized === wholeUtterance(member.email));
      const index = number ? Number(number[1]) - 1 : names.length === 1 ? names[0].index : -1;
      return index >= 0 && index < this.task.choices.length ? this.choose(index, signal) : this.choiceView();
    }
    if (decision !== 'unknown') return { phase: 'error', text: voiceText('voice.create.noReview', 'There is no create awaiting confirmation.') };
    this.binaryClarification = null;
    const revision = ++this.revision;
    this.working = true;
    try {
      const evaluation = await evaluateVoiceCreateSemantics(transcript, signal, {
        planner: this.options.planner,
        context: currentSignal => this.check(currentSignal, revision),
        refreshBoard: this.options.refreshBoard,
        readMembers: (projectSlug, currentSignal) => this.readMembers(projectSlug, currentSignal),
        readTags: (projectSlug, currentSignal) => this.readTags(projectSlug, currentSignal),
        onPlannerStart: () => this.trace().emit('planner_start', { plannerVersion: VOICE_CREATE_PLANNER_VERSION, transcriptLength: transcript.length, modelCall: 1 }),
        onPlan: plan => this.tracePlan(plan),
      });
      const task: Task = { plan: evaluation.plan, choices: [], prepared: null, tagBindings: [], tagSuggestion: null };
      return this.presentPreparation(task, evaluation.preparation);
    } catch (error) { if (revision !== this.revision) return { phase: 'error', text: failureText(error) }; return this.fail(error); }
    finally { this.working = false; }
  }
  private presentReview(): AgentLoopView {
    const prepared = this.task!.prepared!;
    this.trace().command(prepared.command, 'confirmation_preflight');
    this.trace().emit('resolve', { defaultLane: prepared.plan.lane === undefined, defaultAssignee: prepared.plan.assignee === undefined });
    if (prepared.plan.tags?.length) this.trace().emit('resolve', { entityType: 'tag', result: 'resolved', candidateCount: 1,
      referenceNormalizationApplied: prepared.tagReferenceNormalizationApplied });
    this.trace().emit('confirmation', { required: true, proposalCount: 1, plannerVersion: VOICE_CREATE_PLANNER_VERSION });
    return this.review();
  }
  async choose(index: number, signal: AbortSignal): Promise<AgentLoopView> {
    const task = this.task;
    const member = task?.choices[index];
    if (!member || this.working) return { phase: 'error', text: failureText(new VoiceCreatePlanError('stale_context')) };
    this.working = true;
    const revision = this.revision;
    try {
      const result = await this.prepare(task.plan, signal, revision, member, task.tagBindings);
      return this.presentPreparation({ ...task, member }, result, member);
    } catch (error) { return revision === this.revision ? this.fail(error) : { phase: 'error', text: failureText(error) }; }
    finally { this.working = false; }
  }
  async confirm(signal: AbortSignal): Promise<AgentLoopView> {
    if (this.task?.tagSuggestion) return this.acceptTagSuggestion(signal);
    const task = this.task;
    const prepared = task?.prepared;
    if (!prepared || this.working) return { phase: 'error', text: voiceText('voice.create.noReview', 'There is no create awaiting confirmation.') };
    this.working = true;
    this.binaryClarification = null;
    this.task = null; // Consume consent before any asynchronous work; no retries after a sent write.
    const revision = this.revision;
    let dispatched = false;
    try {
      this.trace().emit('confirmation', { phase: 'confirm_revalidation', result: 'started' });
      const fresh = await this.prepare(prepared.plan, signal, revision, prepared.member, task.tagBindings);
      if (fresh.kind !== 'prepared' || fresh.value.fingerprint !== prepared.fingerprint) throw new VoiceCreatePlanError('stale_context');
      this.check(signal, revision);
      this.trace().emit('confirmation', { phase: 'confirm_revalidation', result: 'accepted' });
      this.trace().emit('execute', { result: 'started', commandIntent: 'todos.create', requestCount: 1 });
      dispatched = true;
      await (this.options.execute ?? executeCommandIR)(prepared.command.ir, { signal, recordMutation: this.options.recordMutation });
      this.trace().emit('execute', { result: 'success', commandIntent: 'todos.create' });
      let refreshFailed = false;
      try { await this.options.refreshBoard(); } catch { refreshFailed = true; }
      this.endTrace(refreshFailed ? 'refresh_failure' : 'success', { modelCalls: 1, mutationsExecuted: 1 });
      return { phase: 'success', text: refreshFailed ? voiceText('voice.create.refreshFailed', 'Story created. The board could not refresh; refresh it before trying another create.') : voiceText('voice.status.done', 'Done.') };
    } catch (error) {
      if (!dispatched) return this.fail(error);
      this.trace().emit('execute', { result: 'failed_or_unknown', commandIntent: 'todos.create' });
      this.endTrace('execution_unconfirmed');
      try { await this.options.refreshBoard(); } catch { /* No retry of the create. */ }
      return { phase: 'error', text: voiceText('voice.create.unknown', 'Creation failed or could not be confirmed. Check the board before trying again; the request will not be retried automatically.') };
    } finally { this.working = false; }
  }
}
