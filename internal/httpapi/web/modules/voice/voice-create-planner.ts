import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { parseVoiceCreatePlan, guardCreateRequest, VoiceCreatePlanError, VOICE_CREATE_LIMITS, type VoiceCreatePlanResult } from './voice-create-plan.js';

export const VOICE_CREATE_PLANNER_VERSION = 'voice-create-plan-v1';
export const VOICE_CREATE_PROMPT = `Interpret the COMPLETE user utterance. Extract one requested new story/card/todo/task/item; these words are synonyms.
Return exactly one JSON object, no markdown or prose. Never converse, ask questions, select tools, or emit finish/confirm/decline/cancel actions.
For one create: {"version":1,"kind":"create","title":"literal title","lane":"specified lane name","assignee":"specified person name or email","tags":["specified tag"],"notes":"literal authored notes"}.
Only version and kind are always required. Omit every unspecified field. Do not invent values, defaults, project, IDs, keys, permissions or summaries.
Preserve literal title content after called/named/titled/call it. Done, Backlog and Settings may be titles. Extract lane/status/column only when specified as a destination, not from a title. A request to use the usual place for new cards does not specify a lane: omit lane.
Extract a singular assignee only if specified. Extract tags only if specified. Preserve authored notes. Ignore conversational filler.
Include every material unsupported or unclear request in "unhandled":[{"text":"the unsupported request","reason":"unsupported"}] (or reason "unclear"). Scheduling, cross-project requests, multiple creates, updates to existing todos and multiple assignees are unsupported. Never silently drop them.
If this is not a create request, return {"version":1,"kind":"not_create"} only.
Examples:
Create Big Man. -> {"version":1,"kind":"create","title":"Big Man"}
Create a story called Done. -> {"version":1,"kind":"create","title":"Done"}
Hey, make a card. Call it Settings, put it in Backlog, give it to Mark, tag urgent, and add a note saying Call tomorrow. -> {"version":1,"kind":"create","title":"Settings","lane":"Backlog","assignee":"Mark","tags":["urgent"],"notes":"Call tomorrow"}
Create Fred and schedule it for Tuesday. -> {"version":1,"kind":"create","title":"Fred","unhandled":[{"text":"schedule it for Tuesday","reason":"unsupported"}]}
Create a story. -> {"version":1,"kind":"create"}`;

export type VoiceCreatePlanner = (transcript: string, signal: AbortSignal) => Promise<VoiceCreatePlanResult>;
let nextRequest = 0;
/** Only generation is injected. No query, navigation, preparation or commit ports. No retries. */
export function createVoiceCreatePlanner(capability: Pick<LocalTextGenerationCapability, 'generate'>): VoiceCreatePlanner {
  return async (transcript, signal) => {
    if (signal.aborted) throw new VoiceCreatePlanError('stale_context');
    if (!transcript.trim() || transcript.length > VOICE_CREATE_LIMITS.transcript) throw new VoiceCreatePlanError('invalid_plan');
    const requestId = `${VOICE_CREATE_PLANNER_VERSION}-${++nextRequest}`;
    const result = await capability.generate({ requestId, input: transcript, instructions: VOICE_CREATE_PROMPT, maximumOutputTokens: 256, signal });
    if (signal.aborted || result.requestId !== requestId) throw new VoiceCreatePlanError('stale_context');
    let plan: VoiceCreatePlanResult;
    try {
      plan = parseVoiceCreatePlan(result.text);
    } catch (error) {
      if (error instanceof VoiceCreatePlanError) {
        throw new VoiceCreatePlanError(error.code, { ...error.details, outputLength: result.text.length });
      }
      throw error;
    }
    if (plan.kind === 'create') guardCreateRequest(plan, transcript);
    return plan;
  };
}
