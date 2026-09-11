import { describe, expect, it } from 'vitest';
import { agentRepairInstruction, agentStateGuidance, ALLOWED_ENVELOPE_KINDS, allowedEnvelopeKinds, completeSkillClarification, interpretAgentEnvelope, parseAgentEnvelope, type AgentState, type AgentStateKind } from './agent-protocol.js';
import { VOICE_AGENT_PROMPT, VOICE_AGENT_PROMPT_VERSION } from './agent-prompt.js';
const idle: AgentState = { kind: 'idle' };
const clarification: AgentState = { kind: 'clarification' };
const choice: AgentState = { kind: 'choice' };
const proposalsReady: AgentState = { kind: 'proposals_ready', proposalCount: 1 };
const confirmation: AgentState = { kind: 'confirmation', proposalCount: 1 };
const envelopeFor = (kind: string) => kind === 'skill_call' ? '{"kind":"skill_call","skill":"todos.resolve","arguments":{"reference":"x"}}'
  : kind === 'clarify_skill' ? '{"kind":"clarify_skill","skill":"todos.move","arguments":{"lane":"Done"},"missing":"reference","text":"Which story?"}'
  : kind === 'ask_user' ? '{"kind":"ask_user","text":"Which lane?"}' : JSON.stringify({ kind });
describe('voice-agent-v13 protocol', () => {
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
  it('accepts only strictly partial todos.move clarifications and completes the declared slot', () => {
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
  });
  it.each([
    '{"kind":"clarify_skill","skill":"todos.open","arguments":{},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"lane":"Done","reference":"x"},"missing":"reference","text":"Which story?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x","todoRef":"todo_1"},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x","extra":"y"},"missing":"lane","text":"Which lane?"}',
    '{"kind":"clarify_skill","skill":"todos.move","arguments":{"reference":"x"},"missing":"member","text":"Which member?"}',
  ])('rejects malformed or over-authoritative skill clarification %s', raw => {
    expect(() => parseAgentEnvelope(raw, idle)).toThrow();
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
    expect(VOICE_AGENT_PROMPT_VERSION).toBe('voice-agent-v13');
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
  it('requires structured partial moves and denies picker authority to plain ask_user', () => {
    for (const text of ['clarify_skill', 'missing reference requires arguments with lane only', 'missing lane requires arguments with exactly reference or todoRef', 'Plain ask_user never creates an entity picker']) {
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
