import { canRunVoiceMutationInContext } from './command-context.js';
import { normalizeLookup } from './normalize.js';
import { matchVoiceMembers, matchVoiceTagsDetailed, resolveVoiceLane, voiceBoardLanes, formatResolvedCommand } from './resolve.js';
import { isCommandFailure, validateCommandIR } from './schema.js';
import { executableCreatePlan, VoiceCreatePlanError } from './voice-create-plan.js';
import { reconcileVoiceCreateTagReferences } from './voice-create-tag-reconciliation.js';
import { findVoiceCreateTagSuggestions } from './voice-create-tag-suggestions.js';
export function formatVoiceCreateMember(member) {
    const name = member.name?.trim() ?? '';
    const email = member.email?.trim() ?? '';
    if (name && email)
        return `${name} · ${email}`;
    return name || email || String(member.userId);
}
/** No execution/UI ports. Caller refreshes and supplies authoritative project data. */
export function prepareVoiceCreate(planInput, context, members, authoritativeTags, selection, tagBindings = []) {
    const plan = executableCreatePlan(planInput);
    if (!canRunVoiceMutationInContext(context))
        throw new VoiceCreatePlanError('unauthorized');
    const { board } = context;
    if (board.project.id !== context.projectId || board.project.slug !== context.projectSlug)
        throw new VoiceCreatePlanError('stale_context');
    // Defaults require the same authoritative order rendered by the board. An
    // explicit reference resolves directly and never depends on ordering metadata.
    if (plan.lane === undefined && (!board.columnOrder?.length || new Set(board.columnOrder.map(lane => lane.key)).size !== board.columnOrder.length)) {
        throw new VoiceCreatePlanError('lane', { result: 'authoritative_order_unavailable' });
    }
    const resolvedLane = plan.lane === undefined ? { ok: true, value: voiceBoardLanes(board)[0] } : resolveVoiceLane(plan.lane, board);
    if (isCommandFailure(resolvedLane)) {
        throw new VoiceCreatePlanError('lane', {
            result: resolvedLane.code === 'ambiguous_status' ? 'ambiguous' : 'unavailable',
            ...(resolvedLane.code === 'ambiguous_status' ? {} : { candidateCount: 0 }),
        });
    }
    if (!resolvedLane.value?.key || !resolvedLane.value.name)
        throw new VoiceCreatePlanError('lane', { result: 'unavailable', candidateCount: 0 });
    const lane = resolvedLane.value;
    let member;
    if (plan.assignee !== undefined) {
        const matches = matchVoiceMembers(plan.assignee, members);
        if (selection) {
            member = matches.find(value => value.userId === selection.userId && value.name === selection.name && value.email === selection.email);
            if (!member)
                throw new VoiceCreatePlanError('stale_context');
        }
        else {
            if (!matches.length)
                throw new VoiceCreatePlanError('member', { result: 'unavailable', candidateCount: 0 });
            if (matches.length > 1)
                return { kind: 'member-choice', choices: Object.freeze(matches.map(value => Object.freeze({ userId: value.userId, name: value.name, email: value.email }))) };
            member = matches[0];
        }
    }
    const plannedTagReferences = plan.tags ?? [];
    const effectiveTagReferences = [...plannedTagReferences];
    const boundIndexes = new Set();
    for (const binding of tagBindings) {
        const authoritativeNames = [...new Set(authoritativeTags.map(tag => tag.name))];
        const storedExact = authoritativeNames.filter(name => name === binding.tag);
        const rebound = storedExact.length
            ? storedExact
            : authoritativeNames.filter(name => normalizeLookup(name) === normalizeLookup(binding.tag));
        if (!Number.isInteger(binding.referenceIndex)
            || binding.referenceIndex < 0
            || binding.referenceIndex >= plannedTagReferences.length
            || boundIndexes.has(binding.referenceIndex)
            || rebound.length !== 1) {
            throw new VoiceCreatePlanError('stale_context');
        }
        boundIndexes.add(binding.referenceIndex);
        effectiveTagReferences[binding.referenceIndex] = rebound[0];
    }
    let reconciledTags;
    try {
        reconciledTags = reconcileVoiceCreateTagReferences(effectiveTagReferences, authoritativeTags);
    }
    catch (error) {
        if (!(error instanceof VoiceCreatePlanError) || error.code !== 'tag' || error.details?.result !== 'unavailable')
            throw error;
        const referenceIndex = effectiveTagReferences.findIndex((reference, index) => !boundIndexes.has(index) && matchVoiceTagsDetailed(reference, authoritativeTags).matches.length === 0);
        if (referenceIndex < 0)
            throw error;
        const suggestion = findVoiceCreateTagSuggestions(effectiveTagReferences[referenceIndex], authoritativeTags);
        if (suggestion.matches.length === 0)
            throw error;
        if (suggestion.matches.length > 1) {
            throw new VoiceCreatePlanError('tag', {
                entityType: 'tag',
                result: 'ambiguous',
                candidateCount: suggestion.matches.length,
                referenceNormalizationApplied: false,
            });
        }
        return {
            kind: 'tag-suggestion',
            suggestion: Object.freeze({
                referenceIndex,
                reference: plannedTagReferences[referenceIndex],
                tag: suggestion.matches[0],
                kind: suggestion.kind,
            }),
        };
    }
    const tags = [...new Set(reconciledTags.tags)];
    const tagReferenceNormalizationApplied = reconciledTags.referenceNormalizationApplied;
    tags.sort();
    const ir = validateCommandIR({ intent: 'todos.create', projectId: context.projectId, projectSlug: context.projectSlug,
        entities: { title: plan.title, columnKey: lane.key, assigneeUserId: member?.userId ?? null, tags, body: plan.notes ?? '' } }, context);
    if (isCommandFailure(ir))
        throw new VoiceCreatePlanError('invalid_plan', { commandCode: ir.code });
    const command = { ir: ir.value, summary: '', confirmLabel: '', danger: false, requiresConfirmation: true,
        statusName: lane.name, assigneeName: member ? formatVoiceCreateMember(member) : undefined };
    Object.assign(command, formatResolvedCommand(command));
    // Compare identity, meaning and policy, not arbitrary board/locale changes or only model phrases.
    const fingerprint = JSON.stringify({ ir: command.ir, lane, defaultLane: plan.lane === undefined,
        member: member ? { userId: member.userId, name: member.name, email: member.email } : null,
        tags: tags.map(name => ({ name })) });
    Object.freeze(tags);
    Object.freeze(command.ir.entities);
    Object.freeze(command.ir);
    Object.freeze(command);
    const boundMember = member ? Object.freeze({ userId: member.userId, name: member.name, email: member.email }) : undefined;
    return { kind: 'prepared', value: Object.freeze({ plan, command, fingerprint, member: boundMember, tagReferenceNormalizationApplied }) };
}
