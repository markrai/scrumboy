import type { LocalTextGenerationCapability, LocalTextGenerationStatus } from '../platform/local-text-generation.js';
import { canRunVoiceMutationInContext, getVoiceCommandContextForIdentity, type VoiceCommandContext } from './command-context.js';
import { isCommandFailure } from './schema.js';
import {
  executableCreatePlan,
  guardCreateRequest,
  VOICE_CREATE_LIMITS,
  VoiceCreatePlanError,
  type VoiceCreatePlanResult,
  type VoiceCreatePlanV1,
} from './voice-create-plan.js';
import { VOICE_CREATE_DRY_RUN_OUTPUT_PREVIEW_CODE_UNITS, type VoiceCreatePlanner } from './voice-create-planner.js';
import type { VoiceCreateMember } from './voice-create-members.js';
import type { VoiceCreateTag, VoiceCreateTagsReader } from './voice-create-tags.js';
import {
  prepareVoiceCreate,
  type CreateMemberChoice,
  type CreatePreparation,
  type PreparedVoiceCreate,
  type VoiceCreateTagBinding,
} from './voice-create-prepare.js';
import { matchVoiceCreateTagReferenceDetailed } from './voice-create-tag-reconciliation.js';
import {
  createVoiceCreateTagRepairRequest,
  type VoiceCreateTagRepairResult,
  type VoiceCreateTagSemanticRepair,
} from './voice-create-tag-semantic-repair.js';

export const VOICE_CREATE_DRY_RUN_VERSION = 1 as const;
export const VOICE_CREATE_DRY_RUN_TIMEOUT_MS = 45_000;

export type VoiceCreateMembersReader = (
  projectSlug: string,
  signal: AbortSignal,
) => Promise<readonly VoiceCreateMember[]>;

type SemanticEvaluationPorts = Readonly<{
  planner: VoiceCreatePlanner;
  context(signal: AbortSignal): VoiceCommandContext;
  refreshBoard(): Promise<void>;
  readMembers: VoiceCreateMembersReader;
  readTags: VoiceCreateTagsReader;
  tagRepair?: VoiceCreateTagSemanticRepair;
  onPlannerStart?(): void;
  onPlan?(plan: VoiceCreatePlanResult): void;
  onTagRepair?(result: VoiceCreateTagRepairResult): void;
}>;

export type VoiceCreateSemanticEvaluation = Readonly<{
  plan: VoiceCreatePlanV1;
  preparation: CreatePreparation;
  tagBindings: readonly VoiceCreateTagBinding[];
  tagRepairAttempted: boolean;
}>;

export type VoiceCreateDryRunFailureStage =
  | 'input_validation'
  | 'provider_readiness'
  | 'planner_invocation'
  | 'planner_parsing'
  | 'planner_contract_validation'
  | 'unsupported_request_guard'
  | 'context_authorization'
  | 'lane_resolution'
  | 'member_resolution'
  | 'tag_resolution'
  | 'command_ir_validation'
  | 'preparation'
  | 'confirmation_construction';

export type VoiceCreateDryRunOutcome =
  | 'ready'
  | 'blocked'
  | 'planner_failed'
  | 'resolution_failed'
  | 'validation_failed'
  | 'context_failed'
  | 'unexpected_failure';

export type VoiceCreateDryRunFailure = Readonly<{
  status: 'failed';
  stage: VoiceCreateDryRunFailureStage;
  code: string;
  details?: Readonly<Record<string, unknown>>;
}>;

export type VoiceCreateDryRunResultV1 = Readonly<{
  version: typeof VOICE_CREATE_DRY_RUN_VERSION;
  input: string;
  outcome: VoiceCreateDryRunOutcome;
  planner:
    | Readonly<{ status: 'ok'; plan: VoiceCreatePlanResult }>
    | VoiceCreateDryRunFailure;
  preparation?:
    | Readonly<{
        status: 'ready';
        title: string;
        lane: Readonly<{ key: string; name: string; defaulted: boolean }>;
        assignee: Readonly<{ userId: number; name: string }> | null;
        tags: readonly string[];
        notesPresent: boolean;
        summary: string;
      }>
    | VoiceCreateDryRunFailure;
  confirmationReady: boolean;
  plannerCallCount: number;
  mutationExecuted: false;
}>;

