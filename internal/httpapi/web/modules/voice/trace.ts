import { voiceFlowDiagnostic } from '../platform/voiceflow-diagnostics.js';
import type { ResolvedCommand } from './schema.js';
import type { VoiceCommandInterpretation } from './interpreter.js';
import { classifyVoiceCommandSafety } from './command-safety.js';

export type VoiceFlowTraceStage = 'asr_final' | 'transcript_input' | 'interpret' | 'planner_start' | 'plan' | 'resolve'
  | 'safety' | 'confirmation' | 'execute' | 'cancel' | 'failure' | 'terminal';
export type VoiceFlowTracePhase = 'initial' | 'confirmation_preflight' | 'confirm_revalidation';
type Fields = Readonly<Record<string, unknown>>;
let nextOperation = 0;
const traceSession = Date.now().toString(36);

/** A logical task outlives individual microphone/AbortController ownership leases. */
export function createVoiceFlowTrace() {
  const op = `${traceSession}-${++nextOperation}`;
  let ended = false;
  const emit = (stage: VoiceFlowTraceStage, fields: Fields = {}) => {
    if (!ended) voiceFlowDiagnostic('trace', { op, stage, ...fields });
  };
  return {
    get ended() { return ended; },
    emit,
    end(outcome: string, fields: Fields = {}) {
      if (ended) return;
      emit('terminal', { outcome, ...fields });
      ended = true;
    },
    command(command: ResolvedCommand, phase: VoiceFlowTracePhase) {
      emit('resolve', { phase, result: 'command', ...summarizeVoiceCommand(command) });
      emit('safety', { phase, commandIntent: command.ir.intent, ...classifyVoiceCommandSafety(command.ir) });
    },
  };
}

export function summarizeVoiceCommand(command: ResolvedCommand): Fields {
  const { ir } = command;
  const fields: Record<string, unknown> = { commandIntent: ir.intent, projectId: ir.projectId };
  // Deliberately omit merged note bodies, complete tag lists and board objects.
  for (const key of ['localId', 'toColumnKey', 'columnKey', 'assigneeUserId', 'title', 'tag'] as const) {
    if (key in ir.entities) fields[key] = (ir.entities as unknown as Record<string, unknown>)[key];
  }
  if ('notes' in ir.entities) fields.notesLength = ir.entities.notes.length;
  return fields;
}

export function summarizeVoiceInterpretation(value: VoiceCommandInterpretation): Fields {
  if (value.kind === 'unsupported') return { interpretationKind: value.kind, result: 'failure', code: value.failure.code };
  if (value.kind === 'candidate') return { interpretationKind: value.kind, command: value.command };
  const fields: Record<string, unknown> = { interpretationKind: value.kind, intent: value.intent.kind };
  for (const key of ['target', 'destination', 'operation', 'slot', 'selector'] as const) {
    if (key in value.intent) fields[key] = (value.intent as unknown as Record<string, unknown>)[key];
  }
  return fields;
}
