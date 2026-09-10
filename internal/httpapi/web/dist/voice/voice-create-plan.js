export const VOICE_CREATE_LIMITS = Object.freeze({ output: 8192, transcript: 2000, titleBytes: 200, reference: 200, tags: 5, notes: 1000 });
export class VoiceCreatePlanError extends Error {
    constructor(code, details) {
        super(code);
        this.code = code;
        this.details = details;
    }
}
export function utf8Length(text) { return new TextEncoder().encode(text).length; }
function object(value) {
    if (!value || typeof value !== 'object' || Array.isArray(value))
        throw new VoiceCreatePlanError('not_object');
    return value;
}
function keys(value, allowed, required) {
    const unexpectedFields = Object.keys(value).filter(key => !allowed.includes(key));
    if (unexpectedFields.length)
        throw new VoiceCreatePlanError('unknown_fields', { unexpectedFields: unexpectedFields.slice(0, 8) });
    if (required.some(key => !(key in value)))
        throw new VoiceCreatePlanError('missing_required_field');
}
function bounded(value, max, code) {
    if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value))
        throw new VoiceCreatePlanError(code);
}
function unwrapVoiceCreateJsonFence(raw) {
    const trimmed = raw.trim();
    const fenced = /^```(?:json)?\r?\n([\s\S]*?)\r?\n```$/i.exec(trimmed);
    return fenced ? fenced[1].trim() : raw;
}
export function parseVoiceCreatePlan(raw) {
    if (typeof raw !== 'string' || raw.length > VOICE_CREATE_LIMITS.output)
        throw new VoiceCreatePlanError('output_too_large');
    const payload = unwrapVoiceCreateJsonFence(raw);
    let value;
    if (/^\s*\{/.test(payload) && !/\}\s*$/.test(payload))
        throw new VoiceCreatePlanError('invalid_json');
    if (!/^\s*\{[\s\S]*\}\s*$/.test(payload))
        throw new VoiceCreatePlanError('surrounding_prose');
    try {
        value = object(JSON.parse(payload));
    }
    catch {
        throw new VoiceCreatePlanError('invalid_json');
    }
    if (value.version !== 1)
        throw new VoiceCreatePlanError('wrong_version');
    if (value.kind === 'not_create') {
        keys(value, ['version', 'kind'], ['version', 'kind']);
        return Object.freeze({ version: 1, kind: 'not_create' });
    }
    if (value.kind !== 'create')
        throw new VoiceCreatePlanError('invalid_kind');
    keys(value, ['version', 'kind', 'title', 'lane', 'assignee', 'tags', 'notes', 'unhandled'], ['version', 'kind']);
    for (const field of ['title', 'lane', 'assignee', 'notes']) {
        if (field in value)
            bounded(value[field], field === 'notes' ? VOICE_CREATE_LIMITS.notes : VOICE_CREATE_LIMITS.reference, field === 'title' ? 'invalid_title' : field === 'lane' ? 'invalid_lane' : field === 'assignee' ? 'invalid_assignee' : 'invalid_notes');
    }
    if ('title' in value && utf8Length(value.title) > VOICE_CREATE_LIMITS.titleBytes)
        throw new VoiceCreatePlanError('invalid_title');
    if ('tags' in value) {
        if (!Array.isArray(value.tags) || value.tags.length > VOICE_CREATE_LIMITS.tags)
            throw new VoiceCreatePlanError('invalid_tags');
        value.tags.forEach(tag => bounded(tag, VOICE_CREATE_LIMITS.reference, 'invalid_tags'));
        Object.freeze(value.tags);
    }
    if ('unhandled' in value) {
        if (!Array.isArray(value.unhandled) || value.unhandled.length > 5)
            throw new VoiceCreatePlanError('invalid_unhandled');
        value.unhandled.forEach(item => {
            const issue = object(item);
            try {
                keys(issue, ['text', 'reason'], ['text', 'reason']);
                bounded(issue.text, 500, 'invalid_unhandled');
            }
            catch (error) {
                if (error instanceof VoiceCreatePlanError)
                    throw new VoiceCreatePlanError('invalid_unhandled', error.details);
                throw error;
            }
            if (issue.reason !== 'unsupported' && issue.reason !== 'unclear')
                throw new VoiceCreatePlanError('invalid_unhandled');
            Object.freeze(issue);
        });
        Object.freeze(value.unhandled);
    }
    return Object.freeze(value);
}
/** Also used at preparation, so a typed test double cannot bypass parser validation. */
export function executableCreatePlan(plan) {
    const checked = parseVoiceCreatePlan(JSON.stringify(plan));
    if (checked.kind !== 'create' || checked.unhandled?.length)
        throw new VoiceCreatePlanError('incomplete_request');
    if (!checked.title)
        throw new VoiceCreatePlanError('missing_title');
    return checked;
}
/** Defense in depth for obvious unsupported clauses, not a second semantic parser.
 * Literal authored fields are excluded so e.g. a title "Schedule Tuesday" stays literal.
 * General extraction completeness remains a physical-model evaluation responsibility.
 */
export function guardCreateRequest(plan, transcript) {
    let remainder = transcript.toLocaleLowerCase('en-US');
    for (const literal of [plan.title, plan.notes]) {
        if (literal)
            remainder = remainder.replace(literal.toLocaleLowerCase('en-US'), ' ');
    }
    if (/\b(schedule|remind|deadline|due date|priority|sprint|estimate)\b|\b(?:in|to) (?:the )?(?:project|board)\b|\b(?:two|three|2|3) (?:stories|cards|todos|tasks|items)\b|\b(?:and|then|also) (?:create|make|delete|move|open|rename)\b/i.test(remainder)) {
        throw new VoiceCreatePlanError('incomplete_request');
    }
}
