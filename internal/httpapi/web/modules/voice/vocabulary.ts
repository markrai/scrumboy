import { normalizeLookup, normalizeVoiceReviewUtterance } from './normalize.js';

export type VoiceConfirmation = "yes" | "no" | "cancel";
export type VoiceReviewDecision = "confirm" | "cancel" | "unknown";
export type VoiceDisambiguationChoice = "option_1" | "option_2" | "option_3";

export const ENTITY_ALIASES = new Set(["story", "stories", "todo", "todos", "to do", "to dos"]);

export const ENTITY_ALIAS_PATTERN = "(?:story|stories|todo|todos|to[-\\s]+dos|to[-\\s]+do)";

export const BUILTIN_STATUS_ALIASES: Array<[string, string]> = [
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
] as const);

const NO_REVIEW_PHRASES = Object.freeze(["no", "nope", "nah", "no thanks", "not now"] as const);
const CANCEL_REVIEW_PHRASES = Object.freeze([
  "cancel", "stop", "never mind", "nevermind", "don't", "do not", "don't do it", "do not do it",
  "forget it", "cancel that", "please cancel", "please stop", "abort", "decline",
] as const);
export const VOICE_REVIEW_CANCEL_PHRASES = Object.freeze([...NO_REVIEW_PHRASES, ...CANCEL_REVIEW_PHRASES] as const);

const YES_ALIASES = new Set<string>(VOICE_REVIEW_CONFIRM_PHRASES);
const NO_ALIASES = new Set<string>(NO_REVIEW_PHRASES);
const CANCEL_ALIASES = new Set<string>(CANCEL_REVIEW_PHRASES);
const DISAMBIGUATION_ALIASES = new Map<string, VoiceDisambiguationChoice>([
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

export function normalizeEntityAlias(input: string): "todo" | null {
  return ENTITY_ALIASES.has(normalizeLookup(input)) ? "todo" : null;
}

export function normalizeConfirmationResponse(input: string): VoiceConfirmation | null {
  const normalized = normalizeVoiceReviewUtterance(input);
  if (YES_ALIASES.has(normalized)) return "yes";
  if (NO_ALIASES.has(normalized)) return "no";
  if (CANCEL_ALIASES.has(normalized)) return "cancel";
  return null;
}

/** Side-effect-free whole-utterance decision shared by review surfaces. */
export function classifyVoiceReviewDecision(input: string): VoiceReviewDecision {
  const normalized = normalizeVoiceReviewUtterance(input);
  if (YES_ALIASES.has(normalized)) return "confirm";
  if (NO_ALIASES.has(normalized) || CANCEL_ALIASES.has(normalized)) return "cancel";
  return "unknown";
}

export function isBuiltinStatusPhrase(input: string): boolean {
  const normalized = normalizeLookup(input);
  return BUILTIN_STATUS_ALIASES.some(([alias]) => normalizeLookup(alias) === normalized);
}

export function normalizeDisambiguationChoice(input: string): VoiceDisambiguationChoice | null {
  return DISAMBIGUATION_ALIASES.get(normalizeLookup(input)) ?? null;
}