export type VoiceCreateDryRunOptions = Readonly<{
  planner: VoiceCreatePlanner;
  getContext(): VoiceCommandContext | null;
  refreshBoard(): Promise<void>;
  readMembers: VoiceCreateMembersReader;
  readTags: VoiceCreateTagsReader;
  tagRepair?: VoiceCreateTagSemanticRepair;
  provider?: Pick<LocalTextGenerationCapability, 'status'>;
  signal?: AbortSignal;
  timeoutMs?: number;
}>;

const PARSER_CODES = new Set<VoiceCreatePlanError['code']>([
  'invalid_json',
  'not_object',
  'output_too_large',
  'surrounding_prose',
]);
const CONTRACT_CODES = new Set<VoiceCreatePlanError['code']>([
  'invalid_plan',
  'wrong_version',
  'invalid_kind',
  'unknown_fields',
  'missing_required_field',
  'invalid_title',
  'invalid_lane',
  'invalid_assignee',
  'invalid_tags',
  'invalid_notes',
  'invalid_unhandled',
  'missing_title',
]);

type VoiceCreatePreparationAuthority = Readonly<{
  context: VoiceCommandContext;
  members: readonly VoiceCreateMember[];
  tags: readonly VoiceCreateTag[];
}>;

async function readVoiceCreatePreparationAuthority(
  plan: VoiceCreatePlanV1,
  signal: AbortSignal,
  ports: Pick<SemanticEvaluationPorts, 'context' | 'refreshBoard' | 'readMembers' | 'readTags'>,
): Promise<VoiceCreatePreparationAuthority> {
  ports.context(signal);
  await ports.refreshBoard();
  const context = ports.context(signal);
  let members: readonly VoiceCreateMember[] = [];
  if (plan.assignee !== undefined) {
    members = await ports.readMembers(context.projectSlug, signal);
    if (!Array.isArray(members)) throw new VoiceCreatePlanError('network');
  }
  let tags: readonly VoiceCreateTag[] = [];
  if (plan.tags?.length) {
    tags = await ports.readTags(context.projectSlug, signal);
    if (!Array.isArray(tags)) throw new VoiceCreatePlanError('network');
  }
  return Object.freeze({ context: ports.context(signal), members, tags });
}

/** Shared read/prepare path used by member choice and confirm revalidation. */
export async function prepareVoiceCreateAgainstCurrentContext(
  plan: VoiceCreatePlanV1,
  signal: AbortSignal,
  ports: Pick<SemanticEvaluationPorts, 'context' | 'refreshBoard' | 'readMembers' | 'readTags'>,
  selection?: CreateMemberChoice,
  tagBindings: readonly VoiceCreateTagBinding[] = [],
): Promise<CreatePreparation> {
  const authority = await readVoiceCreatePreparationAuthority(plan, signal, ports);
  return prepareVoiceCreate(plan, authority.context, authority.members, authority.tags, selection, tagBindings);
}

export type VoiceCreateContextPreparation = Readonly<{
  preparation: CreatePreparation;
  tagBindings: readonly VoiceCreateTagBinding[];
  tagRepairAttempted: boolean;
}>;

function unavailableTagError(error: unknown): error is VoiceCreatePlanError {
  return error instanceof VoiceCreatePlanError
    && error.code === 'tag'
    && error.details?.result === 'unavailable';
}

/**
 * Optional second-pass repair exists only at the Create authority seam. A
 * selected candidate is rebound through a fresh full preparation before it can
 * reach review; malformed, incomplete, or unavailable repair falls back to the
 * existing suggestion/failure result.
 */
