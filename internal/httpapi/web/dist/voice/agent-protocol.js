export const AGENT_LIMITS = Object.freeze({ modelSteps: 8, skillCalls: 6, proposals: 4, choices: 5, resultText: 2048, ask: 320, trace: 16, utterance: 2000 });
export const SKILL_NAMES = Object.freeze([
    'todos.resolve', 'todos.open', 'todos.inspect', 'todos.create', 'todos.move', 'todos.rename',
    'todos.append_notes', 'todos.replace_notes', 'todos.assign', 'todos.unassign', 'todos.add_tag',
    'todos.remove_tag', 'todos.delete', 'analytics.count_completed',
]);
export class AgentProtocolError extends Error {
}
function invalid(reason) { throw new AgentProtocolError(reason); }
function object(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value))
        invalid('Expected one object');
    return value;
}
function keys(value, allowed, required = allowed) {
    if (Object.keys(value).some(key => !allowed.includes(key)) || required.some(key => !(key in value)))
        invalid('Unexpected or missing fields');
}
function text(value, max) {
    if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value))
        invalid('Invalid bounded string');
}
export function parseAgentEnvelope(raw, confirmation) {
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
    if (envelope.kind === 'ask_user') {
        keys(envelope, ['kind', 'text']);
        text(envelope.text, AGENT_LIMITS.ask);
    }
    else if (['finish', 'confirm', 'decline', 'cancel'].includes(String(envelope.kind))) {
        keys(envelope, ['kind']);
        if (envelope.kind !== 'finish' && !confirmation)
            invalid('Confirmation is not pending');
    }
    else if (envelope.kind === 'skill_call') {
        keys(envelope, ['kind', 'skill', 'arguments']);
        if (!SKILL_NAMES.includes(envelope.skill))
            invalid('Unknown skill');
        const args = object(envelope.arguments);
        const skill = envelope.skill;
        if (skill === 'analytics.count_completed') {
            keys(args, ['range']);
            if (args.range !== 'this_week')
                invalid('Invalid range');
        }
        else if (skill === 'todos.create') {
            keys(args, ['title', 'lane'], ['title']);
            text(args.title, 200);
            if ('lane' in args)
                text(args.lane, 200);
        }
        else {
            if (('reference' in args) === ('todoRef' in args))
                invalid('Supply reference OR todoRef');
            const target = 'reference' in args ? 'reference' : 'todoRef';
            if (skill === 'todos.resolve' && target !== 'reference')
                invalid('Resolve requires reference');
            text(args[target], target === 'todoRef' ? 80 : 200);
            const extra = skill === 'todos.move' ? 'lane' : skill === 'todos.rename' ? 'title'
                : ['todos.append_notes', 'todos.replace_notes'].includes(skill) ? 'text'
                    : ['todos.assign', 'todos.unassign'].includes(skill) ? 'member'
                        : ['todos.add_tag', 'todos.remove_tag'].includes(skill) ? 'tag' : skill === 'todos.inspect' ? 'fields' : null;
            const required = extra && !['fields'].includes(extra) && skill !== 'todos.unassign' ? [target, extra] : [target];
            keys(args, extra ? [target, extra] : [target], required);
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
    else
        invalid('Unknown envelope kind');
    return value;
}
