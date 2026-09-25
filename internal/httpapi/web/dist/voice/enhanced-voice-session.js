import { agentSafeFailure } from './agent-loop.js';
import { normalizeLookup } from './normalize.js';
const CREATE_OBJECTS = new Set(['story', 'card', 'todo', 'task', 'item']);
const PREFIX_WORDS = new Set(['uh', 'um', 'hey', 'please']);
const PREFIX_PAIRS = new Set(['can you', 'could you', 'would you']);
/**
 * Classifies only the outer shape of a fresh request. The Create planner remains
 * responsible for all title, lane, member, tag, note, and unsupported-request
 * semantics once this returns true.
 */
export function isVoiceCreateCandidate(transcript) {
    const tokens = normalizeLookup(transcript).split(' ').filter(Boolean);
    let index = 0;
    while (index < tokens.length) {
        if (PREFIX_WORDS.has(tokens[index])) {
            index += 1;
            continue;
        }
        const pair = `${tokens[index]} ${tokens[index + 1] ?? ''}`;
        if (PREFIX_PAIRS.has(pair)) {
            index += 2;
            continue;
        }
        break;
    }
    const verb = tokens[index];
    if (!verb)
        return false;
    index += 1;
    if (verb === 'create') {
        const article = tokens[index];
        const object = article === 'a' || article === 'an' || article === 'the' ? tokens[index + 1] : article;
        if (object && (object === 'tag' || object === 'note' || object === 'notes'))
            return false;
        if (object && CREATE_OBJECTS.has(object))
            return true;
        // “Create Fred”, “Create Fred and …”, and even a bare “Create” are sent to
        // the hardened planner, which rejects missing/unsupported details safely.
        return true;
    }
    if (verb === 'add') {
        // “Add a story/card/todo/task/item” is a fresh Todo-like-object request.
        // Require the object noun (and the natural article) so field mutations
        // such as “add a tag”, “add notes”, or “add a note” remain Agent work.
        const article = tokens[index];
        const object = article === 'a' || article === 'an' || article === 'the' ? tokens[index + 1] : undefined;
        return !!object && CREATE_OBJECTS.has(object);
    }
    if (verb !== 'make')
        return false;
    const article = tokens[index];
    const object = article === 'a' || article === 'an' || article === 'the' ? tokens[index + 1] : undefined;
    return !!object && CREATE_OBJECTS.has(object);
}
export class EnhancedVoiceSession {
    constructor(options) {
        this.options = options;
        this.owner = null;
        this.freshTrace = null;
    }
    get currentOwner() { return this.owner; }
    get pending() {
        return this.owner === 'create' ? this.options.create.pending
            : this.owner === 'agent' ? this.options.agent.pending
                : false;
    }
    get confirmationPending() {
        return this.owner === 'create' ? this.options.create.confirmationPending
            : this.owner === 'agent' ? this.options.agent.confirmationPending
                : false;
    }
    get captureContext() {
        if (this.owner === 'create')
            return this.options.create.captureContext;
        // Agent follow-ups deliberately use the robust Create-compatible capture
        // policy, but have no Create-specific semantic context.
        if (this.owner === 'agent')
            return undefined;
        return 'initial_enhanced_voiceflow_capture';
    }
    get retainsContext() { return !!this.options.agent.session.activeTodo; }
    trace() {
        if (this.owner === 'create')
            return this.options.create.trace();
        if (this.owner === 'agent')
            return this.options.agent.trace();
        if (!this.freshTrace || this.freshTrace.ended)
            this.freshTrace = this.options.create.trace();
        return this.freshTrace;
    }
    adopt(owner) {
        // Capture events are emitted before the first transcript identifies an
        // owner. Reuse that provisional trace so the whole interaction remains a
        // single diagnostic operation after routing.
        const trace = this.freshTrace ?? this.trace();
        if (owner === 'create')
            this.options.create.adoptTrace(trace);
        else
            this.options.agent.adoptTrace(trace);
    }
    clearOwner(owner, result) {
        if (result.phase !== 'success' && result.phase !== 'error')
            return;
        // A successful Create is deliberately not made the Agent's active Todo.
        // This prevents a later pronoun from silently targeting stale Agent state.
        if (owner === 'create' && result.phase === 'success')
            this.options.agent.session.activeTodo = null;
        this.owner = null;
        this.freshTrace = null;
    }
    endTrace(reason, fields = {}) { this.trace().end(reason, fields); }
    cancelTrace(reason, retained = false) { this.trace().emit('cancel', { reason, interactionRetained: retained }); if (!retained)
        this.trace().end(reason); }
    context(signal) {
        return this.owner === 'create' ? this.options.create.context(signal) : this.options.agent.registry.context(signal);
    }
    async submit(transcript, signal) {
        if (!this.owner || !this.pending) {
            this.owner = isVoiceCreateCandidate(transcript) ? 'create' : 'agent';
            this.adopt(this.owner);
        }
        const owner = this.owner;
        const result = owner === 'create'
            ? await this.options.create.submit(transcript, signal)
            : await this.options.agent.submit(transcript, signal);
        this.clearOwner(owner, result);
        return result;
    }
    async confirm(signal) {
        const owner = this.owner;
        if (!owner)
            return { phase: 'error', text: agentSafeFailure() };
        const result = owner === 'create'
            ? await this.options.create.confirm(signal)
            : await this.options.agent.confirm(signal);
        this.clearOwner(owner, result);
        return result;
    }
    cancel() {
        const owner = this.owner;
        const result = owner === 'create' ? this.options.create.cancel() : this.options.agent.cancel();
        if (owner)
            this.clearOwner(owner, result);
        return result;
    }
    async choose(index, signal) {
        const owner = this.owner;
        if (!owner)
            return { phase: 'error', text: agentSafeFailure() };
        const result = owner === 'create'
            ? await this.options.create.choose(index, signal)
            : await this.options.agent.choose(index, signal);
        this.clearOwner(owner, result);
        return result;
    }
    setKeepListening(enabled) {
        this.options.create.setKeepListening(enabled);
        this.options.agent.setKeepListening(enabled);
    }
    invalidate() {
        this.options.create.invalidate();
        this.options.agent.invalidate();
        this.owner = null;
        this.freshTrace = null;
    }
}
