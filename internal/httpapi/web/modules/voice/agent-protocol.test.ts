import { describe, expect, it } from 'vitest';
import { AgentProtocolError, agentRepairInstruction, agentStateGuidance, ALLOWED_ENVELOPE_KINDS, allowedEnvelopeKinds, completeSkillClarification, interpretAgentEnvelope, parseAgentEnvelope, type AgentState, type AgentStateKind } from './agent-protocol.js';
import { VOICE_AGENT_PROMPT, VOICE_AGENT_PROMPT_VERSION } from './agent-prompt.js';
const idle: AgentState = { kind: 'idle' };
const clarification: AgentState = { kind: 'clarification' };
const choice: AgentState = { kind: 'choice' };
const proposalsReady: AgentState = { kind: 'proposals_ready', proposalCount: 1 };
const confirmation: AgentState = { kind: 'confirmation', proposalCount: 1 };
const envelopeFor = (kind: string) => kind === 'skill_call' ? '{"kind":"skill_call","skill":"todos.resolve","arguments":{"reference":"x"}}'
  : kind === 'clarify_skill' ? '{"kind":"clarify_skill","skill":"todos.move","arguments":{"lane":"Done"},"missing":"reference","text":"Which story?"}'
  : kind === 'ask_user' ? '{"kind":"ask_user","text":"Which lane?"}' : JSON.stringify({ kind });