export async function prepareVoiceCreateWithTagRepairAgainstCurrentContext(
  plan: VoiceCreatePlanV1,
  transcript: string,
  signal: AbortSignal,
  ports: Pick<SemanticEvaluationPorts, 'context' | 'refreshBoard' | 'readMembers' | 'readTags' | 'tagRepair' | 'onTagRepair'>,
  selection?: CreateMemberChoice,
  tagBindings: readonly VoiceCreateTagBinding[] = [],
): Promise<VoiceCreateContextPreparation> {
  const authority = await readVoiceCreatePreparationAuthority(plan, signal, ports);
  let initialPreparation: CreatePreparation | null = null;
  let initialError: unknown;
  try {
    initialPreparation = prepareVoiceCreate(plan, authority.context, authority.members, authority.tags, selection, tagBindings);
    if (initialPreparation.kind !== 'tag-suggestion') {
      return Object.freeze({ preparation: initialPreparation, tagBindings, tagRepairAttempted: false });
    }
  } catch (error) {
    if (!unavailableTagError(error)) throw error;
    initialError = error;
  }

  const plannedReferences = plan.tags ?? [];
  const boundIndexes = new Set(tagBindings.map(binding => binding.referenceIndex));
  const unresolvedIndexes = plannedReferences
    .map((reference, index) => ({ index, resolved: matchVoiceCreateTagReferenceDetailed(reference, authority.tags) }))
    .filter(value => !boundIndexes.has(value.index) && value.resolved.match.matches.length === 0)
    .map(value => value.index);
  const request = ports.tagRepair
    ? createVoiceCreateTagRepairRequest(transcript, plannedReferences, unresolvedIndexes, authority.tags)
    : null;
  if (!request) {
    if (initialPreparation) return Object.freeze({ preparation: initialPreparation, tagBindings, tagRepairAttempted: false });
    throw initialError;
  }

  let repair: VoiceCreateTagRepairResult;
  try {
    repair = await ports.tagRepair!(request, signal);
  } catch {
    repair = Object.freeze({ result: 'invalid', candidateCount: 0, bindings: Object.freeze([]) });
  }
  ports.onTagRepair?.(repair);
  if (repair.result !== 'selected') {
    if (initialPreparation) return Object.freeze({ preparation: initialPreparation, tagBindings, tagRepairAttempted: true });
    throw initialError;
  }

  const repairedBindings = Object.freeze([...tagBindings, ...repair.bindings]);
  const preparation = await prepareVoiceCreateAgainstCurrentContext(plan, signal, ports, selection, repairedBindings);
  return Object.freeze({ preparation, tagBindings: repairedBindings, tagRepairAttempted: true });
}

/** The one semantic Create v2 path. It has read ports but deliberately no execute port. */
export async function evaluateVoiceCreateSemantics(
  transcript: string,
  signal: AbortSignal,
  ports: SemanticEvaluationPorts,
): Promise<VoiceCreateSemanticEvaluation> {
  ports.context(signal);
  ports.onPlannerStart?.();
  const result = await ports.planner(transcript, signal);
  ports.context(signal);
  ports.onPlan?.(result);
  if (result.kind !== 'create') {
    throw new VoiceCreatePlanError('incomplete_request', { result: 'not_create' });
  }
  const plan = executableCreatePlan(result);
  guardCreateRequest(plan, transcript);
  const prepared = await prepareVoiceCreateWithTagRepairAgainstCurrentContext(plan, transcript, signal, ports);
  return Object.freeze({ plan, ...prepared });
}

function baseResult(input: string, plannerCallCount: number) {
  return {
    version: VOICE_CREATE_DRY_RUN_VERSION,
    input,
    confirmationReady: false as boolean,
    plannerCallCount,
    mutationExecuted: false as const,
  };
}

function safeDetails(value: unknown, allowOutputPreview: boolean): Readonly<Record<string, unknown>> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const source = value as Record<string, unknown>;
  const allowed = [
    'result',
    'candidateCount',
    'referenceNormalizationApplied',
    'reference',
    'repairAttempted',
    'repairCandidateCount',
    'repairResult',
    'outputLength',
    'unexpectedFields',
    'commandCode',
    'providerState',
    'providerReason',
    'providerAction',
    'providerCode',
  ];
  if (allowOutputPreview) allowed.push('outputPreview');
  const details: Record<string, unknown> = {};
  for (const key of allowed) {
    const item = source[key];
    if (key === 'outputPreview' && typeof item === 'string') details[key] = item.slice(0, VOICE_CREATE_DRY_RUN_OUTPUT_PREVIEW_CODE_UNITS);
    else if (typeof item === 'string' || typeof item === 'number' || typeof item === 'boolean') details[key] = item;
    else if (Array.isArray(item)) details[key] = item.filter(entry => typeof entry === 'string').slice(0, 8);
  }
  return Object.keys(details).length ? Object.freeze(details) : undefined;
}

