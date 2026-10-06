import { recognizeMoveCommand } from './agent-move-extraction.js';
/**
 * Production precedence for application-owned interpretation. The evaluator calls
 * this same function so application accuracy observes orchestration instead of
 * reconstructing it in a separate order.
 */
export function interpretApplicationEnvelope(utterance, modelEnvelope, stateKind, board) {
    if (stateKind === 'idle') {
        const recognized = recognizeMoveCommand(utterance, board);
        if (recognized)
            return { ...recognized, source: 'deterministic_move' };
    }
    return modelEnvelope ? { envelope: modelEnvelope, source: 'model', completion: 'continue' } : null;
}
