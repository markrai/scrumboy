// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { createVoiceAgentController } from './local-agent-controller.js';
import { harness, skill, finish, latestRef } from './agent.test.utils.js';
import { SpeechInputError } from '../platform/speech-input.js';

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
