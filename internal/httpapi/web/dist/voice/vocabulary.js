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
/** Side-effect-free whole-utterance decision shared by review surfaces. */
export function classifyVoiceReviewDecision(input) {
    const normalized = normalizeVoiceReviewUtterance(input);
    if (YES_ALIASES.has(normalized))
        return "confirm";
    if (NO_ALIASES.has(normalized) || CANCEL_ALIASES.has(normalized))
        return "cancel";
    return "unknown";
}
export function isBuiltinStatusPhrase(input) {
    const normalized = normalizeLookup(input);
    return BUILTIN_STATUS_ALIASES.some(([alias]) => normalizeLookup(alias) === normalized);
}
export function normalizeDisambiguationChoice(input) {
    return DISAMBIGUATION_ALIASES.get(normalizeLookup(input)) ?? null;
}
