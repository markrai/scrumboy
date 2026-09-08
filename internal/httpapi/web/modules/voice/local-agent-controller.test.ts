// @vitest-environment happy-dom
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { createVoiceAgentController } from './local-agent-controller.js';
import { harness, skill, finish, latestRef } from './agent.test.utils.js';
import { SpeechInputError } from '../platform/speech-input.js';
import { SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS } from '../platform/speech-output.js';

function surface(h: ReturnType<typeof harness>, transcripts: string[], keepListening = false) {
  let speaking = false;
  const speechOutput = { status: vi.fn(async () => ({ state: 'ready' as const })),
    speak: vi.fn(async () => { speaking = true; await Promise.resolve(); speaking = false; return { completed: true as const }; }),
    stop: vi.fn(async () => { speaking = false; }), invalidate: vi.fn(async () => { speaking = false; }) };
  const speechInput = { status: vi.fn(async () => ({ state: 'ready' as const })), listen: vi.fn(async options => {
    expect(speaking).toBe(false); expect(options.maxDurationMs).toBe(10000); options.onListening?.();
    const transcript = transcripts.shift(); if (!transcript) throw new SpeechInputError('no_speech'); return { transcript };
  }) };
  const onView = vi.fn();
  const controller = createVoiceAgentController({ ...h.options, model: h.model, loop: h.loop, speechInput, speechOutput, continuationEnabled: keepListening, onView });
  return { controller, speechInput, speechOutput, onView };
}
describe('VoiceAgentController local skill production path', () => {
  it('solicits yes only after every effect in a >600-character visual batch has been spoken', async () => {
    const notes = 'Long dictated paragraph. '.repeat(32);
    const h = harness([
      skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }),
      skill('todos.assign', { reference: 'Happy Birthday', member: 'Mark' }),
      skill('todos.append_notes', { reference: 'Happy Birthday', text: notes }),
      skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' }), finish, { kind: 'confirm' },
    ]);
    const s = surface(h, ['Move, assign, append the paragraph, and tag urgent', 'yeah go ahead']);
    let completeSpeech!: () => void;
    s.speechOutput.speak.mockImplementationOnce(async () => {
      await new Promise<void>(resolve => { completeSpeech = resolve; });
      return { completed: true };
    });
    const listening = s.controller.startListening();
    await vi.waitFor(() => expect(s.speechOutput.speak).toHaveBeenCalledOnce());
    const visual = s.controller.getView().confirmation!.summary;
    const spoken = s.speechOutput.speak.mock.calls[0][0].text;
    expect(visual.length).toBeGreaterThan(SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS);
    expect(visual.indexOf('urgent')).toBeGreaterThan(SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS);
    expect(visual).toContain(notes);
    expect(spoken.length).toBeLessThanOrEqual(SPEECH_OUTPUT_MAX_TEXT_CODE_UNITS);
    for (const effect of ['Move', 'Mark', 'dictated text', 'urgent']) expect(spoken).toContain(effect);
    expect(spoken).not.toContain(notes);
    expect(s.speechInput.listen).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
    completeSpeech(); await listening;
    expect(s.speechInput.listen).toHaveBeenCalledTimes(2);
    expect(h.execute).toHaveBeenCalledTimes(4);
    s.controller.close();
  });
  it.each([false, true])('does not auto-listen when the complete batch cannot fit, Keep Listening=%s', async enabled => {
    const title = 'X'.repeat(200);
    const h = harness([
      skill('todos.move', { reference: title, lane: 'Done' }),
      skill('todos.assign', { reference: title, member: 'Mark' }),
      skill('todos.append_notes', { reference: title, text: 'Paragraph. '.repeat(80) }),
      skill('todos.add_tag', { reference: title, tag: 'urgent' }), finish, { kind: 'confirm' },
    ], enabled);
    h.todo.title = title;
    const s = surface(h, ['Prepare four changes', 'yes'], enabled);
    await s.controller.startListening();
    expect(s.controller.getView().confirmation!.summary).toContain('urgent');
    expect(s.controller.getView().phase).toBe('confirmation');
    expect(s.speechOutput.speak).not.toHaveBeenCalled();
    expect(s.speechInput.listen).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
    // Explicit manual Listen remains available after visual review.
    await s.controller.startListening();
    expect(h.execute).toHaveBeenCalledTimes(4);
    s.controller.close();
  });
  it.each([false, true])('compound confirmation continues with Keep Listening %s; one next window only when enabled', async enabled => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), input => skill('todos.append_notes', { todoRef: latestRef(input), text: 'How are you?' }), finish, { kind: 'confirm' }], enabled);
    const s = surface(h, ['Open Happy Birthday and add to the notes section: How are you?', 'yeah go ahead'], enabled);
    await s.controller.startListening();
    expect(h.execute).toHaveBeenCalledOnce(); expect(s.controller.getView().phase).toBe('success');
    expect(s.speechInput.listen).toHaveBeenCalledTimes(enabled ? 3 : 2);
    expect(s.onView.mock.calls.filter(([view]) => view.phase === 'confirmation' && view.activity === 'idle')).not.toHaveLength(0);
    s.controller.close();
  });
  it('clarification opens a bounded reply window with Keep Listening off', async () => {
    const h = harness([{ kind: 'ask_user', text: 'What is the title?' }, skill('todos.open', { reference: 'Happy Birthday' }), finish]);
    const s = surface(h, ['open a todo', 'Happy Birthday']);
    await s.controller.startListening(); expect(s.speechInput.listen).toHaveBeenCalledTimes(2); expect(h.options.openTodo).toHaveBeenCalledOnce(); s.controller.close();
  });
  it('UI confirmation executes once, without needing a model call', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, []);
    await s.controller.submitTranscript('delete Happy Birthday'); expect(s.controller.getView().phase).toBe('confirmation');
    await Promise.all([s.controller.confirm(), s.controller.confirm()]); expect(h.execute).toHaveBeenCalledOnce(); expect(h.model).toHaveBeenCalledTimes(2); s.controller.close();
  });
  it('typed tasks never automatically acquire the microphone', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), finish], true); const s = surface(h, [], true);
    await s.controller.submitTranscript('open'); expect(s.speechInput.listen).not.toHaveBeenCalled(); s.controller.close();
  });
  it('preserves a pending confirmation when the bounded microphone window receives no speech', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, ['delete']);
    await s.controller.startListening(); expect(s.controller.getView().phase).toBe('confirmation'); expect(h.loop.confirmationPending).toBe(true);
    expect(s.speechInput.listen).toHaveBeenCalledTimes(2); expect(h.execute).not.toHaveBeenCalled(); s.controller.close();
  });
  it.each(['invalidate', 'close'] as const)('%s clears pending proposals, handles and session', async action => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, []);
    await s.controller.submitTranscript('delete'); s.controller[action](); await s.controller.confirm();
    expect(h.loop.pending).toBe(false); expect(h.execute).not.toHaveBeenCalled(); expect(s.speechOutput.invalidate).toHaveBeenCalled(); s.controller.close();
  });
  it('late local model completion after close cannot open or mutate', async () => {
    let resolve!: (value: string) => void; const h = harness(); h.model.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
    const s = surface(h, []); const pending = s.controller.submitTranscript('open');
    await vi.waitFor(() => expect(h.model).toHaveBeenCalledOnce()); s.controller.close(); resolve(JSON.stringify(skill('todos.open', { reference: 'Happy Birthday' }))); await pending;
    expect(h.options.openTodo).not.toHaveBeenCalled(); expect(s.controller.getView().phase).toBe('closed');
  });
  it('context drift during local generation fails closed', async () => {
    let resolve!: (value: string) => void; const h = harness(); h.model.mockImplementationOnce(() => new Promise<string>(done => { resolve = done; }));
    const s = surface(h, []); const pending = s.controller.submitTranscript('open'); await vi.waitFor(() => expect(h.model).toHaveBeenCalledOnce());
    h.setContext(null); resolve(JSON.stringify(skill('todos.open', { reference: 'Happy Birthday' }))); await pending;
    expect(h.options.openTodo).not.toHaveBeenCalled(); expect(s.controller.getView().phase).toBe('error'); s.controller.close();
  });
  it('UI cancellation clears a prepared mutation without execution', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, []);
    await s.controller.submitTranscript('delete'); s.controller.cancelConfirmation(); await vi.waitFor(() => expect(h.loop.pending).toBe(false)); expect(h.execute).not.toHaveBeenCalled(); s.controller.close();
  });
  it('invalidates an idle pending confirmation immediately on account drift', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, []);
    await s.controller.submitTranscript('delete'); h.setContext(null);
    await vi.waitFor(() => expect(h.loop.pending).toBe(false));
    expect(s.controller.getView().phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled(); s.controller.close();
  });
});

