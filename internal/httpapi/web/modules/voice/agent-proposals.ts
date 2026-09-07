import { AGENT_LIMITS, AgentProtocolError } from './agent-protocol.js';
import type { AgentSkillContext, PreparedSkill, VoiceAgentSkillRegistry } from './agent-skills.js';
import type { CommandIR } from './schema.js';

function affectedField(ir: CommandIR): string {
  if (ir.intent.includes('notes')) return 'notes';
  if (ir.intent.includes('tag')) return 'tags';
  if (ir.intent.includes('assign')) return 'assignee';
  return ir.intent;
}
export type BatchExecution = { succeeded: string[]; failed: string | null; unattempted: string[]; refreshFailed: boolean };
export class VoiceAgentProposalStore {
  private proposals: PreparedSkill[] = [];
  private consumed = false;
  get count(): number { return this.proposals.length; }
  summaries(): string[] { return this.proposals.map(proposal => proposal.command.summary); }
  get danger(): boolean { return this.proposals.some(proposal => proposal.command.danger); }
  add(value: PreparedSkill): string {
    if (this.consumed || this.count >= AGENT_LIMITS.proposals) throw new AgentProtocolError('Proposal limit');
    const ir = value.command.ir;
    for (const prior of this.proposals) {
      const other = prior.command.ir;
      if (ir.intent === 'todos.create' || other.intent === 'todos.create') throw new AgentProtocolError('Create dependencies require a separate task');
      if ('localId' in ir.entities && 'localId' in other.entities && ir.entities.localId === other.entities.localId
        && (ir.intent === 'todos.delete' || other.intent === 'todos.delete' || affectedField(ir) === affectedField(other))) throw new AgentProtocolError('Conflicting proposal; cancel and start again');
    }
    // No references to caller-owned objects survive preparation.
    const copy: PreparedSkill = JSON.parse(JSON.stringify(value));
    Object.freeze(copy.command.ir.entities); Object.freeze(copy.command.ir); Object.freeze(copy.command);
    Object.freeze(copy.call.arguments); Object.freeze(copy.call); Object.freeze(copy);
    this.proposals.push(copy);
    return `proposal_${this.count}`;
  }
  async preflight(registry: VoiceAgentSkillRegistry, task: AgentSkillContext, signal: AbortSignal): Promise<PreparedSkill[]> {
    if (this.consumed || !this.count) throw new AgentProtocolError('No pending proposals');
    await registry.options.refreshBoard();
    registry.context(signal);
    const fresh: PreparedSkill[] = [];
    for (const proposal of this.proposals) fresh.push(await registry.preflight(proposal, task, signal));
    return fresh;
  }
  async confirm(registry: VoiceAgentSkillRegistry, task: AgentSkillContext, signal: AbortSignal): Promise<BatchExecution> {
    // All re-resolution, permission, validation and precondition checks precede the FIRST mutation.
    const fresh = await this.preflight(registry, task, signal);
    this.consumed = true;
    const result: BatchExecution = { succeeded: [], failed: null, unattempted: [], refreshFailed: false };
    for (let index = 0; index < fresh.length; index++) {
      const proposal = fresh[index];
      try {
        await registry.commit(proposal, signal);
        result.succeeded.push(proposal.command.summary);
        const ir = proposal.command.ir;
        if ('localId' in ir.entities && ir.intent !== 'todos.delete') {
          const args = proposal.call.arguments;
          if ('todoRef' in args && args.todoRef) task.session.activeTodo = task.handles.get(args.todoRef, 'todo');
        } else if (ir.intent === 'todos.delete' && task.session.activeTodo?.localId === ir.entities.localId) task.session.activeTodo = null;
      } catch {
        result.failed = proposal.command.summary;
        result.unattempted = fresh.slice(index + 1).map(proposal => proposal.command.summary);
        break;
      }
    }
    if (result.succeeded.length || result.failed) {
      try { await registry.options.refreshBoard(); } catch { result.refreshFailed = true; }
    }
    return result;
  }
  clear(): void { this.proposals = []; this.consumed = true; }
}
