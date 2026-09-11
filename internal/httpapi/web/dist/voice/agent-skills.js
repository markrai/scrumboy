import { classifyVoiceCommandSafety } from './command-safety.js';
import { canRunVoiceMutationInContext, getActiveVoiceCommandContext } from './command-context.js';
import { callMcpTool } from './mcp-client.js';
import { executeCommandIR } from './execute.js';
import { rankTitleCandidates, resolveExactTodoTitle, resolveTodoTarget } from './target-resolver.js';
import { normalizeLookup, parseSpokenNumber, stripWrappingQuotes } from './normalize.js';
import { formatResolvedCommand, resolveVoiceLane, voiceBoardLanes, matchVoiceMembers, matchVoiceTags } from './resolve.js';
import { isCommandFailure, validateCommandIR } from './schema.js';
import { voiceText } from './i18n.js';
import { AGENT_LIMITS, AgentProtocolError, SKILL_NAMES } from './agent-protocol.js';
const short = (value, max = 200) => value.slice(0, max);
const resourceLike = (value) => /^(todo|member|lane|tag|proposal)_/.test(value);
const fail = (status, resource = 'todo') => ({ result: status === 'not_found' ? { status, resource } : { status } });
const TODO_REFERENCE_WRAPPER = /^(?:the\s+)?(?:story|todo|to[-\s]?do|card|task|item)(?:\s+(?:called|named|titled))?\s+(.+)$/i;
function unwrapTodoReference(reference) {
    const match = TODO_REFERENCE_WRAPPER.exec(stripWrappingQuotes(reference.trim()));
    return match?.[1]?.trim() || null;
}
function strippedReferenceIsStronger(reference, stripped, todo) {
    const candidate = [{ localId: todo.localId, title: todo.title }];
    const originalScore = rankTitleCandidates(reference, candidate)[0]?.score ?? 0;
    const strippedScore = rankTitleCandidates(stripped, candidate)[0]?.score ?? 0;
    return strippedScore > originalScore;
}
export class VoiceAgentSkillRegistry {
    constructor(options, ports = {}) {
        this.options = options;
        this.callTool = ports.callTool ?? callMcpTool;
        this.execute = ports.execute ?? executeCommandIR;
        this.skills = new Map(SKILL_NAMES.map(name => [name, Object.freeze({
                effect: name === 'todos.open' ? 'open' : ['todos.resolve', 'todos.inspect', 'analytics.count_completed'].includes(name) ? 'read' : 'proposal',
                run: (call, task, signal) => this.resolve(call, task, signal),
            })]));
    }
    context(signal) {
        if (signal.aborted)
            throw new AgentProtocolError('Task cancelled');
        const result = getActiveVoiceCommandContext(this.options);
        if (isCommandFailure(result))
            throw new AgentProtocolError('Context invalidated');
        return result.value;
    }
    async tool(name, input, signal) {
        this.context(signal);
        const result = await this.callTool(name, input, { signal });
        this.context(signal);
        return result;
    }
    lane(todo, board) {
        return todo.columnKey ?? Object.entries(board.columns).find(([, values]) => values.some(t => t.id === todo.id))?.[0] ?? todo.status;
    }
    async fresh(resource, signal) {
        const context = this.context(signal);
        const response = await this.tool('todos_get', { projectSlug: context.projectSlug, localId: resource.localId }, signal);
        if (!response.todo || response.todo.localId !== resource.localId || response.todo.id !== resource.rowId)
            throw new AgentProtocolError('Stale todo');
        return response.todo;
    }
    facts(todo, task, board) {
        const todoRef = task.handles.issue({ kind: 'todo', localId: todo.localId, rowId: todo.id });
        const lane = voiceBoardLanes(board).find(lane => lane.key === this.lane(todo, board))?.name ?? todo.status;
        return { status: 'resolved', todoRef, title: short(todo.title), number: todo.localId, lane: short(lane) };
    }
    choice(task, call, resource, choices) {
        const result = { status: 'choices', resource, choices: choices.slice(0, AGENT_LIMITS.choices) };
        task.pendingChoice = { call, result };
        task.choiceAnswered = false;
        return { result };
    }
    async todos(call, task, signal) {
        const args = call.arguments;
        const context = this.context(signal);
        if (!('reference' in args) && !('todoRef' in args))
            return [];
        if ('todoRef' in args && args.todoRef)
            return [await this.fresh(task.handles.get(args.todoRef, 'todo'), signal)];
        const reference = args.reference;
        if (resourceLike(reference))
            throw new AgentProtocolError('Use issued todoRef');
        if (['this', 'it', 'its', 'current', 'this todo', 'this story', 'the current story', 'the current todo'].includes(reference.toLowerCase().trim())) {
            return task.session.activeTodo ? [await this.fresh(task.session.activeTodo, signal)] : [];
        }
        const unwrapped = unwrapTodoReference(reference);
        const number = parseSpokenNumber(reference) ?? (unwrapped ? parseSpokenNumber(unwrapped) : null);
        let target = number
            ? { kind: 'id', localId: number.value, ambiguousId: number.ambiguous, display: reference }
            : { kind: 'title', phrase: reference, display: reference };
        const resolveContext = {
            projectSlug: context.projectSlug, board: context.board,
            callTool: (name, input) => this.tool(name, input, signal),
        };
        let resolved;
        if (target.kind === 'title' && unwrapped) {
            // A literal exact title owns the wrapped phrase. Otherwise the wrapper is
            // grammatical: resolve only its payload and never fall back to a fuzzy
            // raw match. A stripped fuzzy interpretation must be strictly stronger.
            const exactOriginal = await resolveExactTodoTitle(target.phrase, resolveContext);
            if (exactOriginal)
                resolved = exactOriginal;
            else {
                target = { kind: 'title', phrase: unwrapped, display: reference };
                resolved = await resolveTodoTarget(target, resolveContext);
                if (!isCommandFailure(resolved) && !strippedReferenceIsStronger(reference, unwrapped, resolved.value.todo))
                    return [];
            }
        }
        else
            resolved = await resolveTodoTarget(target, resolveContext);
        this.context(signal);
        const candidates = isCommandFailure(resolved) ? resolved.candidates ?? [] : [resolved.value.todo];
        const values = [];
        for (const candidate of candidates.slice(0, AGENT_LIMITS.choices)) {
            const response = await this.tool('todos_get', { projectSlug: context.projectSlug, localId: candidate.localId }, signal);
            if (!response.todo || response.todo.localId !== candidate.localId)
                throw new AgentProtocolError('Stale target');
            if (target.kind === 'title' && response.todo.title !== candidate.title)
                throw new AgentProtocolError('Title changed during resolution');
            values.push(response.todo);
        }
        return values;
    }
    async run(call, task, signal) {
        const definition = this.skills.get(call.skill);
        if (!definition)
            throw new AgentProtocolError('Unknown skill');
        const supplied = call.arguments;
        if ('todoRef' in supplied && supplied.todoRef)
            task.handles.get(supplied.todoRef, 'todo');
        if ('member' in supplied && supplied.member && resourceLike(supplied.member))
            task.handles.get(supplied.member, 'member');
        if ('tag' in supplied && resourceLike(supplied.tag))
            task.handles.get(supplied.tag, 'tag');
        if ('lane' in supplied && supplied.lane && resourceLike(supplied.lane))
            task.handles.get(supplied.lane, 'lane');
        if (task.pendingChoice) {
            const pending = task.pendingChoice;
            const args = call.arguments;
            const selection = pending.result.resource === 'todo' ? ('todoRef' in args ? args.todoRef : undefined)
                : pending.result.resource === 'member' ? ('member' in args ? args.member : undefined)
                    : pending.result.resource === 'lane' ? ('lane' in args ? args.lane : undefined)
                        : ('tag' in args ? args.tag : undefined);
            if (!task.choiceAnswered || call.skill !== pending.call.skill || !pending.result.choices.some(choice => choice.handle === selection))
                throw new AgentProtocolError('Select only an offered choice after the user replies');
            // Selection may replace only the ambiguous resource; authored content and other targets stay bound.
            const clean = (value) => {
                const copy = { ...value };
                for (const key of pending.result.resource === 'todo' ? ['reference', 'todoRef'] : [pending.result.resource])
                    delete copy[key];
                return JSON.stringify(Object.keys(copy).sort().map(key => [key, copy[key]]));
            };
            if (clean(call.arguments) !== clean(pending.call.arguments))
                throw new AgentProtocolError('Choice changed the operation');
            task.pendingChoice = null;
        }
        return definition.run(call, task, signal);
    }
    async resolve(call, task, signal) {
        let context = this.context(signal);
        const mutation = this.skills.get(call.skill).effect === 'proposal';
        if (mutation && !canRunVoiceMutationInContext(context))
            return fail('denied');
        const args = call.arguments;
        if (call.skill === 'analytics.count_completed') {
            const response = await this.tool('todos_countCompleted', { projectSlug: context.projectSlug, period: 'this-week', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' }, signal);
            if (!Number.isSafeInteger(response.count) || response.count < 0)
                return fail('invalid');
            return { result: { status: 'count', count: response.count } };
        }
        let lane;
        let member;
        let tag;
        let boundCall = call;
        if ('lane' in args && args.lane !== undefined) {
            if (resourceLike(args.lane)) {
                const resource = task.handles.get(args.lane, 'lane');
                lane = voiceBoardLanes(context.board).find(lane => lane.key === resource.key);
            }
            else {
                const resolved = resolveVoiceLane(args.lane, context.board);
                if (isCommandFailure(resolved)) {
                    if (resolved.code !== 'ambiguous_status')
                        return fail('not_found', 'lane');
                    return this.choice(task, call, 'lane', voiceBoardLanes(context.board).filter(lane => normalizeLookup(lane.name) === normalizeLookup(args.lane) || normalizeLookup(lane.key) === normalizeLookup(args.lane)).map(lane => ({ handle: task.handles.issue({ kind: 'lane', key: lane.key }), label: short(lane.name) })));
                }
                lane = resolved.value;
            }
            if (!lane)
                return fail('stale');
            boundCall = { ...call, arguments: { ...args, lane: task.handles.issue({ kind: 'lane', key: lane.key }) } };
        }
        if (call.skill === 'todos.create' && !lane) {
            lane = voiceBoardLanes(context.board)[0];
            if (!lane)
                return fail('not_found', 'lane');
            boundCall = { ...call, arguments: { ...call.arguments, lane: task.handles.issue({ kind: 'lane', key: lane.key }) } };
        }
        if ('member' in args && args.member !== undefined) {
            const response = await this.tool('members_list', { projectSlug: context.projectSlug }, signal);
            if (!Array.isArray(response.items))
                return fail('stale');
            const members = [...new Map(response.items.map(member => [member.userId, member])).values()];
            let matches;
            if (resourceLike(args.member))
                matches = members.filter(member => member.userId === task.handles.get(args.member, 'member').userId);
            else {
                matches = matchVoiceMembers(args.member, members);
            }
            if (!matches.length)
                return fail('not_found', 'member');
            if (matches.length > 1)
                return this.choice(task, call, 'member', matches.map(member => ({ handle: task.handles.issue({ kind: 'member', userId: member.userId }), label: short(`${member.name} · ${member.email}`) })));
            member = matches[0];
            boundCall = { ...boundCall, arguments: { ...boundCall.arguments, member: task.handles.issue({ kind: 'member', userId: member.userId }) } };
        }
        if ('tag' in args) {
            const names = [...new Set((context.board.tags ?? []).map(tag => tag.name))];
            const matches = resourceLike(args.tag) ? names.filter(name => name === task.handles.get(args.tag, 'tag').name)
                : matchVoiceTags(args.tag, context.board);
            if (!matches.length)
                return fail('not_found', 'tag');
            if (matches.length > 1)
                return this.choice(task, call, 'tag', matches.map(name => ({ handle: task.handles.issue({ kind: 'tag', name }), label: short(name) })));
            tag = matches[0];
            boundCall = { ...boundCall, arguments: { ...boundCall.arguments, tag: task.handles.issue({ kind: 'tag', name: tag }) } };
        }
        const candidates = await this.todos(call, task, signal);
        context = this.context(signal);
        const satisfied = (todo) => {
            switch (call.skill) {
                case 'todos.move': return this.lane(todo, context.board) === lane.key;
                case 'todos.rename': return todo.title === call.arguments.title.trim();
                case 'todos.assign': return todo.assigneeUserId === member.userId;
                case 'todos.unassign': return todo.assigneeUserId == null || (!!member && todo.assigneeUserId !== member.userId);
                case 'todos.add_tag': return (todo.tags ?? []).some(name => normalizeLookup(name) === normalizeLookup(tag));
                case 'todos.remove_tag': return !(todo.tags ?? []).some(name => normalizeLookup(name) === normalizeLookup(tag));
                case 'todos.replace_notes': return (todo.body ?? '') === call.arguments.text;
                default: return false;
            }
        };
        const actionable = candidates.filter(todo => !satisfied(todo));
        if (call.skill !== 'todos.create' && !candidates.length)
            return fail('not_found');
        if (candidates.length && !actionable.length)
            return { result: { status: 'no_op' } };
        if (actionable.length > 1)
            return this.choice(task, call, 'todo', actionable.map(todo => {
                const facts = this.facts(todo, task, context.board);
                return { handle: facts.todoRef, label: facts.title, number: facts.number, lane: facts.lane };
            }));
        const todo = actionable[0];
        const facts = todo ? this.facts(todo, task, context.board) : undefined;
        if (todo) {
            const { reference: _reference, ...rest } = boundCall.arguments;
            boundCall = { ...boundCall, arguments: { ...rest, todoRef: facts.todoRef } };
        }
        if (call.skill === 'todos.resolve')
            return { result: facts };
        if (call.skill === 'todos.open') {
            this.context(signal);
            task.diagnostic?.emit('resolve', { phase: 'initial', result: 'command', commandIntent: 'open_todo', localId: todo.localId, projectId: context.projectId });
            task.diagnostic?.emit('safety', { phase: 'initial', commandIntent: 'open_todo', ...classifyVoiceCommandSafety({ intent: 'open_todo' }) });
            task.diagnostic?.emit('execute', { result: 'started', commandIntent: 'open_todo' });
            await this.options.openTodo(todo.localId);
            task.diagnostic?.emit('execute', { result: 'success', commandIntent: 'open_todo' });
            this.context(signal);
            task.session.activeTodo = task.handles.get(facts.todoRef, 'todo');
            return { result: { ...facts, status: 'opened' } };
        }
        if (call.skill === 'todos.inspect') {
            const fields = call.arguments.fields ?? ['title', 'lane'];
            const values = fields.map(field => ({ field, value: short(field === 'title' ? todo.title : field === 'lane' ? facts.lane : field === 'notes' ? todo.body ?? '' : field === 'tags' ? (todo.tags ?? []).slice(0, 5).join(', ') : context.members.find(member => member.userId === todo.assigneeUserId)?.name ?? '', 320) }));
            return { result: { status: 'inspected', todoRef: facts.todoRef, facts: values } };
        }
        const base = { projectId: context.projectId, projectSlug: context.projectSlug };
        let ir;
        switch (call.skill) {
            case 'todos.create':
                ir = { ...base, intent: 'todos.create', entities: { title: call.arguments.title, columnKey: lane.key } };
                break;
            case 'todos.move':
                ir = { ...base, intent: 'todos.move', entities: { localId: todo.localId, toColumnKey: lane.key } };
                break;
            case 'todos.rename':
                ir = { ...base, intent: 'todos.update_title', entities: { localId: todo.localId, title: call.arguments.title } };
                break;
            case 'todos.assign':
                ir = { ...base, intent: 'todos.assign', entities: { localId: todo.localId, assigneeUserId: member.userId } };
                break;
            case 'todos.unassign':
                ir = { ...base, intent: 'todos.unassign', entities: { localId: todo.localId, assigneeUserId: null } };
                break;
            case 'todos.delete':
                ir = { ...base, intent: 'todos.delete', entities: { localId: todo.localId } };
                break;
            case 'todos.append_notes':
            case 'todos.replace_notes': {
                const notes = call.arguments.text;
                const old = todo.body ?? '';
                const body = call.skill === 'todos.replace_notes' || !old ? notes : `${old}${old.endsWith('\n') ? '' : '\n'}${notes}`;
                ir = { ...base, intent: call.skill, entities: { localId: todo.localId, notes, body } };
                break;
            }
            case 'todos.add_tag':
            case 'todos.remove_tag':
                ir = { ...base, intent: call.skill, entities: { localId: todo.localId, tag: tag, tags: call.skill === 'todos.add_tag' ? [...(todo.tags ?? []), tag] : (todo.tags ?? []).filter(name => normalizeLookup(name) !== normalizeLookup(tag)) } };
                break;
            default: throw new AgentProtocolError('Invalid mutation');
        }
        const validated = validateCommandIR(ir, context);
        if (isCommandFailure(validated))
            return fail('invalid');
        const command = { ir: validated.value, storyTitle: todo?.title, statusName: lane?.name, assigneeName: member?.name, danger: classifyVoiceCommandSafety(validated.value).danger, requiresConfirmation: true, summary: '', confirmLabel: '' };
        Object.assign(command, formatResolvedCommand(command));
        if (call.skill === 'todos.create')
            command.summary = `${command.summary} · ${lane.name}`;
        // Precondition contains only the fields relevant to this effect and remains application-private.
        const before = !todo ? null : call.skill.includes('notes') ? todo.body ?? '' : call.skill.includes('tag') ? todo.tags ?? [] : call.skill === 'todos.rename' ? todo.title : call.skill === 'todos.move' ? this.lane(todo, context.board) : call.skill.includes('assign') ? todo.assigneeUserId ?? null : todo;
        return { result: { status: 'prepared', proposalRef: '', summary: short(command.summary, 500) }, prepared: { call: boundCall, command, fingerprint: JSON.stringify({ ir: validated.value, before, title: todo?.title, lane: lane?.name, member: member?.name }) } };
    }
    async preflight(prepared, task, signal, phase = 'initial') {
        const context = this.context(signal);
        if (!canRunVoiceMutationInContext(context))
            throw new AgentProtocolError('Permission denied');
        const result = await this.resolve(prepared.call, task, signal);
        if (result.prepared)
            task.diagnostic?.command(result.prepared.command, phase);
        else
            task.diagnostic?.emit('resolve', { phase, result: result.result.status });
        if (!result.prepared || result.prepared.fingerprint !== prepared.fingerprint)
            throw new AgentProtocolError('Proposal changed; start again');
        return result.prepared;
    }
    async commit(prepared, signal) {
        const context = this.context(signal);
        if (!canRunVoiceMutationInContext(context))
            throw new AgentProtocolError('Permission denied');
        await this.execute(prepared.command.ir, { signal, recordMutation: this.options.recordMutation });
    }
}
export function renderAgentSkillResult(result) {
    switch (result.status) {
        case 'count': return voiceText(result.count === 1 ? 'voice.info.completedThisWeekOne' : 'voice.info.completedThisWeek', result.count === 1 ? '1 story was completed this week.' : '{count} stories were completed this week.', { count: result.count });
        case 'opened':
        case 'resolved': return `#${result.number} · ${result.title} · ${result.lane}`;
        case 'inspected': return result.facts.map(fact => fact.value).join(' · ');
        case 'prepared': return result.summary;
        case 'choices': return `${voiceText('voice.prompt.whichOne', 'Which one?')} ${result.choices.map(choice => `${choice.number ? `#${choice.number} · ` : ''}${choice.label}${choice.lane ? ` · ${choice.lane}` : ''}`).join('; ')}`;
        case 'no_op': return voiceText('voice.agent.noChanges', 'No changes needed.');
        case 'not_found': return result.resource === 'member' ? voiceText('voice.errors.assigneeNotFound', 'Assignee was not found in this project.')
            : result.resource === 'tag' ? voiceText('voice.errors.tagNotFound', 'Tag was not found in this project.')
                : result.resource === 'lane' ? voiceText('voice.errors.statusNotFound', 'Status was not found on this board.')
                    : voiceText('voice.errors.noStrongTitleMatch', 'No strong todo title match was found in this project.');
        case 'denied': return voiceText('voice.errors.unauthorizedMutation', 'Only maintainers can run mutating commands.');
        case 'stale': return voiceText('voice.errors.staleContext', 'The board changed before the command could run.');
        default: return voiceText('voice.agent.safeFailure', 'I could not finish that safely. Please try again.');
    }
}