function failure(stage: VoiceCreateDryRunFailureStage, code: string, details?: unknown): VoiceCreateDryRunFailure {
  const bounded = safeDetails(details, stage === 'planner_parsing');
  return Object.freeze({ status: 'failed', stage, code, ...(bounded ? { details: bounded } : {}) });
}

function plannerFailure(
  input: string,
  plannerCallCount: number,
  stage: VoiceCreateDryRunFailureStage,
  code: string,
  details?: unknown,
): VoiceCreateDryRunResultV1 {
  return Object.freeze({
    ...baseResult(input, plannerCallCount),
    outcome: stage === 'context_authorization' ? 'context_failed' : stage === 'input_validation' ? 'validation_failed' : 'planner_failed',
    planner: failure(stage, code, details),
  });
}

function preparationFailure(
  input: string,
  plannerCallCount: number,
  plan: VoiceCreatePlanResult,
  stage: VoiceCreateDryRunFailureStage,
  code: string,
  outcome: VoiceCreateDryRunOutcome,
  details?: unknown,
): VoiceCreateDryRunResultV1 {
  return Object.freeze({
    ...baseResult(input, plannerCallCount),
    outcome,
    planner: Object.freeze({ status: 'ok' as const, plan }),
    preparation: failure(stage, code, details),
  });
}

function providerDetails(status: Exclude<LocalTextGenerationStatus, { state: 'ready' }>): Record<string, unknown> {
  return {
    providerState: status.state,
    ...('reason' in status ? { providerReason: status.reason } : {}),
    ...('action' in status ? { providerAction: status.action } : {}),
  };
}

function preparationErrorResult(
  input: string,
  plannerCallCount: number,
  plan: VoiceCreatePlanResult,
  error: unknown,
): VoiceCreateDryRunResultV1 {
  if (!(error instanceof VoiceCreatePlanError)) {
    return preparationFailure(input, plannerCallCount, plan, 'preparation', 'unexpected_failure', 'unexpected_failure');
  }
  const details = error.details;
  if (error.code === 'incomplete_request') {
    return preparationFailure(input, plannerCallCount, plan, 'unsupported_request_guard', error.code, 'blocked', details);
  }
  if (error.code === 'lane') {
    const result = details?.result;
    const code = result === 'ambiguous' ? 'ambiguous_lane' : result === 'authoritative_order_unavailable' ? 'lane_order_unavailable' : 'unknown_lane';
    return preparationFailure(input, plannerCallCount, plan, 'lane_resolution', code, 'resolution_failed', details);
  }
  if (error.code === 'member') {
    const code = details?.result === 'ambiguous' ? 'ambiguous_member' : 'unknown_member';
    return preparationFailure(input, plannerCallCount, plan, 'member_resolution', code, 'resolution_failed', details);
  }
  if (error.code === 'tag') {
    const code = details?.result === 'ambiguous' ? 'ambiguous_tag' : 'unknown_tag';
    return preparationFailure(input, plannerCallCount, plan, 'tag_resolution', code, 'resolution_failed', details);
  }
  if (error.code === 'unauthorized' || error.code === 'stale_context') {
    return preparationFailure(input, plannerCallCount, plan, 'context_authorization', error.code, 'context_failed', details);
  }
  if (error.code === 'invalid_plan' && details?.commandCode) {
    return preparationFailure(input, plannerCallCount, plan, 'command_ir_validation', error.code, 'validation_failed', details);
  }
  if (CONTRACT_CODES.has(error.code)) {
    return preparationFailure(input, plannerCallCount, plan, 'planner_contract_validation', error.code, 'validation_failed', details);
  }
  return preparationFailure(input, plannerCallCount, plan, 'preparation', error.code, 'unexpected_failure', details);
}

