import { normalizeLookup, normalizeVoiceReviewUtterance } from './normalize.js';
export const ENTITY_ALIASES = new Set(["story", "stories", "todo", "todos", "to do", "to dos"]);
export const ENTITY_ALIAS_PATTERN = "(?:story|stories|todo|todos|to[-\\s]+dos|to[-\\s]+do)";
export const BUILTIN_STATUS_ALIASES = [
    ["backlog", "backlog"],
    ["not started", "not_started"],
    ["in progress", "doing"],
    ["doing", "doing"],
    ["testing", "testing"],
    ["done", "done"],
    ["to do", "todo"],
    ["todo", "todo"],
];
export const VOICE_REVIEW_CONFIRM_PHRASES = Object.freeze([
    "yes", "yeah", "yep", "yup", "sure", "okay", "ok", "confirm", "confirmed",
    "go ahead", "go for it", "proceed", "do it", "please do", "yes please", "sure thing",
    "sounds good", "looks good", "that's fine", "that is fine", "that's good", "that is good",
    "absolutely", "correct", "all right", "alright", "approve", "approved", "please proceed",
]);
const NO_REVIEW_PHRASES = Object.freeze(["no", "nope", "nah", "no thanks", "not now"]);
const CANCEL_REVIEW_PHRASES = Object.freeze([
    "cancel", "stop", "never mind", "nevermind", "don't", "do not", "don't do it", "do not do it",
    "forget it", "cancel that", "please cancel", "please stop", "abort", "decline",
]);
export const VOICE_REVIEW_CANCEL_PHRASES = Object.freeze([...NO_REVIEW_PHRASES, ...CANCEL_REVIEW_PHRASES]);
const YES_ALIASES = new Set(VOICE_REVIEW_CONFIRM_PHRASES);
const NO_ALIASES = new Set(NO_REVIEW_PHRASES);
const CANCEL_ALIASES = new Set(CANCEL_REVIEW_PHRASES);
const DECISION_PHRASES = Object.freeze([
    ...VOICE_REVIEW_CONFIRM_PHRASES.map(phrase => ({ signal: 'yes', words: normalizeVoiceReviewUtterance(phrase).split(' ') })),
    ...NO_REVIEW_PHRASES.map(phrase => ({ signal: 'no', words: normalizeVoiceReviewUtterance(phrase).split(' ') })),
    ...CANCEL_REVIEW_PHRASES.map(phrase => ({ signal: 'cancel', words: normalizeVoiceReviewUtterance(phrase).split(' ') })),
]);
const DISAMBIGUATION_ALIASES = new Map([
    ["first one", "option_1"],
    ["number one", "option_1"],
    ["option one", "option_1"],
    ["one", "option_1"],
    ["1", "option_1"],
    ["second one", "option_2"],
    ["number two", "option_2"],
    ["option two", "option_2"],
    ["two", "option_2"],
    ["2", "option_2"],
    ["third one", "option_3"],
    ["number three", "option_3"],
    ["option three", "option_3"],
    ["three", "option_3"],
    ["3", "option_3"],
]);
export function normalizeEntityAlias(input) {
    return ENTITY_ALIASES.has(normalizeLookup(input)) ? "todo" : null;
}
export function normalizeConfirmationResponse(input) {
    const normalized = normalizeVoiceReviewUtterance(input);
    if (YES_ALIASES.has(normalized))
        return "yes";
    if (NO_ALIASES.has(normalized))
        return "no";
    if (CANCEL_ALIASES.has(normalized))
        return "cancel";
    return null;
}
/**
 * Side-effect-free bounded phrase composition for Create review questions.
 * Every input word must belong to an allowlisted phrase. Longest matches keep
 * nested phrases such as "do it" from conflicting with "don't do it".
 */
export function classifyVoiceBinaryDecision(input) {
    const normalized = normalizeVoiceReviewUtterance(input);
    if (!normalized)
        return 'unknown';
    const words = normalized.split(' ');
    const signals = new Set();
    let uncovered = false;
    for (let index = 0; index < words.length;) {
        const matches = DECISION_PHRASES.filter(({ words: phrase }) => phrase.every((word, offset) => words[index + offset] === word));
        const length = matches.reduce((maximum, match) => Math.max(maximum, match.words.length), 0);
        if (length === 0) {
            uncovered = true;
            index += 1;
            continue;
        }
        matches.filter(match => match.words.length === length).forEach(match => signals.add(match.signal));
        index += length;
    }
    if (signals.has('yes') && (signals.has('no') || signals.has('cancel')))
        return 'unknown';
    if (uncovered)
        return 'unknown';
    if (signals.has('yes'))
        return 'yes';
    if (signals.has('cancel'))
        return 'cancel';
    return signals.has('no') ? 'no' : 'unknown';
}
/** Side-effect-free decision shared by Voice Create review surfaces. */
export function classifyVoiceReviewDecision(input) {
    const decision = classifyVoiceBinaryDecision(input);
    return decision === 'yes' ? 'confirm' : decision === 'no' || decision === 'cancel' ? 'cancel' : 'unknown';
}
export function isBuiltinStatusPhrase(input) {
    const normalized = normalizeLookup(input);
    return BUILTIN_STATUS_ALIASES.some(([alias]) => normalizeLookup(alias) === normalized);
}
export function normalizeDisambiguationChoice(input) {
    return DISAMBIGUATION_ALIASES.get(normalizeLookup(input)) ?? null;
}