describe('production VoiceFlow traces', () => {
  let events: Record<string, any>[];
  beforeEach(() => {
    events = [];
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    vi.spyOn(console, 'debug').mockImplementation((event, details) => { if (event === 'VoiceFlow trace') events.push(details); });
  });
  afterEach(() => { localStorage.removeItem('scrumboy_debug_voiceflow'); vi.restoreAllMocks(); });
  it.each(['mlkit_genai_advanced', 'android_on_device'])('correlates safe voice execution with %s', async provider => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), finish]);
    const s = surface(h, []);
    s.speechInput.listen.mockResolvedValueOnce({ transcript: '  open Happy Birthday  ', provider } as never);
    await s.controller.startListening();
    expect(events.map(e => e.stage)).toEqual(['asr_final', 'interpret', 'resolve', 'safety', 'execute', 'execute', 'interpret', 'terminal']);
    expect(events[0]).toMatchObject({ provider, modality: 'voice', transcript: 'open Happy Birthday' });
    expect(events[3]).toMatchObject({ danger: false, reason: 'non_delete_command' });
    expect(events.at(-1)).toMatchObject({ outcome: 'success' });
    expect(new Set(events.map(e => e.op)).size).toBe(1);
    s.controller.close();
  });
  it.each(['todos.delete', 'todos.move'])('keeps typed %s pending until confirmation and traces both preflights', async name => {
    const h = harness([skill(name, { reference: 'Happy Birthday', ...(name === 'todos.move' ? { lane: 'Done' } : {}) }), finish]);
    const s = surface(h, []);
    await s.controller.submitTranscript('change Happy Birthday');
    expect(events[0]).toMatchObject({ stage: 'transcript_input', modality: 'typed' });
    expect(events.find(e => e.stage === 'safety')).toMatchObject({ danger: name === 'todos.delete', reason: name === 'todos.delete' ? 'destructive_delete' : 'non_delete_command' });
    expect(events.at(-1)).toMatchObject({ stage: 'confirmation', required: true });
    expect(h.execute).not.toHaveBeenCalled();
    await s.controller.confirm();
    expect(events.filter(e => e.stage === 'resolve').map(e => e.phase)).toEqual(['initial', 'confirmation_preflight', 'confirm_revalidation']);
    expect(events.at(-1)).toMatchObject({ outcome: 'success' });
    expect(new Set(events.map(e => e.op)).size).toBe(1);
    s.controller.close();
  });
  it('traces protocol failure without raw model output', async () => {
    const h = harness(['private invalid output', 'private invalid output']); const s = surface(h, []);
    await s.controller.submitTranscript('open');
    expect(events.map(e => e.stage)).toEqual(['transcript_input', 'interpret', 'terminal']);
    expect(events[1]).toMatchObject({ result: 'failure' });
    expect(events.at(-1)).toMatchObject({ outcome: 'interpret_failure' });
    expect(JSON.stringify(events)).not.toContain('private invalid output');
    expect(h.execute).not.toHaveBeenCalled(); s.controller.close();
  });
  it('traces questions, resolution failures, and cancellation', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Which todo?' }]); const s = surface(h, []);
    await s.controller.submitTranscript('open');
    expect(events.at(-1)).toMatchObject({ stage: 'resolve', result: 'question' });
    s.controller.cancelClarification();
    await vi.waitFor(() => expect(events.at(-1)).toMatchObject({ stage: 'terminal', outcome: 'cancelled_by_user' }));
    s.controller.close();
    const h2 = harness([skill('todos.open', { reference: 'Missing' })]); const s2 = surface(h2, []);
    await s2.controller.submitTranscript('open Missing');
    expect(events.at(-1)).toMatchObject({ outcome: 'resolution_failure', code: 'not_found' });
    expect(h2.execute).not.toHaveBeenCalled(); s2.controller.close();
  });
  it('records changed proposals and does not execute them', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, []);
    await s.controller.submitTranscript('delete Happy Birthday');
    h.todo.title = 'Changed';
    await s.controller.confirm();
    expect(events.find(e => e.stage === 'resolve' && e.phase === 'confirm_revalidation')).toBeDefined();
    expect(events.find(e => e.stage === 'failure')).toMatchObject({ reason: 'Proposal changed; start again' });
    expect(events.at(-1)).toMatchObject({ outcome: 'confirmation_failed' });
    expect(h.execute).not.toHaveBeenCalled(); s.controller.close();
  });
  it('ends invalidated input and suppresses late results', async () => {
    const h = harness([finish]); const s = surface(h, []);
    let complete!: (value: { transcript: string }) => void;
    s.speechInput.listen.mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
    const listening = s.controller.startListening();
    await vi.waitFor(() => expect(complete).toBeDefined());
    s.controller.invalidate(); complete({ transcript: 'late' }); await listening;
    expect(events.at(-1)).toMatchObject({ outcome: 'controller_invalidated' });
    expect(events.filter(e => e.stage === 'terminal')).toHaveLength(1);
    expect(h.model).not.toHaveBeenCalled(); s.controller.close();
  });
});


it('retains one production trace after microphone timeout until tap confirmation', async () => {
  const events: Record<string, any>[] = [];
  localStorage.setItem('scrumboy_debug_voiceflow', '1');
  const debug = vi.spyOn(console, 'debug').mockImplementation((name, details) => { if (name === 'VoiceFlow trace') events.push(details); });
  const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); const s = surface(h, ['delete Happy Birthday']);
  try {
    await s.controller.startListening();
    expect(events.at(-1)).toMatchObject({ stage: 'failure', source: 'speech', interactionRetained: true });
    expect(events.some(e => e.stage === 'terminal')).toBe(false);
    await s.controller.confirm();
    expect(events.at(-1)).toMatchObject({ stage: 'terminal', outcome: 'success' });
    expect(new Set(events.map(e => e.op)).size).toBe(1);
  } finally { s.controller.close(); debug.mockRestore(); localStorage.removeItem('scrumboy_debug_voiceflow'); }
});