function readyResult(
  input: string,
  plannerCallCount: number,
  plan: VoiceCreatePlanV1,
  prepared: PreparedVoiceCreate,
): VoiceCreateDryRunResultV1 {
  const ir = prepared.command.ir;
  if (ir.intent !== 'todos.create') {
    return preparationFailure(input, plannerCallCount, plan, 'confirmation_construction', 'invalid_prepared_command', 'unexpected_failure');
  }
  const entities = ir.entities;
  if (typeof entities.body !== 'string' || !Array.isArray(entities.tags)) {
    return preparationFailure(input, plannerCallCount, plan, 'confirmation_construction', 'invalid_prepared_command', 'unexpected_failure');
  }
  return Object.freeze({
    ...baseResult(input, plannerCallCount),
    outcome: 'ready',
    planner: Object.freeze({ status: 'ok' as const, plan }),
    preparation: Object.freeze({
      status: 'ready' as const,
      title: entities.title,
      lane: Object.freeze({
        key: entities.columnKey,
        name: prepared.command.statusName ?? entities.columnKey,
        defaulted: plan.lane === undefined,
      }),
      assignee: prepared.member
        ? Object.freeze({ userId: prepared.member.userId, name: prepared.member.name || prepared.member.email })
        : null,
      tags: Object.freeze([...entities.tags]),
      notesPresent: entities.body.length > 0,
      summary: prepared.command.summary,
    }),
    confirmationReady: true,
  });
}

export type VoiceCreateDryRunEvaluator = (
  input: string,
  options?: Readonly<{ timeoutMs?: number }>,
) => Promise<VoiceCreateDryRunResultV1>;

function unavailableSessionResult(input: string, code: 'provider_session_busy' | 'provider_session_poisoned'): VoiceCreateDryRunResultV1 {
  return Object.freeze({
    ...baseResult(input, 0),
    outcome: 'planner_failed',
    planner: failure('provider_readiness', code),
  });
}

/**
 * Owns one device dry-run provider session. A timeout poisons the session because
 * the losing provider promise may still be running after the hard result race.
 */
export function createVoiceCreateDryRunSession(evaluator: VoiceCreateDryRunEvaluator): VoiceCreateDryRunEvaluator {
  let state: 'ready' | 'active' | 'poisoned' = 'ready';
  return async (input, options = {}) => {
    if (state === 'poisoned') return unavailableSessionResult(input, 'provider_session_poisoned');
    if (state === 'active') return unavailableSessionResult(input, 'provider_session_busy');
    state = 'active';
    try {
      const result = await evaluator(input, options);
      state = result.planner.status === 'failed' && result.planner.code === 'planner_timeout'
        ? 'poisoned'
        : 'ready';
      return result;
    } catch (error) {
      state = 'poisoned';
      throw error;
    }
  };
}

/**
 * Transcript-driven evaluation boundary. The options type intentionally has no
 * execute/callTool/mutation member; only the members read is injectable.
 */
