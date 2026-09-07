import { describe, expect, it } from 'vitest';
import { parseAgentEnvelope } from './agent-protocol.js';
import { VOICE_AGENT_PROMPT, VOICE_AGENT_PROMPT_VERSION } from './agent-prompt.js';
describe('voice-agent-v9 protocol', () => {
  it.each([
    'not JSON', '[]', 'null', '{}', '{"kind":"finish","text":"Done"}',
    'Here: {"kind":"finish"}', '{"kind":"skill_call","skill":"ui.birds_eye_view","arguments":{}}',
    '{"kind":"skill_call","skill":"todos.open","arguments":{"localId":355}}',
    '{"kind":"skill_call","skill":"todos.open","arguments":{"reference":"x","todoRef":"todo_1"}}',
    '{"kind":"skill_call","skill":"todos.create","arguments":{"title":"x","projectId":1}}',
    '{"kind":"skill_call","skill":"todos.inspect","arguments":{"reference":"x","fields":["sql"]}}',
    '{"kind":"skill_call","skill":"analytics.count_completed","arguments":{"range":"all_time"}}',
    JSON.stringify({ kind: 'ask_user', text: 'x'.repeat(321) }),
    JSON.stringify({ kind: 'skill_call', skill: 'todos.rename', arguments: { reference: 'x', title: 'x'.repeat(201) } }),
    JSON.stringify({ kind: 'skill_call', skill: 'todos.append_notes', arguments: { reference: 'x', text: 'x'.repeat(1001) } }),
  ])('rejects invalid envelope %s', raw => expect(() => parseAgentEnvelope(raw, false)).toThrow());
  it.each(['confirm', 'decline', 'cancel'])('%s requires confirmation', kind => {
    expect(() => parseAgentEnvelope(JSON.stringify({ kind }), false)).toThrow();
    expect(parseAgentEnvelope(JSON.stringify({ kind }), true)).toEqual({ kind });
  });
  it('accepts one optional whole JSON fence', () => expect(parseAgentEnvelope('```json\n{"kind":"finish"}\n```', false)).toEqual({ kind: 'finish' }));
  it('conditions literal domain titles and finite authority', () => {
    expect(VOICE_AGENT_PROMPT_VERSION).toBe('voice-agent-v9');
    for (const text of ["Bird's Eye View", 'Settings', 'Search', 'story called X', 'card named X', 'PREPARE', 'Only the skills']) expect(VOICE_AGENT_PROMPT).toContain(text);
    expect(VOICE_AGENT_PROMPT.length).toBeLessThan(8192);
  });
});
