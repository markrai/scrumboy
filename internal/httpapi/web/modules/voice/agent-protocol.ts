export const AGENT_LIMITS = Object.freeze({ modelSteps: 8, skillCalls: 6, proposals: 4, choices: 5, resultText: 2048, ask: 320, trace: 16, utterance: 2000 });

type Target = { reference: string; todoRef?: never } | { todoRef: string; reference?: never };
export type InspectField = 'title' | 'lane' | 'assignees' | 'tags' | 'notes';
export type SkillArguments = {
  'todos.resolve': { reference: string };
  'todos.open': Target;
  'todos.inspect': Target & { fields?: InspectField[] };
  'todos.create': { title: string; lane?: string };
  'todos.move': Target & { lane: string };
  'todos.rename': Target & { title: string };
  'todos.append_notes': Target & { text: string };
  'todos.replace_notes': Target & { text: string };
  'todos.assign': Target & { member: string };
  'todos.unassign': Target & { member?: string };
  'todos.add_tag': Target & { tag: string };
  'todos.remove_tag': Target & { tag: string };
  'todos.delete': Target;
  'analytics.count_completed': { range: 'this_week' };
};
export type VoiceAgentSkillName = keyof SkillArguments;
export type SkillCall = { [K in VoiceAgentSkillName]: { kind: 'skill_call'; skill: K; arguments: SkillArguments[K] } }[VoiceAgentSkillName];
export type AgentEnvelope = SkillCall | { kind: 'ask_user'; text: string } | { kind: 'finish' | 'confirm' | 'decline' | 'cancel' };
export const SKILL_NAMES: readonly VoiceAgentSkillName[] = Object.freeze([
  'todos.resolve', 'todos.open', 'todos.inspect', 'todos.create', 'todos.move', 'todos.rename',
  'todos.append_notes', 'todos.replace_notes', 'todos.assign', 'todos.unassign', 'todos.add_tag',
  'todos.remove_tag', 'todos.delete', 'analytics.count_completed',
]);
export class AgentProtocolError extends Error {}
function invalid(reason: string): never { throw new AgentProtocolError(reason); }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid('Expected one object');
  return value as Record<string, unknown>;
}
function keys(value: Record<string, unknown>, allowed: string[], required = allowed): void {
  if (Object.keys(value).some(key => !allowed.includes(key)) || required.some(key => !(key in value))) invalid('Unexpected or missing fields');
}
function text(value: unknown, max: number): void {
  if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value)) invalid('Invalid bounded string');
}
export function parseAgentEnvelope(raw: string, confirmation: boolean): AgentEnvelope {
  if (typeof raw !== 'string' || raw.length > 8192) invalid('Output too large');
  let value: unknown;
  // The local provider already tolerates a single surrounding JSON fence.
  let source = raw.trim();
  if (source.startsWith('```json\n') && source.endsWith('\n```')) source = source.slice(8, -4);
  else if (source.startsWith('```\n') && source.endsWith('\n```')) source = source.slice(4, -4);
  try { value = JSON.parse(source); } catch { invalid('Expected strict JSON'); }
  const envelope = object(value);
  if (envelope.kind === 'ask_user') {
    keys(envelope, ['kind', 'text']); text(envelope.text, AGENT_LIMITS.ask);
  } else if (['finish', 'confirm', 'decline', 'cancel'].includes(String(envelope.kind))) {
    keys(envelope, ['kind']);
    if (envelope.kind !== 'finish' && !confirmation) invalid('Confirmation is not pending');
  } else if (envelope.kind === 'skill_call') {
    keys(envelope, ['kind', 'skill', 'arguments']);
    if (!SKILL_NAMES.includes(envelope.skill as VoiceAgentSkillName)) invalid('Unknown skill');
    const args = object(envelope.arguments);
    const skill = envelope.skill as VoiceAgentSkillName;
    if (skill === 'analytics.count_completed') {
      keys(args, ['range']); if (args.range !== 'this_week') invalid('Invalid range');
    } else if (skill === 'todos.create') {
      keys(args, ['title', 'lane'], ['title']); text(args.title, 200);
      if ('lane' in args) text(args.lane, 200);
    } else {
      if (('reference' in args) === ('todoRef' in args)) invalid('Supply reference OR todoRef');
      const target = 'reference' in args ? 'reference' : 'todoRef';
      if (skill === 'todos.resolve' && target !== 'reference') invalid('Resolve requires reference');
      text(args[target], target === 'todoRef' ? 80 : 200);
      const extra = skill === 'todos.move' ? 'lane' : skill === 'todos.rename' ? 'title'
        : ['todos.append_notes', 'todos.replace_notes'].includes(skill) ? 'text'
        : ['todos.assign', 'todos.unassign'].includes(skill) ? 'member'
        : ['todos.add_tag', 'todos.remove_tag'].includes(skill) ? 'tag' : skill === 'todos.inspect' ? 'fields' : null;
      const required = extra && !['fields'].includes(extra) && skill !== 'todos.unassign' ? [target, extra] : [target];
      keys(args, extra ? [target, extra] : [target], required);
      if (extra === 'fields' && extra in args) {
        const fields = args.fields;
        if (!Array.isArray(fields) || !fields.length || fields.length > 5 || new Set(fields).size !== fields.length
          || fields.some(field => !['title', 'lane', 'assignees', 'tags', 'notes'].includes(field))) invalid('Invalid inspect fields');
      } else if (extra && extra in args) text(args[extra], extra === 'text' ? 1000 : 200);
    }
  } else invalid('Unknown envelope kind');
  return value as AgentEnvelope;
}
