import { voiceFlowDiagnostic } from '../platform/voiceflow-diagnostics.js';
import { classifyVoiceCommandSafety } from './command-safety.js';
let nextOperation = 0;
const traceSession = Date.now().toString(36);
/** A logical task outlives individual microphone/AbortController ownership leases. */
export function createVoiceFlowTrace() {
    const op = `${traceSession}-${++nextOperation}`;
    let ended = false;
    const emit = (stage, fields = {}) => {
        if (!ended)
            voiceFlowDiagnostic('trace', { op, stage, ...fields });
    };
    return {
        get ended() { return ended; },
        emit,
        end(outcome, fields = {}) {
            if (ended)
                return;
            emit('terminal', { outcome, ...fields });
            ended = true;
        },
        command(command, phase) {
            emit('resolve', { phase, result: 'command', ...summarizeVoiceCommand(command) });
            emit('safety', { phase, commandIntent: command.ir.intent, ...classifyVoiceCommandSafety(command.ir) });
        },
    };
}
export function summarizeVoiceCommand(command) {
    const { ir } = command;
    const fields = { commandIntent: ir.intent, projectId: ir.projectId };
    // Deliberately omit merged note bodies, complete tag lists and board objects.
    for (const key of ['localId', 'toColumnKey', 'columnKey', 'assigneeUserId', 'title', 'tag']) {
        if (key in ir.entities)
            fields[key] = ir.entities[key];
    }
    if ('notes' in ir.entities)
        fields.notesLength = ir.entities.notes.length;
    return fields;
}
export function summarizeVoiceInterpretation(value) {
    if (value.kind === 'unsupported')
        return { interpretationKind: value.kind, result: 'failure', code: value.failure.code };
    if (value.kind === 'candidate')
        return { interpretationKind: value.kind, command: value.command };
    const fields = { interpretationKind: value.kind, intent: value.intent.kind };
    for (const key of ['target', 'destination', 'operation', 'slot', 'selector']) {
        if (key in value.intent)
            fields[key] = value.intent[key];
    }
    return fields;
}
