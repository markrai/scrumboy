import { LOCAL_TEXT_GENERATION_CAPABILITY, type LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { getVoiceCreateDryRunBoardPorts } from '../views/board.js';
import { VOICE_AGENT_EVALUATION_CASES } from './agent-evaluation-corpus.js';
import {
  createVoiceAgentEvaluationModel,
  evaluateVoiceAgentCorpus,
  evaluateVoiceAgentUtterance,
  voiceAgentEvaluationCaseApplicability,
  VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS,
  VOICE_AGENT_EVALUATION_TIMEOUT_MS,
  type VoiceAgentCorpusEvaluation,
  type VoiceAgentUtteranceEvaluation,
} from './agent-evaluation.js';

function currentBoardContext() {
  return getVoiceCreateDryRunBoardPorts().getContext();
}

function emptyUtterance(transcript: string): VoiceAgentUtteranceEvaluation {
  return Object.freeze({
    version: 1,
    transcript,
    deterministicCall: null,
    rawEnvelope: null,
    protocolEnvelope: null,
    protocolRecoveredFrom: null,
    guardedEnvelope: null,
    slots: { reference: null, numeric: false, lane: null },
    boardApplicable: false,
    boardApplicabilityReason: 'board_unavailable',
    rawMatches: null,
    protocolMatches: null,
    applicationMatches: null,
    mismatches: ['kind'] as const,
    mutationExecuted: false,
    providerUsed: false,
  });
}

async function evaluateOnCurrentBoard(
  transcript: string,
  options: Readonly<{ timeoutMs?: number }> = {},
): Promise<VoiceAgentUtteranceEvaluation> {
  const capability = getAppRuntime().capability(LOCAL_TEXT_GENERATION_CAPABILITY) as LocalTextGenerationCapability | null;
  const context = currentBoardContext();
  if (!context) return emptyUtterance(transcript);
  const controller = new AbortController();
  const timeoutMs = options.timeoutMs ?? VOICE_AGENT_EVALUATION_TIMEOUT_MS;
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const testCase = VOICE_AGENT_EVALUATION_CASES.find(entry => entry.transcript === transcript);
    return await evaluateVoiceAgentUtterance(transcript, {
      model: capability ? createVoiceAgentEvaluationModel(capability) : null,
      board: context.board,
      expected: testCase?.expected,
      applicability: testCase ? voiceAgentEvaluationCaseApplicability(testCase, context.board) : undefined,
      signal: controller.signal,
    });
  } finally {
    clearTimeout(timer);
  }
}

async function evaluateCorpusOnCurrentBoard(
  options: Readonly<{ timeoutMs?: number }> = {},
): Promise<VoiceAgentCorpusEvaluation> {
  const capability = getAppRuntime().capability(LOCAL_TEXT_GENERATION_CAPABILITY) as LocalTextGenerationCapability | null;
  const context = currentBoardContext();
  const controller = new AbortController();
  const timeoutMs = options.timeoutMs ?? VOICE_AGENT_EVALUATION_TIMEOUT_MS * Math.max(1, VOICE_AGENT_EVALUATION_CASES.length);
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    if (!context) {
      return Object.freeze({
        version: 1,
        mutationExecuted: false,
        providerUsed: false,
        boardRequirements: VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS,
        caseCount: 0,
        applicableCount: 0,
        inapplicableCount: 0,
        rawMatched: 0,
        protocolMatched: 0,
        applicationMatched: 0,
        rawAccuracy: null,
        protocolAccuracy: null,
        applicationAccuracy: null,
        results: [],
      });
    }
    return await evaluateVoiceAgentCorpus(VOICE_AGENT_EVALUATION_CASES, {
      model: capability ? createVoiceAgentEvaluationModel(capability) : null,
      board: context.board,
      signal: controller.signal,
    });
  } finally {
    clearTimeout(timer);
  }
}

/** Lazily loaded only after a native/debug request; never part of board activation. */
export const evaluateVoiceAgentOnCurrentBoard = evaluateOnCurrentBoard;
export const evaluateVoiceAgentCorpusOnCurrentBoard = evaluateCorpusOnCurrentBoard;
