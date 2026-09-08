import type { BoardMember } from '../state/state.js';
import { canRunVoiceMutationInContext, type VoiceCommandContext } from './command-context.js';
import { matchVoiceMembers, matchVoiceTags, resolveVoiceLane, voiceBoardLanes, formatResolvedCommand } from './resolve.js';
import { isCommandFailure, validateCommandIR, type ResolvedCommand } from './schema.js';
import { executableCreatePlan, VoiceCreatePlanError, type VoiceCreatePlanV1 } from './voice-create-plan.js';

export type CreateMemberChoice = Readonly<Pick<BoardMember, 'userId' | 'name' | 'email'>>;
export type PreparedVoiceCreate = Readonly<{ plan: VoiceCreatePlanV1; command: ResolvedCommand; fingerprint: string; member?: CreateMemberChoice }>;
export type CreatePreparation = { kind: 'prepared'; value: PreparedVoiceCreate }
  | { kind: 'member-choice'; choices: readonly CreateMemberChoice[] };

/** No execution/UI ports. Caller refreshes and supplies authoritative project data. */
export function prepareVoiceCreate(planInput: VoiceCreatePlanV1, context: VoiceCommandContext, members: readonly BoardMember[], selection?: CreateMemberChoice): CreatePreparation {
  const plan = executableCreatePlan(planInput);
  if (!canRunVoiceMutationInContext(context)) throw new VoiceCreatePlanError('unauthorized');
  const { board } = context;
  if (board.project.id !== context.projectId || board.project.slug !== context.projectSlug) throw new VoiceCreatePlanError('stale_context');
  // voiceBoardLanes also has a legacy object-order fallback. V2 deliberately forbids it.
  if (!board.columnOrder?.length || new Set(board.columnOrder.map(lane => lane.key)).size !== board.columnOrder.length) throw new VoiceCreatePlanError('lane');
  const lanes = voiceBoardLanes(board);
  const resolvedLane = plan.lane === undefined ? { ok: true as const, value: lanes[0] } : resolveVoiceLane(plan.lane, board);
  if (isCommandFailure(resolvedLane) || !resolvedLane.value?.key || !resolvedLane.value.name) throw new VoiceCreatePlanError('lane');
  const lane = resolvedLane.value;
  let member: CreateMemberChoice | undefined;
  if (plan.assignee !== undefined) {
    const matches = matchVoiceMembers(plan.assignee, members);
    if (selection) {
      member = matches.find(value => value.userId === selection.userId && value.name === selection.name && value.email === selection.email);
      if (!member) throw new VoiceCreatePlanError('stale_context');
    } else {
      if (!matches.length) throw new VoiceCreatePlanError('member');
      if (matches.length > 1) return { kind: 'member-choice', choices: Object.freeze(matches.map(value => Object.freeze({ userId: value.userId, name: value.name, email: value.email }))) };
      member = matches[0];
    }
  }
  const tags: string[] = [];
  for (const reference of plan.tags ?? []) {
    const matches = matchVoiceTags(reference, board);
    if (matches.length !== 1) throw new VoiceCreatePlanError('tag');
    if (!tags.includes(matches[0])) tags.push(matches[0]);
  }
  tags.sort();
  const ir = validateCommandIR({ intent: 'todos.create', projectId: context.projectId, projectSlug: context.projectSlug,
    entities: { title: plan.title, columnKey: lane.key, assigneeUserId: member?.userId ?? null, tags, body: plan.notes ?? '' } }, context);
  if (isCommandFailure(ir)) throw new VoiceCreatePlanError('invalid_plan');
  const command: ResolvedCommand = { ir: ir.value, summary: '', confirmLabel: '', danger: false, requiresConfirmation: true,
    statusName: lane.name, assigneeName: member ? `${member.name} · ${member.email}` : undefined };
  Object.assign(command, formatResolvedCommand(command));
  // Compare identity, meaning and policy, not arbitrary board/locale changes or only model phrases.
  const fingerprint = JSON.stringify({ ir: command.ir, lane, defaultLane: plan.lane === undefined,
    member: member ? { userId: member.userId, name: member.name, email: member.email } : null,
    tags: tags.map(name => ({ name, ids: board.tags.filter(tag => tag.name === name).map(tag => tag.tagId ?? null).sort() })) });
  Object.freeze(tags); Object.freeze(command.ir.entities); Object.freeze(command.ir); Object.freeze(command);
  const boundMember = member ? Object.freeze({ userId: member.userId, name: member.name, email: member.email }) : undefined;
  return { kind: 'prepared', value: Object.freeze({ plan, command, fingerprint, member: boundMember }) };
}