function rejected(raw: string): AgentProtocolError {
  try { parseAgentEnvelope(raw, idle); }
  catch (error) {
    expect(error).toBeInstanceOf(AgentProtocolError);
    return error as AgentProtocolError;
  }
  throw new Error('Expected protocol rejection');
}
describe('voice-agent-v15 protocol', () => {
  it('still accepts a valid named delete envelope', () => {
    expect(parseAgentEnvelope('{"kind":"skill_call","skill":"todos.delete","arguments":{"reference":"Billy Mongoose"}}', idle)).toEqual({
      kind: 'skill_call', skill: 'todos.delete', arguments: { reference: 'Billy Mongoose' },
    });
  });
  it('recovers a delete clarification that already contains a valid reference', () => {
    expect(interpretAgentEnvelope(JSON.stringify({ kind: 'clarify_skill', skill: 'todos.delete', arguments: { reference: 'Billy Mongoose' }, missing: 'confirmation', text: 'Are you sure?' }), idle)).toEqual({
      envelope: { kind: 'skill_call', skill: 'todos.delete', arguments: { reference: 'Billy Mongoose' } },
      recoveredFrom: 'delete_clarification_with_target',
    });
  });
  it('recovers a delete clarification that already contains a valid todoRef without trusting missing', () => {
    expect(interpretAgentEnvelope(JSON.stringify({ kind: 'clarify_skill', skill: 'todos.delete', arguments: { todoRef: 'todo_369' }, missing: 'approval', text: 'Please confirm.' }), idle)).toEqual({
      envelope: { kind: 'skill_call', skill: 'todos.delete', arguments: { todoRef: 'todo_369' } },
      recoveredFrom: 'delete_clarification_with_target',
    });
  });
  describe('safe structural rejection diagnostics', () => {
    it('reports an unexpected top-level field without its values or raw JSON', () => {
      const raw = '{"kind":"skill_call","skill":"todos.delete","arguments":{"reference":"Billy Mongoose"},"danger":true}';
      const error = rejected(raw);
      expect(error.message).toBe('Unexpected or missing fields');
      expect(error.diagnostic).toEqual({
        protocolScope: 'envelope',
        protocolEnvelopeKind: 'skill_call',
        protocolSkill: 'todos.delete',
        protocolTopLevelKeys: ['arguments', 'danger', 'kind', 'skill'],
        protocolArgumentKeys: ['reference'],
        protocolAllowedKeys: ['arguments', 'kind', 'skill'],
        protocolUnexpectedKeys: ['danger'],
      });
      expect(JSON.stringify(error.diagnostic)).not.toContain('Billy Mongoose');
      expect(JSON.stringify(error.diagnostic)).not.toContain(raw);
    });
    it('reports wrong and missing delete argument keys without the title value', () => {
      const error = rejected('{"kind":"skill_call","skill":"todos.delete","arguments":{"title":"Billy Mongoose"}}');
      expect(error.diagnostic).toMatchObject({
        protocolScope: 'arguments',
        protocolArgumentKeys: ['title'],
        protocolAllowedKeys: ['reference', 'todoRef'],
        protocolUnexpectedKeys: ['title'],
        protocolMissingKeys: ['reference|todoRef'],
      });
      expect(JSON.stringify(error.diagnostic)).not.toContain('Billy Mongoose');
    });
    it('reports an extra delete argument by field name only', () => {
      const error = rejected('{"kind":"skill_call","skill":"todos.delete","arguments":{"reference":"Billy Mongoose","confirm":true}}');
      expect(error.diagnostic).toMatchObject({
        protocolScope: 'arguments',
        protocolArgumentKeys: ['confirm', 'reference'],
        protocolUnexpectedKeys: ['confirm'],
      });
      expect(JSON.stringify(error.diagnostic)).not.toContain('Billy Mongoose');
    });
    it('keeps reference plus todoRef contradictory and identifies only the conflicting fields', () => {
      const error = rejected('{"kind":"skill_call","skill":"todos.delete","arguments":{"reference":"Billy Mongoose","todoRef":"todo_private"}}');
      expect(error.message).toBe('Supply reference OR todoRef');
      expect(error.diagnostic).toMatchObject({
        protocolScope: 'arguments',
        protocolArgumentKeys: ['reference', 'todoRef'],
        protocolConflictKeys: ['reference', 'todoRef'],
      });
      expect(JSON.stringify(error.diagnostic)).not.toContain('Billy Mongoose');
      expect(JSON.stringify(error.diagnostic)).not.toContain('todo_private');
    });
    it('distinguishes missing top-level fields, wrong skills, invalid kinds and clarification arguments', () => {
      const missingTopLevel = rejected('{"kind":"skill_call","skill":"todos.delete"}');
      expect(missingTopLevel.diagnostic).toMatchObject({
        protocolScope: 'envelope', protocolMissingKeys: ['arguments'], protocolTopLevelKeys: ['kind', 'skill'],
      });

      const wrongSkill = rejected('{"kind":"skill_call","skill":"todos.destroy","arguments":{"reference":"PRIVATE_VALUE"}}');
      expect(wrongSkill.diagnostic).toMatchObject({ protocolScope: 'envelope', protocolSkill: 'todos.destroy' });

      const invalidKind = rejected('{"kind":"execute","skill":"todos.delete","arguments":{"reference":"PRIVATE_VALUE"}}');
      expect(invalidKind.diagnostic).toMatchObject({ protocolScope: 'envelope', protocolEnvelopeKind: 'execute' });

      const invalidClarification = rejected('{"kind":"clarify_skill","skill":"todos.delete","arguments":{"title":"PRIVATE_VALUE"},"missing":"reference","text":"Which story?"}');
      expect(invalidClarification.diagnostic).toMatchObject({
        protocolScope: 'clarification_arguments', protocolArgumentKeys: ['title'], protocolUnexpectedKeys: ['title'],
      });
      expect(JSON.stringify({ missingTopLevel: missingTopLevel.diagnostic, wrongSkill: wrongSkill.diagnostic, invalidKind: invalidKind.diagnostic, invalidClarification: invalidClarification.diagnostic })).not.toContain('PRIVATE_VALUE');
    });
    it('bounds structural key counts and lengths', () => {
      const argumentsValue = Object.fromEntries(Array.from({ length: 20 }, (_, index) => [`unexpectedArgumentField${index}${'x'.repeat(80)}`, true]));
      const error = rejected(JSON.stringify({ kind: 'skill_call', skill: 'todos.delete', arguments: argumentsValue }));
      for (const values of Object.values(error.diagnostic).filter(Array.isArray) as string[][]) {
        expect(values.length).toBeLessThanOrEqual(12);
        expect(values.every(value => value.length <= 48)).toBe(true);
      }
    });
  });
  it.each([
    'not JSON', '[]', 'null', '{}',
    'Here: {"kind":"finish"}', '{"kind":"skill_call","skill":"ui.birds_eye_view","arguments":{}}',
    '{"kind":"skill_call","skill":"todos.open","arguments":{"localId":355}}',
    '{"kind":"skill_call","skill":"todos.open","arguments":{"reference":"x","todoRef":"todo_1"}}',
    '{"kind":"skill_call","skill":"todos.create","arguments":{"title":"x","projectId":1}}',
    '{"kind":"skill_call","skill":"todos.inspect","arguments":{"reference":"x","fields":["sql"]}}',
    '{"kind":"skill_call","skill":"analytics.count_completed","arguments":{"range":"all_time"}}',
    JSON.stringify({ kind: 'ask_user', text: 'x'.repeat(321) }),
    JSON.stringify({ kind: 'skill_call', skill: 'todos.rename', arguments: { reference: 'x', title: 'x'.repeat(201) } }),
    JSON.stringify({ kind: 'skill_call', skill: 'todos.append_notes', arguments: { reference: 'x', text: 'x'.repeat(1001) } }),
  ])('rejects invalid envelope %s', raw => expect(() => parseAgentEnvelope(raw, idle)).toThrow());
  it.each(['confirm', 'decline', 'cancel'])('%s is named in the rejection outside confirmation', kind => {
    for (const state of [idle, clarification, choice]) {
      expect(() => parseAgentEnvelope(JSON.stringify({ kind }), state)).toThrow(`Envelope ${kind} is not allowed in state ${state.kind}`);
    }
    expect(parseAgentEnvelope(JSON.stringify({ kind }), confirmation)).toEqual({ kind });
  });
  it('recovers a bare confirm in proposals_ready as finish without making confirm legal', () => {
    expect(ALLOWED_ENVELOPE_KINDS.proposals_ready).toEqual(['skill_call', 'clarify_skill', 'ask_user', 'finish']);
    expect(interpretAgentEnvelope('{"kind":"confirm"}', proposalsReady)).toEqual({ envelope: { kind: 'finish' }, recoveredFrom: 'confirm' });
    expect(parseAgentEnvelope('{"kind":"confirm"}', proposalsReady)).toEqual({ kind: 'finish' });
    expect(interpretAgentEnvelope('{"kind":"finish"}', proposalsReady)).toEqual({ envelope: { kind: 'finish' } });
  });
  it.each(['idle', 'clarification', 'choice'] as const)('does not recover confirm in %s', kind => {
    const state = kind === 'idle' ? idle : kind === 'clarification' ? clarification : choice;
    expect(() => interpretAgentEnvelope('{"kind":"confirm"}', state)).toThrow(`Envelope confirm is not allowed in state ${kind}`);
  });
  it('leaves confirmation-state confirm as a real confirm', () => {
    expect(interpretAgentEnvelope('{"kind":"confirm"}', confirmation)).toEqual({ envelope: { kind: 'confirm' } });
  });
  it.each(['decline', 'cancel'] as const)('does not recover %s in proposals_ready', kind => {
    expect(() => parseAgentEnvelope(JSON.stringify({ kind }), proposalsReady)).toThrow(`Envelope ${kind} is not allowed in state proposals_ready`);
  });
  it('accepts finish with a harmless bounded text and ignores it', () => {
    expect(parseAgentEnvelope('{"kind":"finish","text":"Done"}', proposalsReady)).toEqual({ kind: 'finish', text: 'Done' });
    expect(parseAgentEnvelope('{"kind":"finish"}', idle)).toEqual({ kind: 'finish' });
  });
  it.each([
    '{"kind":"finish","summary":"Done"}', '{"kind":"finish","text":5}',
    JSON.stringify({ kind: 'finish', text: 'x'.repeat(321) }),
  ])('still rejects unknown or unbounded finish fields %s', raw => expect(() => parseAgentEnvelope(raw, proposalsReady)).toThrow());
  it('accepts one optional whole JSON fence', () => expect(parseAgentEnvelope('```json\n{"kind":"finish"}\n```', idle)).toEqual({ kind: 'finish' }));
  it('accepts only strictly partial move/delete clarifications and completes the declared slot', () => {
    const missingReference = parseAgentEnvelope('{"kind":"clarify_skill","skill":"todos.move","arguments":{"lane":"Done"},"missing":"reference","text":"Which story?"}', idle);
    expect(missingReference).toMatchObject({ kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference' });
    if (missingReference.kind !== 'clarify_skill') throw new Error('Expected skill clarification');
    expect(completeSkillClarification(missingReference, '#369')).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { lane: 'Done', reference: '#369' },
    });

    const missingLane = parseAgentEnvelope('{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"Goblins in Washington"},"missing":"lane","text":"Which lane?"}', idle);
    expect(missingLane).toMatchObject({ kind: 'clarify_skill', arguments: { reference: 'Goblins in Washington' }, missing: 'lane' });
    if (missingLane.kind !== 'clarify_skill') throw new Error('Expected skill clarification');
    expect(completeSkillClarification(missingLane, 'Done')).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: 'Goblins in Washington', lane: 'Done' },
    });

    const missingDeleteReference = parseAgentEnvelope('{"kind":"clarify_skill","skill":"todos.delete","arguments":{},"missing":"reference","text":"Which story?"}', idle);
    expect(missingDeleteReference).toMatchObject({ kind: 'clarify_skill', skill: 'todos.delete', arguments: {}, missing: 'reference' });
    if (missingDeleteReference.kind !== 'clarify_skill') throw new Error('Expected skill clarification');
    expect(completeSkillClarification(missingDeleteReference, '#369')).toEqual({
      kind: 'skill_call', skill: 'todos.delete', arguments: { reference: '#369' },
    });
  });
  it.each([
    '{"kind":"clarify_skill","skill":"todos.open","arguments":{},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"lane":"Done","reference":"x"},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x","todoRef":"todo_1"},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x","extra":"y"},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x"},"missing":"member","text":"Which member?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"extra":"x"},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"reference":"x","todoRef":"todo_1"},"missing":"confirmation","text":"Are you sure?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"reference":"x","confirm":true},"missing":"confirmation","text":"Are you sure?"}',
  ])('rejects malformed or over-authoritative skill clarification %s', raw => {
    expect(() => parseAgentEnvelope(raw, idle)).toThrow();
  });
  it.each([
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"reference":""},"missing":"confirmation","text":"Are you sure?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"reference":"   "},"missing":"confirmation","text":"Are you sure?"}',
    '{"kind":"clarify_skill","skill":"todos.delete","arguments":{"todoRef":""},"missing":"approval","text":"Are you sure?"}',
  ])('rejects a delete clarification with an empty target %s', raw => expect(() => parseAgentEnvelope(raw, idle)).toThrow('Invalid bounded string'));
  it('does not recover a clarification belonging to another skill', () => {
    const move = interpretAgentEnvelope('{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x"},"missing":"lane","text":"Which lane?"}', idle);
    expect(move).toEqual({ envelope: { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: 'x' }, missing: 'lane', text: 'Which lane?' } });
    expect(() => parseAgentEnvelope('{"kind":"clarify_skill","skill":"todos.open","arguments":{"reference":"x"},"missing":"confirmation","text":"Are you sure?"}', idle)).toThrow('Unsupported skill clarification');
  });
  describe('state-aware envelope legality', () => {
    it.each([
      ['idle', idle], ['clarification', clarification], ['choice', choice],
      ['proposals_ready', proposalsReady], ['confirmation', confirmation],
    ])('accepts exactly the allowed kinds in %s', (_name, state) => {
      for (const kind of ['skill_call', 'clarify_skill', 'ask_user', 'finish', 'confirm', 'decline', 'cancel']) {
        if (state.kind === 'proposals_ready' && kind === 'confirm') {
          expect(parseAgentEnvelope(envelopeFor(kind), state)).toEqual({ kind: 'finish' });
          continue;
        }
        const allowed = allowedEnvelopeKinds(state).includes(kind as never);
        if (allowed) expect(parseAgentEnvelope(envelopeFor(kind), state)).toMatchObject({ kind });
        else expect(() => parseAgentEnvelope(envelopeFor(kind), state)).toThrow(`Envelope ${kind} is not allowed in state ${state.kind}`);
      }
    });
    it('never allows confirmation actions outside confirmation', () => {
      for (const [name, kinds] of Object.entries(ALLOWED_ENVELOPE_KINDS)) {
        for (const kind of ['confirm', 'decline', 'cancel']) expect(kinds.includes(kind as never)).toBe(name === 'confirmation');
      }
    });
    it('does not let finish bypass an unresolved choice', () => {
      expect(() => parseAgentEnvelope('{"kind":"finish"}', choice)).toThrow('Envelope finish is not allowed in state choice');
    });
    it('does not let finish close an unanswered clarification', () => {
      expect(() => parseAgentEnvelope('{"kind":"finish"}', clarification)).toThrow('Envelope finish is not allowed in state clarification');
      expect(() => parseAgentEnvelope('{"kind":"finish","text":"Done"}', clarification)).toThrow('Envelope finish is not allowed in state clarification');
    });
    it('does not allow ask_user or finish while confirmation is pending', () => {
      expect(ALLOWED_ENVELOPE_KINDS.confirmation).toEqual(['skill_call', 'confirm', 'decline', 'cancel']);
      expect(() => parseAgentEnvelope('{"kind":"ask_user","text":"Which lane?"}', confirmation)).toThrow('Envelope ask_user is not allowed in state confirmation');
      expect(() => parseAgentEnvelope('{"kind":"finish"}', confirmation)).toThrow('Envelope finish is not allowed in state confirmation');
    });
    it('allows skill_call in every state so compound work is never blocked', () => {
      for (const kinds of Object.values(ALLOWED_ENVELOPE_KINDS)) expect(kinds).toContain('skill_call');
    });
  });
  describe('state-aware repair', () => {
    it.each(['idle', 'clarification', 'choice', 'proposals_ready', 'confirmation'] as AgentStateKind[])('names the %s state and its permitted kinds', kind => {
      const guidance = agentStateGuidance({ kind, proposalCount: 2 });
      expect(guidance).toContain(`Current state: ${kind}`);
      expect(guidance).toContain(`Allowed envelope kinds: ${ALLOWED_ENVELOPE_KINDS[kind].join(', ')}`);
    });
    it('reports proposal counts and keeps rejected model output out of the repair', () => {
      const repair = agentRepairInstruction(proposalsReady, 'Unexpected or missing fields');
      expect(repair).toContain('Previous response violated protocol: Unexpected or missing fields');
      expect(repair).toContain('Current state: proposals_ready');
      expect(repair).toContain('1 mutation(s) are prepared');
      expect(repair).toContain('Allowed envelope kinds: skill_call, clarify_skill, ask_user, finish');
      expect(repair).not.toContain('{');
    });
    it('tells clarification replies apart from protocol actions', () => {
      const repair = agentRepairInstruction(clarification, 'Envelope confirm is not allowed in state clarification');
      expect(repair).toContain('Current state: clarification');
      expect(repair).toContain('answers the pending question');
      expect(repair).toContain('Allowed envelope kinds: skill_call, clarify_skill, ask_user');
      expect(repair).not.toContain('finish');
    });
  });
  it('conditions literal domain titles and finite authority', () => {
    expect(VOICE_AGENT_PROMPT_VERSION).toBe('voice-agent-v15');
    for (const text of ["Bird's Eye View", 'Settings', 'Search', 'story called X', 'card named X', 'PREPARE', 'Only the skills']) expect(VOICE_AGENT_PROMPT).toContain(text);
    expect(VOICE_AGENT_PROMPT.length).toBeLessThan(8192);
  });
  it('routes single named-story discovery to open without treating collections as one story', () => {
    for (const text of ['find X', 'find me X', 'search for X', 'look up X', 'todos.open with X as reference', 'not todos.resolve', 'Find me the Goblin story', 'Look up Goblin']) expect(VOICE_AGENT_PROMPT).toContain(text);
    for (const text of ['all or multiple todos', 'tag, assignee or text filter', 'Find all Goblin stories is not todos.open', 'No collection-search skill exists']) expect(VOICE_AGENT_PROMPT).toContain(text);
  });
  it('routes mark/set/change status wording directly to todos.move with a bare reference', () => {
    for (const text of ['Move Goblins in Washington to Done', 'Mark Goblins in Washington as Done', 'Mark the story Goblins in Washington as Done', 'Mark the story Goblins in Washington done', 'Set Goblins in Washington to Done', 'Change Goblins in Washington to Done', 'Change the status of Goblins in Washington to Done', 'Mark the story "Goblins in Washington" as "Done"', '"reference":"Goblins in Washington"', '"lane":"Done"', 'do not call todos.resolve first']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
    for (const text of ['never ask for a more specific title or identifier', 'pass only the literal entity reference', 'Nano never needs board contents to call todos.move']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
  });
  it('routes named delete wording directly without opening or resolving first', () => {
    for (const text of ['Delete Billy Mongoose', 'Delete the story Billy Mongoose', 'Delete the story called Billy Mongoose', 'Remove Billy Mongoose', 'Remove the story Billy Mongoose', 'I want you to delete Billy Mongoose', 'Please delete Billy Mongoose', 'Get rid of Billy Mongoose', '"skill":"todos.delete"', '"reference":"Billy Mongoose"', 'do not call todos.open, todos.resolve or inspect']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
    for (const text of ['remove tag, assignee or notes are field changes', 'todos.delete","arguments":{},"missing":"reference"', 'only absent reference uses {} + missing reference']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
  });
  it('makes Scrumboy, not the agent, authoritative for delete confirmation', () => {
    for (const text of ['Target known:', 'Target absent:', 'Never ask "Are you sure?"', 'Scrumboy owns destructive confirmation', 'never for confirmation, approval, permission or another id']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
  });
  it('requires structured partial moves and denies picker authority to plain ask_user', () => {
    for (const text of ['clarify_skill', 'todos.move: missing reference uses lane', 'missing lane uses reference or todoRef', 'Plain ask_user is conversational', 'never creates entity picker']) {
      expect(VOICE_AGENT_PROMPT).toContain(text);
    }
  });
  it('treats numbered and labeled choice replies as handle selection rather than confirmation', () => {
    for (const text of ['pending.kind choice', '#374', 'the Washington one', 'matching offered handle', 'Never emit confirm for a choice reply']) expect(VOICE_AGENT_PROMPT).toContain(text);
  });
  it('derives the prompt state contract from the parser table', () => {
    for (const [state, kinds] of Object.entries(ALLOWED_ENVELOPE_KINDS)) expect(VOICE_AGENT_PROMPT).toContain(`${state}: ${kinds.join(', ')}`);
  });
});
