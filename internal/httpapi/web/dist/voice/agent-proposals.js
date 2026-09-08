import { classifyVoiceCommandBatchSafety } from './command-safety.js';
import { AGENT_LIMITS, AgentProtocolError } from './agent-protocol.js';
import { SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS } from '../platform/speech-output.js';
import { voiceText } from './i18n.js';
function affectedField(ir) {
    if (ir.intent.includes('notes'))
        return 'notes';
    if (ir.intent.includes('tag'))
        return 'tags';
    if (ir.intent.includes('assign'))
        return 'assignee';
    return ir.intent;
}
export class VoiceAgentProposalStore {
    constructor() {
        this.proposals = [];
        this.consumed = false;
    }
    get count() { return this.proposals.length; }
    summaries() { return this.proposals.map(proposal => proposal.command.summary); }
    /** All effects are represented, or speech confirmation is unavailable. Never truncate a batch. */
    confirmationSpeech() {
        const summaries = this.proposals.map(({ command }) => {
            const ir = command.ir;
            if ((ir.intent === 'todos.append_notes' || ir.intent === 'todos.replace_notes') && ir.entities.notes.length > 160) {
                return ir.intent === 'todos.append_notes'
                    ? voiceText('voice.prompt.appendNotesLong', 'Add the dictated text to the notes of {title}?', { title: command.storyTitle })
                    : voiceText('voice.prompt.replaceNotesLong', 'Replace the notes of {title} with the dictated text?', { title: command.storyTitle });
            }
            return command.summary;
        });
        const text = `${summaries.join('; ')}?`;
        return summaries.length > 0 && text.length <= SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS ? text : null;
    }
    get danger() { return this.proposals.some(proposal => proposal.command.danger); }
    get safetyReason() { return classifyVoiceCommandBatchSafety(this.proposals.map(proposal => proposal.command.ir)).reason; }
    diagnosticSummary() {
        return {
            proposalCount: this.count,
            commandIntents: this.proposals.map(proposal => proposal.command.ir.intent),
            danger: this.danger,
            reason: this.safetyReason,
            summary: this.summaries().join('; '),
        };
    }
    add(value) {
        if (this.consumed || this.count >= AGENT_LIMITS.proposals)
            throw new AgentProtocolError('Proposal limit');
        const ir = value.command.ir;
        for (const prior of this.proposals) {
            const other = prior.command.ir;
            if (ir.intent === 'todos.create' || other.intent === 'todos.create')
                throw new AgentProtocolError('Create dependencies require a separate task');
            if ('localId' in ir.entities && 'localId' in other.entities && ir.entities.localId === other.entities.localId
                && (ir.intent === 'todos.delete' || other.intent === 'todos.delete' || affectedField(ir) === affectedField(other)))
                throw new AgentProtocolError('Conflicting proposal; cancel and start again');
        }
        // No references to caller-owned objects survive preparation.
        const copy = JSON.parse(JSON.stringify(value));
        Object.freeze(copy.command.ir.entities);
        Object.freeze(copy.command.ir);
        Object.freeze(copy.command);
        Object.freeze(copy.call.arguments);
        Object.freeze(copy.call);
        Object.freeze(copy);
        this.proposals.push(copy);
        return `proposal_${this.count}`;
    }
    async preflight(registry, task, signal, phase = 'initial') {
        if (this.consumed || !this.count)
            throw new AgentProtocolError('No pending proposals');
        await registry.options.refreshBoard();
        registry.context(signal);
        const fresh = [];
        for (const proposal of this.proposals)
            fresh.push(await registry.preflight(proposal, task, signal, phase));
        return fresh;
    }
    async confirm(registry, task, signal) {
        // All re-resolution, permission, validation and precondition checks precede the FIRST mutation.
        const fresh = await this.preflight(registry, task, signal, 'confirm_revalidation');
        task.diagnostic?.emit('confirmation', { phase: 'confirm_revalidation', result: 'accepted', ...this.diagnosticSummary() });
        this.consumed = true;
        const result = { succeeded: [], failed: null, unattempted: [], refreshFailed: false };
        for (let index = 0; index < fresh.length; index++) {
            const proposal = fresh[index];
            try {
                task.diagnostic?.emit('execute', { result: 'started', proposal: index + 1, commandIntent: proposal.command.ir.intent });
                await registry.commit(proposal, signal);
                task.diagnostic?.emit('execute', { result: 'success', proposal: index + 1, commandIntent: proposal.command.ir.intent });
                result.succeeded.push(proposal.command.summary);
                const ir = proposal.command.ir;
                if ('localId' in ir.entities && ir.intent !== 'todos.delete') {
                    const args = proposal.call.arguments;
                    if ('todoRef' in args && args.todoRef)
                        task.session.activeTodo = task.handles.get(args.todoRef, 'todo');
                }
                else if (ir.intent === 'todos.delete' && task.session.activeTodo?.localId === ir.entities.localId)
                    task.session.activeTodo = null;
            }
            catch {
                task.diagnostic?.emit('execute', { result: 'failure', reason: signal.aborted ? 'ownership_lost' : 'execution_exception', proposal: index + 1 });
                result.failed = proposal.command.summary;
                result.unattempted = fresh.slice(index + 1).map(proposal => proposal.command.summary);
                break;
            }
        }
        if (result.succeeded.length || result.failed) {
            try {
                await registry.options.refreshBoard();
            }
            catch {
                result.refreshFailed = true;
            }
        }
        return result;
    }
    clear() { this.proposals = []; this.consumed = true; }
}
