import type { Board } from '../types.js';
import type { BoardMember } from '../state/state.js';
import {
  LocalTextGenerationError,
  type LocalTextGenerationCapability,
} from '../platform/local-text-generation.js';
import {
  generateVoiceInterpretationRaw,
  parseVoiceInterpretationEnvelopeDetails,
  validateVoiceInterpretationCandidate,
} from './local-interpretation.js';
import {
  INTERPRETATION_LAB_CANDIDATE_PROFILE,
  INTERPRETATION_LAB_CURRENT_PROFILE,
  INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
  interpretationLabInstructions,
  type InterpretationLabProfile,
} from './interpretation-lab-prompts.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import { isCommandFailure } from './schema.js';

export type InterpretationLabClassification =
  | 'recognized'
  | 'partial'
  | 'model-refused'
  | 'output-rejected'
  | 'canonical-rejected'
  | 'resolution-failed'
  | 'provider-error'
  | 'cancelled';

export type VoiceInterpretationDiagnostic = {
  input: string;
  profile: InterpretationLabProfile;
  rawOutput?: string;
  provider: 'ok' | 'error' | 'cancelled';
  envelope:
    | { state: 'candidate'; command: string; unrepresented: string[] }
    | { state: 'refused'; command: string | null; unrepresented: string[] }
    | { state: 'rejected'; reason: string }
    | { state: 'not-run' };
  canonicalParse:
    | { state: 'ok'; command: string }
    | { state: 'failure'; code: string; message: string }
    | { state: 'not-run' };
  resolution:
    | { state: 'ok'; summary: string }
    | { state: 'failure'; code: string; message: string }
    | { state: 'not-run' };
  finalClassification: InterpretationLabClassification;
};

export type InterpretationLabResolveContext = {
  projectId: number;
  projectSlug: string;
  board: Board;
  members: BoardMember[];
};

export type DiagnoseVoiceInterpretationOptions = {
  input: string;
  profile: InterpretationLabProfile;
  capability: LocalTextGenerationCapability;
  locale?: string;
  signal?: AbortSignal;
  getResolveContext?: () => InterpretationLabResolveContext | null;
};

export type InterpretationLabProgress = {
  completed: number;
  total: number;
  input: string;
  profile: InterpretationLabProfile;
  result?: VoiceInterpretationDiagnostic;
};

export type RunInterpretationLabBatchOptions = {
  inputs: readonly string[];
  profiles: readonly InterpretationLabProfile[];
  capability: LocalTextGenerationCapability;
  locale?: string;
  signal?: AbortSignal;
  getResolveContext?: () => InterpretationLabResolveContext | null;
  onProgress?: (progress: InterpretationLabProgress) => void;
};

const NOT_RUN = Object.freeze({ state: 'not-run' as const });

function cancelledDiagnostic(
  input: string,
  profile: InterpretationLabProfile,
  rawOutput?: string,
): VoiceInterpretationDiagnostic {
  return {
    input,
    profile,
    ...(rawOutput === undefined ? {} : { rawOutput }),
    provider: 'cancelled',
    envelope: NOT_RUN,
    canonicalParse: NOT_RUN,
    resolution: NOT_RUN,
    finalClassification: 'cancelled',
  };
}

function stableProviderReason(error: unknown): string {
  if (error instanceof LocalTextGenerationError) return error.code;
  if (error instanceof Error && error.name === 'AbortError') return 'cancelled';
  return 'unavailable';
}

function isCancellation(error: unknown, signal?: AbortSignal): boolean {
  return !!signal?.aborted
    || (error instanceof LocalTextGenerationError && error.code === 'cancelled')
    || (error instanceof Error && error.name === 'AbortError');
}