export async function evaluateVoiceCreateDryRun(
  input: string,
  options: VoiceCreateDryRunOptions,
): Promise<VoiceCreateDryRunResultV1> {
  if (typeof input !== 'string' || !input.trim() || input.length > VOICE_CREATE_LIMITS.transcript) {
    return plannerFailure(typeof input === 'string' ? input : '', 0, 'input_validation', 'invalid_transcript');
  }

  const timeoutMs = Number.isFinite(options.timeoutMs) && Number(options.timeoutMs) > 0
    ? Math.min(Number(options.timeoutMs), 120_000)
    : VOICE_CREATE_DRY_RUN_TIMEOUT_MS;
  const controller = new AbortController();
  let timedOut = false;
  const externalAbort = () => controller.abort(options.signal?.reason);
  if (options.signal?.aborted) externalAbort();
  else options.signal?.addEventListener('abort', externalAbort, { once: true });
  let plannerCallCount = 0;
  let observedPlan: VoiceCreatePlanResult | null = null;
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined;
  const timeout = new Promise<VoiceCreateDryRunResultV1>((resolve) => {
    timer = globalThis.setTimeout(() => {
      timedOut = true;
      controller.abort('voice_create_dry_run_timeout');
      resolve(observedPlan
        ? preparationFailure(input, plannerCallCount, observedPlan, 'preparation', 'preparation_timeout', 'unexpected_failure')
        : plannerFailure(input, plannerCallCount, 'planner_invocation', 'planner_timeout'));
    }, timeoutMs);
  });

  const evaluation = (async (): Promise<VoiceCreateDryRunResultV1> => {
    try {
    const initial = options.getContext();
    if (!initial) return plannerFailure(input, 0, 'context_authorization', 'context_unavailable');
    const contextIdentity = {
      initialUserId: initial.userId,
      initialProjectId: initial.projectId,
      initialProjectSlug: initial.projectSlug,
    };
    const context = (signal: AbortSignal): VoiceCommandContext => {
      if (signal.aborted) throw new VoiceCreatePlanError('stale_context');
      const current = getVoiceCommandContextForIdentity(contextIdentity, options.getContext);
      if (isCommandFailure(current)) throw new VoiceCreatePlanError('stale_context');
      if (!canRunVoiceMutationInContext(current.value)) throw new VoiceCreatePlanError('unauthorized');
      return current.value;
    };

    context(controller.signal);
    if (options.provider) {
      try {
        const status = await options.provider.status({ signal: controller.signal });
        if (status.state !== 'ready') {
          return plannerFailure(input, 0, 'provider_readiness', 'provider_unavailable', providerDetails(status));
        }
      } catch (error) {
        if (timedOut) return plannerFailure(input, 0, 'planner_invocation', 'planner_timeout');
        const providerCode = error && typeof error === 'object' && typeof (error as { code?: unknown }).code === 'string'
          ? (error as { code: string }).code
          : 'internal';
        return plannerFailure(input, 0, 'provider_readiness', 'provider_unavailable', { providerCode });
      }
    }

    const semantic = await evaluateVoiceCreateSemantics(input, controller.signal, {
      planner: async (transcript, signal) => {
        plannerCallCount += 1;
        if (plannerCallCount > 1) throw new VoiceCreatePlanError('invalid_plan');
        return options.planner(transcript, signal);
      },
      context,
      refreshBoard: options.refreshBoard,
      readMembers: options.readMembers,
      readTags: options.readTags,
      tagRepair: options.tagRepair,
      onPlan: plan => { observedPlan = plan; },
    });
    if (semantic.preparation.kind === 'member-choice') {
      return preparationFailure(input, plannerCallCount, semantic.plan, 'member_resolution', 'ambiguous_member', 'resolution_failed', {
        result: 'ambiguous',
        candidateCount: semantic.preparation.choices.length,
      });
    }
    if (semantic.preparation.kind === 'tag-suggestion') {
      return preparationFailure(input, plannerCallCount, semantic.plan, 'tag_resolution', 'unknown_tag', 'resolution_failed', {
        result: 'unavailable',
        candidateCount: 0,
        referenceNormalizationApplied: false,
      });
    }
    return readyResult(input, plannerCallCount, semantic.plan, semantic.preparation.value);
    } catch (error) {
      if (timedOut) {
        return observedPlan
          ? preparationFailure(input, plannerCallCount, observedPlan, 'preparation', 'preparation_timeout', 'unexpected_failure')
          : plannerFailure(input, plannerCallCount, 'planner_invocation', 'planner_timeout');
      }
      if (observedPlan) return preparationErrorResult(input, plannerCallCount, observedPlan, error);
      if (error instanceof VoiceCreatePlanError) {
        if (error.code === 'unauthorized' || error.code === 'stale_context') {
          return plannerFailure(input, plannerCallCount, 'context_authorization', error.code, error.details);
        }
        const stage = PARSER_CODES.has(error.code) ? 'planner_parsing' : 'planner_contract_validation';
        return plannerFailure(input, plannerCallCount, stage, error.code, error.details);
      }
      const providerCode = error && typeof error === 'object' && typeof (error as { code?: unknown }).code === 'string'
        ? (error as { code: string }).code
        : undefined;
      return plannerFailure(input, plannerCallCount, 'planner_invocation', providerCode ?? 'planner_failed', providerCode ? { providerCode } : undefined);
    }
  })();

  try {
    return await Promise.race([evaluation, timeout]);
  } finally {
    if (timer !== undefined) globalThis.clearTimeout(timer);
    options.signal?.removeEventListener('abort', externalAbort);
  }
}
