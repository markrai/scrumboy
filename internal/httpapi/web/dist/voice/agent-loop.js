import { createVoiceFlowTrace } from './trace.js';
import { voiceText } from './i18n.js';
import { AGENT_LIMITS, AgentProtocolError, agentRepairInstruction, completeSkillClarification, interpretAgentEnvelope } from './agent-protocol.js';
import { VoiceAgentResourceHandles } from './agent-resources.js';
import { VoiceAgentProposalStore } from './agent-proposals.js';
import { renderAgentSkillResult } from './agent-skills.js';
import { normalizeLookup } from './normalize.js';
export const agentSafeFailure = () => voiceText('voice.agent.safeFailure', 'I could not finish that safely. Please try again.');
function batchText(result) {
    if (!result.failed && !result.refreshFailed)
        return `${voiceText('voice.status.done', 'Done.')} ${result.succeeded.join('; ')}`;
    return voiceText('voice.agent.batchResult', 'Succeeded: {succeeded}. Failed or unconfirmed: {failed}. Not attempted: {remaining}. Refresh failed: {refresh}.', {
        succeeded: result.succeeded.join('; ') || '—', failed: result.failed || '—', remaining: result.unattempted.join('; ') || '—', refresh: result.refreshFailed ? '✓' : '—',
    });
}
const choiceFillers = new Set(['the', 'one', 'story', 'todo', 'card', 'task', 'item', 'option', 'please', 'i', 'choose', 'select', 'offered', 'number', 'in']);
const choiceOrdinals = new Map([['first', 0], ['second', 1], ['third', 2], ['fourth', 3], ['fifth', 4]]);
function words(value) { return normalizeLookup(value).match(/[\p{L}\p{N}]+/gu) ?? []; }
function isStandaloneNamedOpenGoal(goal) {
    const normalized = normalizeLookup(goal);
    if (!/^(?:please )?(?:open|find(?: me)?|search for|look up)\s+\S/.test(normalized))
        return false;
    if (/\b(?:all|every|everything|multiple|tagged|assigned to)\b/.test(normalized))
        return false;
    // Ambiguous punctuation/connectors bias toward continuing the agent so additional work is never dropped.
    return !/[&,;\n]/.test(goal) && !/\b(?:and|then|also|plus)\b/.test(normalized);
}
/** Resolve only an unambiguous reference to the authoritative choices already on screen. */
function localChoiceCall(task, utterance) {
    const pending = task.pendingChoice;
    if (!pending)
        return null;
    const choices = pending.result.choices;
    const normalized = normalizeLookup(utterance);
    const exactHandle = choices.findIndex(choice => normalized === normalizeLookup(choice.handle));
    let selectedIndex = exactHandle;
    if (selectedIndex < 0) {
        const constraints = [];
        const ordinals = [...new Set(words(normalized).map(word => choiceOrdinals.get(word)).filter((index) => index !== undefined && index < choices.length))];
        if (ordinals.length > 1)
            return null;
        if (ordinals.length === 1)
            constraints.push(ordinals[0]);
        const numerals = [...new Set([...normalized.matchAll(/(?:^|\s|#)(\d+)(?=$|\s)/g)].map(match => Number(match[1])))];
        for (const numeral of numerals) {
            const numbered = choices.map((choice, index) => choice.number === numeral ? index : -1).filter(index => index >= 0);
            if (numbered.length === 1)
                constraints.push(numbered[0]);
            else if (!numbered.length && numeral >= 1 && numeral <= choices.length)
                constraints.push(numeral - 1);
            else
                return null;
        }
        const terms = words(normalized).filter(word => !choiceFillers.has(word) && !choiceOrdinals.has(word) && !/^\d+$/.test(word));
        if (terms.length) {
            const labeled = choices.map((choice, index) => {
                const candidate = new Set(words(`${choice.label} ${choice.lane ?? ''}`));
                return terms.every(term => candidate.has(term)) ? index : -1;
            }).filter(index => index >= 0);
            if (labeled.length !== 1)
                return null;
            constraints.push(labeled[0]);
        }
        if (!constraints.length || new Set(constraints).size !== 1)
            return null;
        selectedIndex = constraints[0];
    }
    const choice = choices[selectedIndex];
    const args = { ...pending.call.arguments };
    if (pending.result.resource === 'todo') {
        delete args.reference;
        delete args.todoRef;
        args.todoRef = choice.handle;
    }
    else
        args[pending.result.resource] = choice.handle;
    return { ...pending.call, arguments: args };
}
export class VoiceAgentLoop {
    trace() {
        if (!this.diagnostic || this.diagnostic.ended)
            this.diagnostic = createVoiceFlowTrace();
        return this.diagnostic;
    }
    adoptTrace(trace) { this.diagnostic = trace; }
    endTrace(reason) { this.diagnostic?.end(reason); }
    cancelTrace(reason, retained = false) {
        this.diagnostic?.emit('cancel', { reason, interactionRetained: retained });
        if (!retained)
            this.diagnostic?.end(reason);
    }
    constructor(model, registry, keepListening) {
        this.model = model;
        this.registry = registry;
        this.task = null;
        this.diagnostic = null;
        this.session = { activeTodo: null, keepListening };
    }
    get pending() { return !!this.task; }
    get confirmationPending() { return !!this.task?.confirmation; }
    get currentState() { return this.task ? this.state(this.task) : null; }
    /** Canonical state: the model input, envelope legality and repair guidance are all derived from it. */
    state(task) {
        if (task.confirmation)
            return { kind: 'confirmation', proposalCount: task.proposals.count };
        if (task.pendingChoice)
            return { kind: 'choice' };
        if (task.pendingSkillClarification) {
            const { skill, arguments: args, missing } = task.pendingSkillClarification;
            return { kind: 'clarification', skillClarification: { skill, arguments: args, missing } };
        }
        if (task.clarification)
            return { kind: 'clarification' };
        if (task.proposals.count)
            return { kind: 'proposals_ready', proposalCount: task.proposals.count };
        return { kind: 'idle' };
    }
    append(task, value) {
        task.trace.push(value);
        if (task.trace.length > AGENT_LIMITS.trace)
            task.trace.shift();
    }
    invalidate() {
        this.diagnostic?.end('controller_invalidated');
        this.task?.handles.clear();
        this.task?.proposals.clear();
        if (this.task) {
            this.task.goal = '';
            this.task.confirmation = false;
            this.task.clarification = false;
            this.task.trace.length = 0;
            this.task.results.length = 0;
            this.task.pendingChoice = null;
            this.task.pendingSkillClarification = null;
        }
        this.task = null;
        this.session.activeTodo = null;
    }
    setKeepListening(enabled) {
        this.session.keepListening = enabled;
        if (!enabled && !this.task)
            this.session.activeTodo = null;
    }
    finish(task, mutations) {
        task.diagnostic?.end('success', { modelSteps: task.modelSteps, skillCalls: task.skillCalls, mutationsExecuted: mutations });
        task.handles.clear();
        task.proposals.clear();
        task.goal = '';
        task.confirmation = false;
        task.clarification = false;
        task.trace.length = 0;
        task.results.length = 0;
        task.pendingChoice = null;
        task.pendingSkillClarification = null;
        this.task = null;
        if (!this.session.keepListening)
            this.session.activeTodo = null;
    }
    cancel() {
        this.cancelTrace('cancelled_by_user');
        if (this.task)
            this.finish(this.task, 0);
        return { phase: 'success', text: voiceText('voice.status.cancelled', 'Cancelled.') };
    }
    async confirm(signal) {
        const task = this.task;
        if (!task?.confirmation)
            return { phase: 'error', text: agentSafeFailure() };
        task.confirmation = false;
        try {
            const result = await task.proposals.confirm(this.registry, task, signal);
            task.diagnostic?.end(result.failed ? 'execution_failure' : result.refreshFailed ? 'refresh_failure' : 'success', { succeededCount: result.succeeded.length, unattemptedCount: result.unattempted.length });
            const view = { phase: result.failed || result.refreshFailed ? 'error' : 'success', text: batchText(result) };
            this.finish(task, result.succeeded.length);
            return view;
        }
        catch (error) {
            task.diagnostic?.emit('failure', { source: 'confirm_revalidation', reason: error instanceof AgentProtocolError ? error.message : 'revalidation_exception' });
            task.diagnostic?.end('confirmation_failed');
            this.invalidate();
            return { phase: 'error', text: agentSafeFailure() };
        }
    }
    async choose(index, signal) {
        const choice = this.task?.pendingChoice?.result.choices[index];
        if (!choice)
            return { phase: 'error', text: agentSafeFailure() };
        return this.submit(choice.handle, signal);
    }
    async submit(utterance, signal) {
        const diagnostic = this.trace();
        let stage = 'interpret';
        try {
            this.registry.context(signal);
            if (!utterance.trim() || utterance.length > AGENT_LIMITS.utterance)
                throw new AgentProtocolError('Utterance limit');
            let localSelection = null;
            let localClarification = null;
            if (!this.task) {
                this.task = { diagnostic, goal: utterance, trace: [], handles: new VoiceAgentResourceHandles(), session: this.session, proposals: new VoiceAgentProposalStore(), pendingChoice: null, pendingSkillClarification: null, choiceAnswered: false, confirmation: false, clarification: false, modelSteps: 0, skillCalls: 0, results: [] };
            }
            else if (this.task.pendingChoice) {
                this.task.choiceAnswered = true;
                localSelection = localChoiceCall(this.task, utterance);
            }
            else if (this.task.pendingSkillClarification) {
                localClarification = this.task.pendingSkillClarification;
                localSelection = completeSkillClarification(localClarification, utterance);
            }
            const task = this.task;
            this.append(task, { user: utterance });
            while (true) {
                let envelope;
                let repair;
                const state = this.state(task);
                if (localSelection) {
                    envelope = localSelection;
                    localSelection = null;
                    diagnostic.emit('interpret', localClarification
                        ? { step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind, source: 'local_clarification', clarificationKind: 'skill_argument', skill: localClarification.skill, missing: localClarification.missing, clarificationResolved: true }
                        : { step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind, source: 'local_choice', skill: envelope.skill, ...Object.fromEntries(Object.entries(envelope.arguments).filter(([key]) => ['reference', 'todoRef', 'lane', 'member', 'tag', 'title'].includes(key))) });
                    localClarification = null;
                }
                for (let attempt = 0; !envelope && attempt < 2; attempt++) {
                    if (task.modelSteps >= AGENT_LIMITS.modelSteps || task.skillCalls >= AGENT_LIMITS.skillCalls)
                        throw new AgentProtocolError('Task limit');
                    task.modelSteps++;
                    const input = JSON.stringify({ goal: task.goal, activeTodoAvailable: !!this.session.activeTodo, trace: task.trace,
                        pending: state.kind === 'choice' ? { ...state, ...task.pendingChoice.result } : state,
                        ...(repair ? { repair: agentRepairInstruction(state, repair) } : {}), });
                    stage = 'interpret';
                    const raw = await this.model(input, signal);
                    this.registry.context(signal);
                    if (this.task !== task)
                        throw new AgentProtocolError('Task expired');
                    try {
                        const parsed = interpretAgentEnvelope(raw, state);
                        envelope = parsed.envelope;
                        diagnostic.emit('interpret', { step: task.modelSteps, state: state.kind, interpretationKind: envelope.kind, ...(parsed.recoveredFrom ? { recoveredFrom: parsed.recoveredFrom } : {}), ...(envelope.kind === 'skill_call' ? { skill: envelope.skill, ...Object.fromEntries(Object.entries(envelope.arguments).filter(([key]) => ['reference', 'todoRef', 'lane', 'member', 'tag', 'title'].includes(key))) } : {}) });
                        break;
                    }
                    catch (error) {
                        if (!(error instanceof AgentProtocolError) || attempt === 1)
                            throw error;
                        repair = error.message;
                    }
                }
                if (!envelope)
                    throw new AgentProtocolError('Missing envelope');
                this.append(task, { agent: envelope });
                // Confirmation and clarification are exclusive; the next model request must see exactly one pending.kind.
                task.clarification = envelope.kind === 'ask_user';
                if (task.clarification)
                    task.confirmation = false;
                if (envelope.kind === 'confirm')
                    return this.confirm(signal);
                if (envelope.kind === 'decline' || envelope.kind === 'cancel')
                    return this.cancel();
                if (envelope.kind === 'ask_user') {
                    diagnostic.emit('resolve', { phase: 'initial', result: 'question', choiceCount: task.pendingChoice?.result.choices.length ?? 0 });
                    const pending = task.pendingChoice?.result;
                    return { phase: 'question', text: pending ? renderAgentSkillResult(pending) : envelope.text,
                        ...(pending ? { choices: pending.choices.map(choice => ({ id: choice.handle, label: `${choice.number ? `#${choice.number} · ` : ''}${choice.label}${choice.lane ? ` · ${choice.lane}` : ''}` })) } : {}) };
                }
                if (envelope.kind === 'clarify_skill') {
                    task.confirmation = false;
                    task.clarification = false;
                    task.pendingSkillClarification = Object.freeze({ ...envelope, arguments: Object.freeze({ ...envelope.arguments }) });
                    diagnostic.emit('resolve', { phase: 'initial', result: 'question', choiceCount: 0, clarificationKind: 'skill_argument', skill: envelope.skill, missing: envelope.missing });
                    return { phase: 'question', text: envelope.text };
                }
                if (envelope.kind === 'finish') {
                    if (task.pendingChoice)
                        throw new AgentProtocolError('Choice unresolved');
                    if (task.proposals.count) {
                        stage = 'confirmation_preflight';
                        await task.proposals.preflight(this.registry, task, signal, 'confirmation_preflight');
                        task.confirmation = true;
                        task.clarification = false;
                        diagnostic.emit('confirmation', { phase: 'initial', required: true, ...task.proposals.diagnosticSummary() });
                        return { phase: 'confirmation', text: `${task.proposals.summaries().join('; ')}?`, speechText: task.proposals.confirmationSpeech(), danger: task.proposals.danger };
                    }
                    const text = task.results.filter(result => result.status !== 'choices').map(renderAgentSkillResult).join('; ') || voiceText('voice.agent.noChanges', 'No changes needed.');
                    this.finish(task, 0);
                    return { phase: 'success', text };
                }
                if (envelope.kind === 'skill_call') {
                    task.skillCalls++;
                    // A correction/addition invalidates the old confirmation before any new work.
                    task.confirmation = false;
                    stage = 'resolve';
                    const outcome = await this.registry.run(envelope, task, signal);
                    task.pendingSkillClarification = null;
                    this.registry.context(signal);
                    if (this.task !== task)
                        throw new AgentProtocolError('Task expired');
                    if (outcome.prepared)
                        diagnostic.command(outcome.prepared.command, 'initial');
                    else if (outcome.result.status !== 'opened')
                        diagnostic.emit('resolve', { phase: 'initial', skill: envelope.skill, result: outcome.result.status, ...('number' in outcome.result ? { localId: outcome.result.number } : {}), ...(outcome.result.status === 'choices' ? { choiceCount: outcome.result.choices.length } : {}) });
                    if (outcome.prepared) {
                        const ref = task.proposals.add(outcome.prepared);
                        if (outcome.result.status === 'prepared')
                            outcome.result.proposalRef = ref;
                    }
                    if (JSON.stringify(outcome.result).length > AGENT_LIMITS.resultText)
                        throw new AgentProtocolError('Result limit');
                    if (['stale', 'denied', 'invalid', 'not_found'].includes(outcome.result.status)) {
                        diagnostic.emit('failure', { source: 'resolve', reason: outcome.result.status });
                        diagnostic.end('resolution_failure', { code: outcome.result.status });
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
                }
            }
        }
        catch (error) {
            diagnostic.emit(stage === 'interpret' ? 'interpret' : 'failure', { result: 'failure', source: stage, reason: error instanceof AgentProtocolError ? error.message : stage + '_exception' });
            diagnostic.end(stage + '_failure');
            this.invalidate();
            return { phase: 'error', text: agentSafeFailure() };
        }
    }
}