export async function diagnoseVoiceInterpretation(
  options: DiagnoseVoiceInterpretationOptions,
): Promise<VoiceInterpretationDiagnostic> {
  const input = String(options.input ?? '').trim();
  const base = { input, profile: options.profile };
  let rawOutput: string | undefined;

  try {
    const generated = await generateVoiceInterpretationRaw({
      transcript: input,
      instructions: interpretationLabInstructions(options.profile),
      capability: options.capability,
      locale: options.locale ?? 'en',
      signal: options.signal,
    });
    rawOutput = generated.rawOutput;
  } catch (error) {
    if (isCancellation(error, options.signal)) return cancelledDiagnostic(input, options.profile);
    const reason = stableProviderReason(error);
    if (error instanceof LocalTextGenerationError && error.code === 'output_rejected') {
      return {
        ...base,
        provider: 'ok',
        envelope: { state: 'rejected', reason: 'generation-output-rejected' },
        canonicalParse: NOT_RUN,
        resolution: NOT_RUN,
        finalClassification: 'output-rejected',
      };
    }
    return {
      ...base,
      provider: 'error',
      envelope: { state: 'rejected', reason: `provider:${reason}` },
      canonicalParse: NOT_RUN,
      resolution: NOT_RUN,
      finalClassification: 'provider-error',
    };
  }

  if (options.signal?.aborted) return cancelledDiagnostic(input, options.profile, rawOutput);

  let command: string | null;
  let unrepresented: string[];
  try {
    const envelope = parseVoiceInterpretationEnvelopeDetails(rawOutput, input);
    command = envelope.command;
    unrepresented = envelope.unrepresented;
  } catch {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope: { state: 'rejected', reason: 'envelope-or-safety-contract' },
      canonicalParse: NOT_RUN,
      resolution: NOT_RUN,
      finalClassification: 'output-rejected',
    };
  }

  const envelope = command === null || unrepresented.length > 0
    ? { state: 'refused' as const, command, unrepresented }
    : { state: 'candidate' as const, command, unrepresented };

  if (command === null) {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope,
      canonicalParse: NOT_RUN,
      resolution: NOT_RUN,
      finalClassification: 'model-refused',
    };
  }

  try {
    validateVoiceInterpretationCandidate(input, command);
  } catch {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope: { state: 'rejected', reason: 'candidate-preservation' },
      canonicalParse: NOT_RUN,
      resolution: NOT_RUN,
      finalClassification: 'output-rejected',
    };
  }

  const parsed = parseCommand(command);
  if (isCommandFailure(parsed)) {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope,
      canonicalParse: { state: 'failure', code: parsed.code, message: parsed.message },
      resolution: NOT_RUN,
      finalClassification: 'canonical-rejected',
    };
  }

  const canonicalParse = { state: 'ok' as const, command };
  if (options.signal?.aborted) return cancelledDiagnostic(input, options.profile, rawOutput);
  const context = options.getResolveContext?.() ?? null;
  if (!context) {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope,
      canonicalParse,
      resolution: NOT_RUN,
      finalClassification: unrepresented.length > 0 ? 'partial' : 'recognized',
    };
  }

  try {
    const resolved = await resolveCommandDraft(parsed.value, {
      projectId: context.projectId,
      projectSlug: context.projectSlug,
      board: context.board,
      members: context.members,
    });
    if (options.signal?.aborted) return cancelledDiagnostic(input, options.profile, rawOutput);
    if (isCommandFailure(resolved)) {
      return {
        ...base,
        rawOutput,
        provider: 'ok',
        envelope,
        canonicalParse,
        resolution: { state: 'failure', code: resolved.code, message: resolved.message },
        finalClassification: 'resolution-failed',
      };
    }
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope,
      canonicalParse,
      resolution: { state: 'ok', summary: resolved.value.summary },
      finalClassification: unrepresented.length > 0 ? 'partial' : 'recognized',
    };
  } catch {
    return {
      ...base,
      rawOutput,
      provider: 'ok',
      envelope,
      canonicalParse,
      resolution: { state: 'failure', code: 'internal', message: 'Resolution failed.' },
      finalClassification: 'resolution-failed',
    };
  }
}

export async function runInterpretationLabBatch(
  options: RunInterpretationLabBatchOptions,
): Promise<VoiceInterpretationDiagnostic[]> {
  const inputs = options.inputs.map((input) => String(input ?? '').trim()).filter(Boolean);
  const profiles = options.profiles.filter((profile) => (
    profile === INTERPRETATION_LAB_CURRENT_PROFILE
    || profile === INTERPRETATION_LAB_EXPERIMENTAL_PROFILE
    || profile === INTERPRETATION_LAB_CANDIDATE_PROFILE
  ));
  const total = inputs.length * profiles.length;
  const results: VoiceInterpretationDiagnostic[] = [];

  for (const input of inputs) {
    for (const profile of profiles) {
      if (options.signal?.aborted) return results;
      options.onProgress?.({ completed: results.length, total, input, profile });
      const result = await diagnoseVoiceInterpretation({
        input,
        profile,
        capability: options.capability,
        locale: options.locale,
        signal: options.signal,
        getResolveContext: options.getResolveContext,
      });
      results.push(result);
      options.onProgress?.({ completed: results.length, total, input, profile, result });
      if (result.finalClassification === 'cancelled') return results;
    }
  }
  return results;
}
