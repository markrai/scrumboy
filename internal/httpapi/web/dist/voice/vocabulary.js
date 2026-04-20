import { normalizeLookup } from './normalize.js';
export const ENTITY_ALIASES = new Set(["story", "stories", "todo", "todos", "to do", "to dos"]);
export const ENTITY_ALIAS_PATTERN = "(story|stories|todo|todos|to[-\\s]+dos|to[-\\s]+do)";
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
const YES_ALIASES = new Set(["yes", "yeah", "yep"]);
const NO_ALIASES = new Set(["no", "nope", "nah"]);
const CANCEL_ALIASES = new Set(["cancel", "stop"]);
export function normalizeEntityAlias(input) {
    return ENTITY_ALIASES.has(normalizeLookup(input)) ? "todo" : null;
}
export function normalizeConfirmationResponse(input) {
    const normalized = normalizeLookup(input);
    if (YES_ALIASES.has(normalized))
        return "yes";
    if (NO_ALIASES.has(normalized))
        return "no";
    if (CANCEL_ALIASES.has(normalized))
        return "cancel";
    return null;
}
