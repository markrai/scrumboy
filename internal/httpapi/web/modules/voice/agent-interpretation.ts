import type { Board } from '../types.js';
import type { AgentEnvelope, AgentStateKind } from './agent-protocol.js';
import { recognizeMoveCommand } from './agent-move-extraction.js';

export type AgentTaskCompletion = 'continue' | 'finish_after_effect';
export type ApplicationInterpretationSource = 'deterministic_move' | 'model';
export type ApplicationInterpretation = Readonly<{
  envelope: AgentEnvelope;
  source: ApplicationInterpretationSource;
  completion: AgentTaskCompletion;
}>;

/**
 * Production precedence for application-owned interpretation. The evaluator calls
 * this same function so application accuracy observes orchestration instead of
 * reconstructing it in a separate order.
 */
export function interpretApplicationEnvelope(
  utterance: string,
  modelEnvelope: AgentEnvelope | null,
  stateKind: AgentStateKind,
  board?: Board,
): ApplicationInterpretation | null {
  if (stateKind === 'idle') {
    const recognized = recognizeMoveCommand(utterance, board);
    if (recognized) return { ...recognized, source: 'deterministic_move' };
  }
  return modelEnvelope ? { envelope: modelEnvelope, source: 'model', completion: 'continue' } : null;
}
