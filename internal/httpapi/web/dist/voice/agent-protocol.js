export const AGENT_LIMITS = Object.freeze({ modelSteps: 8, skillCalls: 6, proposals: 4, choices: 5, resultText: 2048, ask: 320, trace: 16, utterance: 2000 });
const ENVELOPE_KINDS = Object.freeze(['skill_call', 'clarify_skill', 'ask_user', 'finish', 'confirm', 'decline', 'cancel']);
export const ALLOWED_ENVELOPE_KINDS = Object.freeze({
    idle: Object.freeze(['skill_call', 'clarify_skill', 'ask_user', 'finish']),
    clarification: Object.freeze(['skill_call', 'clarify_skill', 'ask_user']),
    choice: Object.freeze(['skill_call', 'ask_user']),
    proposals_ready: Object.freeze(['skill_call', 'clarify_skill', 'ask_user', 'finish']),
    confirmation: Object.freeze(['skill_call', 'confirm', 'decline', 'cancel']),
});
export function allowedEnvelopeKinds(state) {
    return ALLOWED_ENVELOPE_KINDS[state.kind] ?? ALLOWED_ENVELOPE_KINDS.idle;
}
export function agentStateGuidance(state) {
    const count = state.proposalCount ?? 0;
    const detail = state.kind === 'clarification' && state.skillClarification
        ? `The user reply fills the retained ${state.skillClarification.missing} argument for ${state.skillClarification.skill}; Scrumboy handles this locally.`
        : state.kind === 'clarification' ? 'The user reply answers the pending question and is ordinary content, such as the lane named Done; it is never a protocol action.'
            : state.kind === 'choice' ? 'A choice is unresolved: repeat the same skill with exactly one offered handle.'
                : state.kind === 'proposals_ready' ? `${count} mutation(s) are prepared and not yet shown. Emit another skill_call while requested work remains, otherwise finish.`
                    : state.kind === 'confirmation' ? `${count} prepared mutation(s) await the user decision.`
                        : 'No work is prepared yet.';
    return `Current state: ${state.kind}. ${detail} Allowed envelope kinds: ${allowedEnvelopeKinds(state).join(', ')}.`;
}
/** Repair carries only application-owned reasons and state, never the rejected model output. */
export function agentRepairInstruction(state, reason) {
    return `Previous response violated protocol: ${reason}. ${agentStateGuidance(state)} Return exactly one valid envelope.`;
}
export const SKILL_NAMES = Object.freeze([
    'todos.resolve', 'todos.open', 'todos.inspect', 'todos.create', 'todos.move', 'todos.rename',
    'todos.append_notes', 'todos.replace_notes', 'todos.assign', 'todos.unassign', 'todos.add_tag',
    'todos.remove_tag', 'todos.delete', 'analytics.count_completed',
]);
const PROTOCOL_DIAGNOSTIC_KEY_LIMIT = 12;
const PROTOCOL_DIAGNOSTIC_KEY_LENGTH = 48;
function safeProtocolIdentifier(value) {
    if (typeof value !== 'string' || !/^[A-Za-z_][A-Za-z0-9_.|-]*$/.test(value))
        return undefined;
    return value.slice(0, PROTOCOL_DIAGNOSTIC_KEY_LENGTH);
}
function safeProtocolKeys(values) {
    const safe = values.map(value => safeProtocolIdentifier(value) ?? '<invalid>').sort();
    return Object.freeze([...new Set(safe)].slice(0, PROTOCOL_DIAGNOSTIC_KEY_LIMIT));
}
function isObjectRecord(value) {
    return !!value && typeof value === 'object' && !Array.isArray(value);
}
function protocolDiagnostic(envelope, scope, details = {}) {
    const diagnostic = { protocolScope: scope };
    const kind = safeProtocolIdentifier(envelope.kind);
    const skill = safeProtocolIdentifier(envelope.skill);
    const topLevelKeys = safeProtocolKeys(Object.keys(envelope));
    const argumentKeys = isObjectRecord(envelope.arguments) ? safeProtocolKeys(Object.keys(envelope.arguments)) : [];
    if (kind)
        diagnostic.protocolEnvelopeKind = kind;
    if (skill)
        diagnostic.protocolSkill = skill;
    if (topLevelKeys.length)
        diagnostic.protocolTopLevelKeys = topLevelKeys;
    if (argumentKeys.length)
        diagnostic.protocolArgumentKeys = argumentKeys;
    for (const [field, values] of [
        ['protocolAllowedKeys', details.allowedKeys],
        ['protocolUnexpectedKeys', details.unexpectedKeys],
        ['protocolMissingKeys', details.missingKeys],
        ['protocolConflictKeys', details.conflictKeys],
    ]) {
        if (values?.length)
            diagnostic[field] = safeProtocolKeys(values);
    }
    return Object.freeze(diagnostic);
}
export class AgentProtocolError extends Error {
    constructor(message, diagnostic = Object.freeze({})) {
        super(message);
        this.diagnostic = diagnostic;
        this.name = 'AgentProtocolError';
    }
}
function invalid(reason, diagnostic) { throw new AgentProtocolError(reason, diagnostic); }
function object(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value))
        invalid('Expected one object');
    return value;
}
function keys(value, allowed, required = allowed, envelope = value, scope = 'envelope') {
    const actual = Object.keys(value);
    const unexpectedKeys = actual.filter(key => !allowed.includes(key));
    const missingKeys = required.filter(key => !(key in value));
    if (unexpectedKeys.length || missingKeys.length) {
        invalid('Unexpected or missing fields', protocolDiagnostic(envelope, scope, { allowedKeys: allowed, unexpectedKeys, missingKeys }));
    }
}
function text(value, max) {
    if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value))
        invalid('Invalid bounded string');
}
function validateSkillCallEnvelope(envelope) {
    keys(envelope, ['kind', 'skill', 'arguments']);
    if (!SKILL_NAMES.includes(envelope.skill))
        invalid('Unknown skill', protocolDiagnostic(envelope, 'envelope'));
    if (!isObjectRecord(envelope.arguments))
        invalid('Expected one object', protocolDiagnostic(envelope, 'arguments'));
    const args = envelope.arguments;
    const skill = envelope.skill;
    if (skill === 'analytics.count_completed') {
        keys(args, ['range'], ['range'], envelope, 'arguments');
        if (args.range !== 'this_week')
            invalid('Invalid range', protocolDiagnostic(envelope, 'arguments'));
    }
    else if (skill === 'todos.create') {
        keys(args, ['title', 'lane'], ['title'], envelope, 'arguments');
        text(args.title, 200);
        if ('lane' in args)
            text(args.lane, 200);
    }
    else {
        const extra = skill === 'todos.move' ? 'lane' : skill === 'todos.rename' ? 'title'
            : ['todos.append_notes', 'todos.replace_notes'].includes(skill) ? 'text'
                : ['todos.assign', 'todos.unassign'].includes(skill) ? 'member'
                    : ['todos.add_tag', 'todos.remove_tag'].includes(skill) ? 'tag' : skill === 'todos.inspect' ? 'fields' : null;
        const allowedTargetKeys = ['reference', 'todoRef', ...(extra ? [extra] : [])];
        const hasReference = 'reference' in args;
        const hasTodoRef = 'todoRef' in args;
        if (hasReference === hasTodoRef) {
            const actual = Object.keys(args);
            invalid('Supply reference OR todoRef', protocolDiagnostic(envelope, 'arguments', {
                allowedKeys: allowedTargetKeys,
                unexpectedKeys: actual.filter(key => !allowedTargetKeys.includes(key)),
                missingKeys: hasReference ? [] : ['reference|todoRef'],
                conflictKeys: hasReference ? ['reference', 'todoRef'] : [],
            }));
        }
        const target = hasReference ? 'reference' : 'todoRef';
        if (skill === 'todos.resolve' && target !== 'reference')
            invalid('Resolve requires reference', protocolDiagnostic(envelope, 'arguments', { allowedKeys: ['reference'], unexpectedKeys: ['todoRef'], missingKeys: ['reference'] }));
        text(args[target], target === 'todoRef' ? 80 : 200);
        const required = extra && !['fields'].includes(extra) && skill !== 'todos.unassign' ? [target, extra] : [target];
        keys(args, extra ? [target, extra] : [target], required, envelope, 'arguments');
        if (extra === 'fields' && extra in args) {
            const fields = args.fields;
            if (!Array.isArray(fields) || !fields.length || fields.length > 5 || new Set(fields).size !== fields.length
                || fields.some(field => !['title', 'lane', 'assignees', 'tags', 'notes'].includes(field)))
                invalid('Invalid inspect fields');
        }
        else if (extra && extra in args)
            text(args[extra], extra === 'text' ? 1000 : 200);
    }
}
function recoverCompleteDeleteClarification(envelope) {
    if (envelope.skill !== 'todos.delete')
        return undefined;
    keys(envelope, ['kind', 'skill', 'arguments', 'missing', 'text']);
    text(envelope.text, AGENT_LIMITS.ask);
    if (!isObjectRecord(envelope.arguments))
        return undefined;
    const args = envelope.arguments;
    const argumentKeys = Object.keys(args);
    if (argumentKeys.length !== 1 || !['reference', 'todoRef'].includes(argumentKeys[0]))
        return undefined;
    const target = argumentKeys[0];
    const value = args[target];
    text(value, target === 'todoRef' ? 80 : 200);
    return {
        kind: 'skill_call', skill: 'todos.delete',
        arguments: target === 'reference' ? { reference: value } : { todoRef: value },
    };
}
function validateSkillClarificationEnvelope(envelope) {
    keys(envelope, ['kind', 'skill', 'arguments', 'missing', 'text']);
    text(envelope.text, AGENT_LIMITS.ask);
    if (!isObjectRecord(envelope.arguments))
        invalid('Expected one object', protocolDiagnostic(envelope, 'clarification_arguments'));
    const args = envelope.arguments;
    if (envelope.skill === 'todos.delete') {
        if (envelope.missing !== 'reference')
            invalid('Invalid missing skill argument', protocolDiagnostic(envelope, 'clarification'));
        keys(args, [], [], envelope, 'clarification_arguments');
        return;
    }
    if (envelope.skill !== 'todos.move')
        invalid('Unsupported skill clarification', protocolDiagnostic(envelope, 'clarification'));
    if (envelope.missing === 'reference') {
        keys(args, ['lane'], ['lane'], envelope, 'clarification_arguments');
        text(args.lane, 200);
        return;
    }
    if (envelope.missing !== 'lane')
        invalid('Invalid missing skill argument', protocolDiagnostic(envelope, 'clarification'));
    if (('reference' in args) === ('todoRef' in args))
        invalid('Supply reference OR todoRef', protocolDiagnostic(envelope, 'clarification_arguments', {
            allowedKeys: ['reference', 'todoRef'],
            missingKeys: 'reference' in args ? [] : ['reference|todoRef'],
            conflictKeys: 'reference' in args ? ['reference', 'todoRef'] : [],
        }));
    const target = 'reference' in args ? 'reference' : 'todoRef';
    keys(args, [target], [target], envelope, 'clarification_arguments');
    text(args[target], target === 'todoRef' ? 80 : 200);
}
/** Completes only the declared missing slot; the result is a normal validated skill call. */
export function completeSkillClarification(clarification, reply) {
    text(reply, 200);
    const value = clarification.missing === 'reference'
        ? { kind: 'skill_call', skill: clarification.skill, arguments: { ...clarification.arguments, reference: reply.trim() } }
        : { kind: 'skill_call', skill: clarification.skill, arguments: { ...clarification.arguments, lane: reply.trim() } };
    validateSkillCallEnvelope(value);
    return value;
}
export function parseAgentEnvelope(raw, state) {
    return interpretAgentEnvelope(raw, state).envelope;
}
export function interpretAgentEnvelope(raw, state) {
    if (typeof raw !== 'string' || raw.length > 8192)
        invalid('Output too large');
    let value;
    // The local provider already tolerates a single surrounding JSON fence.
    let source = raw.trim();
    if (source.startsWith('```json\n') && source.endsWith('\n```'))
        source = source.slice(8, -4);
    else if (source.startsWith('```\n') && source.endsWith('\n```'))
        source = source.slice(4, -4);
    try {
        value = JSON.parse(source);
    }
    catch {
        invalid('Expected strict JSON');
    }
    const envelope = object(value);
    let kind = ENVELOPE_KINDS.find(name => name === envelope.kind);
    if (!kind)
        invalid('Unknown envelope kind', protocolDiagnostic(envelope, 'envelope'));
    let recoveredFrom;
    // Local models conflate "preparation complete" with confirm. Present confirmation; never execute.
    if (state.kind === 'proposals_ready' && kind === 'confirm') {
        keys(envelope, ['kind']);
        recoveredFrom = 'confirm';
        kind = 'finish';
        value = { kind: 'finish' };
    }
    if (!allowedEnvelopeKinds(state).includes(kind)) {
        invalid(`Envelope ${kind} is not allowed in state ${state.kind}`);
    }
    if (kind === 'ask_user') {
        keys(envelope, ['kind', 'text']);
        text(envelope.text, AGENT_LIMITS.ask);
    }
    else if (kind === 'clarify_skill') {
        const recoveredDelete = recoverCompleteDeleteClarification(envelope);
        if (recoveredDelete)
            return { envelope: recoveredDelete, recoveredFrom: 'delete_clarification_with_target' };
        validateSkillClarificationEnvelope(envelope);
    }
    else if (kind === 'finish') {
        // An accompanying human-readable text is bounded, ignored and never renders; it must not discard prepared proposals.
        keys(envelope, ['kind', 'text'], ['kind']);
        if ('text' in envelope && (typeof envelope.text !== 'string' || envelope.text.length > AGENT_LIMITS.ask))
            invalid('Invalid bounded string');
    }
    else if (kind !== 'skill_call') {
        keys(envelope, ['kind']);
    }
    else {
        validateSkillCallEnvelope(envelope);
    }
    return { envelope: value, recoveredFrom };
}
