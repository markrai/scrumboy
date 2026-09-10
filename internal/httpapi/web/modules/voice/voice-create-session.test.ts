// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { harness } from './agent.test.utils.js';
import { createVoiceCreatePlanner } from './voice-create-planner.js';
import { VoiceCreateSession, voiceCreateDecision } from './voice-create-session.js';
import { createVoiceAgentController } from './local-agent-controller.js';
import { SPEECH_INPUT_CAPABILITY, SpeechInputError, type SpeechInputCapability } from '../platform/speech-input.js';
import { executeCommandIR } from './execute.js';
import type { NativeSpeechInputPlugin } from '../../../../../mobile/capacitor/shell/native-speech-input-plugin.js';
import { createSpeechInputComposition } from '../../../../../mobile/capacitor/shell/speech-input-capability.js';
import { VOICE_REVIEW_CANCEL_PHRASES, VOICE_REVIEW_CONFIRM_PHRASES } from './vocabulary.js';

const all = { version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog', assignee: 'Mark', tags: ['urgent'], notes: 'Call tomorrow' };
const controllers: ReturnType<typeof createVoiceAgentController>[] = [];
afterEach(() => { controllers.splice(0).forEach(c => c.close()); vi.useRealTimers(); vi.restoreAllMocks(); localStorage.removeItem('scrumboy_debug_voiceflow'); });
function fixture(plan = all, suppliedSpeechInput?: SpeechInputCapability) {
  const h = harness();
  let origin = 'https://one.test';
  const generate = vi.fn(async request => ({ requestId: request.requestId, text: JSON.stringify(plan) }));
  const execute = vi.fn(async (ir, options) => executeCommandIR(ir, { ...options, callTool: h.callTool as never, recordMutation: () => {} }));
  const readTags = vi.fn(async () => h.board.tags.map(tag => ({ name: tag.name })));
  const session = new VoiceCreateSession({ ...h.options, planner: createVoiceCreatePlanner({ generate }), callTool: h.callTool as never, readTags, execute, serverOrigin: () => origin });
  const speechInput = { status: vi.fn(async () => ({ state: 'ready' as const })), listen: vi.fn(async () => { throw new SpeechInputError('no_speech'); }) };
  const onView = vi.fn();
  const controller = createVoiceAgentController({ ...h.options, model: h.model, createSession: session, speechInput: suppliedSpeechInput ?? speechInput, onView, continuationEnabled: false });
  controllers.push(controller);
  return { ...h, generate, readTags, execute, session, controller, onView, speechInput, setOrigin: (value: string) => { origin = value; } };
}

function nativeSpeech(result: { transcript: string; segmentCount?: number }): NativeSpeechInputPlugin {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready' }),
    listen: vi.fn().mockResolvedValue(result),
    cancel: vi.fn().mockResolvedValue(undefined),
    invalidate: vi.fn().mockResolvedValue(undefined),
    addListener: vi.fn().mockResolvedValue({ remove: vi.fn().mockResolvedValue(undefined) }),
  } as NativeSpeechInputPlugin;
}
describe('Create v2 application interaction', () => {
  it('requests 45 seconds and sends a final after 20 seconds straight to the planner without an inactivity wait', async () => {
    vi.useFakeTimers();
    const f = fixture();
    let finish!: (value: any) => void;
    f.speechInput.listen.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const listening = f.controller.startListening();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.speechInput.listen).toHaveBeenCalledWith(expect.objectContaining({ maxDurationMs: 45_000 }));
    await vi.advanceTimersByTimeAsync(20_000);
    expect(f.generate).not.toHaveBeenCalled();
    const finalAt = Date.now();
    finish({ transcript: 'Create Big Man in Backlog and assign Mark' });
    await listening;
    expect(Date.now()).toBe(finalAt);
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.controller.getView().phase).toBe('confirmation');
    expect(f.execute).not.toHaveBeenCalled();
  });
  it.each(['timeout', 'recognition_failed', 'unrecognized_code'])('preserves bounded cross-bundle speech error %s', async suppliedCode => {
    const f = fixture();
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    // A separately bundled shell cannot share the frontend error constructor.
    f.speechInput.listen.mockRejectedValueOnce(Object.assign(new Error('private provider detail'), { code: suppliedCode }));
    await f.controller.startListening();
    expect(debug).toHaveBeenCalledWith('VoiceFlow trace', expect.objectContaining({
      stage: 'failure', source: 'speech', maxDurationMs: 45_000,
      code: suppliedCode === 'timeout' ? 'timeout' : 'recognition_failed',
    }));
    expect(JSON.stringify(debug.mock.calls)).not.toContain('private provider detail');
    expect(f.generate).not.toHaveBeenCalled();
  });
  it.each(['button', 'yes', 'confirm', 'go ahead'])('one planner call and one enriched request through %s confirmation', async approval => {
    const f = fixture();
    await f.controller.submitTranscript('Create Big Man in Backlog, assign Mark, tag urgent and add notes Call tomorrow');
    expect(f.controller.getView().phase).toBe('confirmation');
    expect(f.generate).toHaveBeenCalledOnce(); expect(f.model).not.toHaveBeenCalled();
    expect(f.execute).not.toHaveBeenCalled(); expect(f.options.openTodo).not.toHaveBeenCalled();
    expect(f.readTags).toHaveBeenCalledOnce();
    expect(f.callTool.mock.calls.every(([name]) => name === 'members_list')).toBe(true);
    if (approval === 'button') await Promise.all([f.controller.confirm(), f.controller.confirm()]); else await f.controller.submitTranscript(approval);
    expect(f.controller.getView().phase).toBe('success');
    expect(f.generate).toHaveBeenCalledOnce(); expect(f.execute).toHaveBeenCalledOnce();
    expect(f.readTags).toHaveBeenCalledTimes(2);
    expect(f.callTool.mock.calls.filter(([name]) => name === 'todos_create')).toHaveLength(1);
    expect(f.callTool.mock.calls.find(([name]) => name === 'todos_create')?.[1]).toEqual({ projectSlug: 'alpha', title: 'Big Man', columnKey: 'backlog', assigneeUserId: 8, tags: ['urgent'], body: 'Call tomorrow' });
    await f.controller.confirm(); await f.controller.submitTranscript('yes');
    expect(f.execute).toHaveBeenCalledOnce(); expect(f.generate).toHaveBeenCalledOnce();
  });
  it.each(VOICE_REVIEW_CONFIRM_PHRASES)('executes once for standalone affirmative %s without another planner call', async approval => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog' } as never);
    await f.controller.submitTranscript('Create Big Man');
    await f.controller.submitTranscript(approval);
    expect(f.execute).toHaveBeenCalledOnce();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.callTool.mock.calls.filter(([name]) => name === 'todos_create')).toHaveLength(1);
  });
  it('accepts sentence punctuation between words in an allowlisted affirmative', async () => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog' } as never);
    await f.controller.submitTranscript('Create Big Man');
    await f.controller.submitTranscript('Yes, please.');
    expect(f.execute).toHaveBeenCalledOnce();
    expect(f.generate).toHaveBeenCalledOnce();
  });
  it.each(VOICE_REVIEW_CANCEL_PHRASES)('cancels standalone negative %s without mutation or another planner call', async reply => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog' } as never);
    await f.controller.submitTranscript('Create Big Man');
    await f.controller.submitTranscript(reply);
    expect(f.execute).not.toHaveBeenCalled();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.session.confirmationPending).toBe(false);
  });
  it('accepts sentence punctuation between words in an allowlisted cancellation', async () => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog' } as never);
    await f.controller.submitTranscript('Create Big Man');
    await f.controller.submitTranscript('No, thanks.');
    expect(f.execute).not.toHaveBeenCalled();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.session.confirmationPending).toBe(false);
  });
  it.each([
    'yes but assign Sarah',
    'sure but put it in Testing',
    'okay and tag it urgent',
    'yeah change the title',
    'go ahead after adding a note',
    'yes except remove Mark',
    'sure if you rename it',
    'Yes, but assign Sarah.',
    'Okay, and tag it UX.',
  ])('invalidates review for mixed response %s without execution or replanning', async reply => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', lane: 'Backlog' } as never);
    await f.controller.submitTranscript('Create Big Man');
    await f.controller.submitTranscript(reply);
    expect(f.controller.getView().phase).toBe('error');
    expect(f.execute).not.toHaveBeenCalled();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.session.confirmationPending).toBe(false);
    await f.controller.confirm();
    expect(f.execute).not.toHaveBeenCalled();
  });
  it('retains exact final ASR text before generation resolves and throughout review/approval', async () => {
    const f = fixture();
    const transcript = '  Hey, create Big Man in Backlog, assign Mark, tag urgent and add notes Call tomorrow.  ';
    f.speechInput.listen.mockResolvedValueOnce({ transcript } as never);
    let finish!: (value: any) => void;
    f.generate.mockImplementationOnce(request => new Promise(resolve => { finish = value => resolve({ requestId: request.requestId, text: JSON.stringify(value) }); }));
    const listening = f.controller.startListening();
    await vi.waitFor(() => expect(f.generate).toHaveBeenCalledOnce());
    expect(f.controller.getView().capturedTranscript).toBe(transcript);
    expect(f.controller.getView().activity).toBe('processing');
    finish(all); await listening;
    expect(f.controller.getView().capturedTranscript).toBe(transcript);
    f.speechInput.listen.mockResolvedValueOnce({ transcript: 'yes' } as never);
    await f.controller.startListening();
    expect(f.controller.getView().capturedTranscript).toBe(transcript);
    expect(f.generate).toHaveBeenCalledOnce(); expect(f.execute).toHaveBeenCalledOnce();
  });
  it('sends one combined authoritative multi-segment turn to Nano exactly once', async () => {
    const f = fixture();
    const combined = 'create a story called Big Man put it in Backlog and assign Mark tag urgent';
    f.speechInput.listen.mockResolvedValueOnce({ transcript: combined } as never);
    await f.controller.startListening();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.generate.mock.calls[0][0].input).toBe(combined);
    expect(f.controller.getView().phase).toBe('confirmation');
  });
  it.each([
    { transcript: 'Create Big Man', segmentCount: 1 },
    { transcript: 'Create Big Man put it in Backlog', segmentCount: 2 },
  ])('carries native segmentCount=$segmentCount through the shell into one controller turn', async result => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: Record<string, any>[] = [];
    vi.spyOn(console, 'debug').mockImplementation((name, fields) => {
      if (name === 'VoiceFlow trace') events.push(fields);
    });
    const native = nativeSpeech(result);
    const speechInput = createSpeechInputComposition({ plugin: native, operationIdFactory: () => `speech-${result.segmentCount}` })
      .registry.get(SPEECH_INPUT_CAPABILITY)!;
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man', ...(result.segmentCount === 2 ? { lane: 'Backlog' } : {}) } as never, speechInput);

    await f.controller.startListening();

    expect(events.filter(event => event.stage === 'asr_final')).toHaveLength(1);
    expect(events.find(event => event.stage === 'asr_final')).toMatchObject({
      transcript: result.transcript,
      segmentCount: result.segmentCount,
    });
    expect(events.filter(event => event.stage === 'planner_start')).toHaveLength(1);
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.generate.mock.calls[0][0].input).toBe(result.transcript);
  });
  it('turns three aggregated ASR finals into one planner invocation', async () => {
    const result = { transcript: 'Create Big Man put it in Backlog and assign Mark', segmentCount: 3 };
    const speechInput = createSpeechInputComposition({ plugin: nativeSpeech(result), operationIdFactory: () => 'speech-3' })
      .registry.get(SPEECH_INPUT_CAPABILITY)!;
    const f = fixture(all, speechInput);

    await f.controller.startListening();

    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.generate.mock.calls[0][0].input).toBe(result.transcript);
  });
  it.each(['yes but assign Sarah instead', 'never mind', 'no', 'cancel', 'stop'])('never executes for %s', async reply => {
    const f = fixture(); await f.controller.submitTranscript('Create Big Man'); await f.controller.submitTranscript(reply);
    expect(f.execute).not.toHaveBeenCalled(); expect(f.generate).toHaveBeenCalledOnce();
    expect(f.session.confirmationPending).toBe(false); await f.controller.confirm(); expect(f.execute).not.toHaveBeenCalled();
  });
  it.each(['lane-order', 'lane-name', 'member', 'tag', 'permission', 'account', 'server'])('invalidates reviewed create after changed %s', async change => {
    const f = fixture(change === 'lane-order' ? { version: 1, kind: 'create', title: 'Big Man' } as never : all);
    await f.controller.submitTranscript('Create Big Man'); expect(f.session.confirmationPending).toBe(true);
    if (change === 'lane-order') f.board.columnOrder!.reverse();
    if (change === 'lane-name') f.board.columnOrder![0].name = 'Inbox';
    if (change === 'member') f.context().members[0].email = 'different@example.test';
    if (change === 'tag') f.board.tags = [];
    if (change === 'permission') f.context().role = 'viewer';
    if (change === 'account') f.context().userId = 11;
    if (change === 'server') f.setOrigin('https://two.test');
    await f.controller.confirm(); expect(f.execute).not.toHaveBeenCalled(); expect(f.session.confirmationPending).toBe(false);
    expect(f.controller.getView().phase).toBe('error');
  });
  it('refreshes before choosing defaults and revalidates once before the single write', async () => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man' } as never);
    f.options.refreshBoard.mockImplementationOnce(async () => { f.board.columnOrder!.unshift({ key: 'triage', name: 'Triage', isDone: false }); f.board.columns.triage = []; });
    await f.controller.submitTranscript('Create Big Man');
    expect(f.controller.getView().confirmation!.summary).toBe('Create "Big Man" in Triage');
    expect(f.options.refreshBoard).toHaveBeenCalledOnce();
    await f.controller.confirm(); expect(f.options.refreshBoard).toHaveBeenCalledTimes(3); // prepare, revalidation, post-write UI refresh
  });
  it('chooses an ambiguous member without Nano, then reviews all fields', async () => {
    const f = fixture(); f.context().members[0].name = 'Mark Jones';
    f.context().members.push({ userId: 9, name: 'Mark Smith', email: 'smith@example.test', role: 'maintainer' });
    await f.controller.submitTranscript('Create Big Man and assign Mark');
    expect(f.controller.getView().phase).toBe('question'); expect(f.execute).not.toHaveBeenCalled();
    await f.controller.chooseClarification(1);
    expect(f.controller.getView().confirmation!.summary).toContain('Mark Smith');
    await f.controller.submitTranscript('yes'); expect(f.generate).toHaveBeenCalledOnce(); expect(f.execute).toHaveBeenCalledOnce();
  });
  it('resolves U.X. and a member in one preparation, then deterministically confirms with sure', async () => {
    const f = fixture({ version: 1, kind: 'create', title: 'Refactor Navigation', assignee: 'Mark', tags: ['U.X.'] } as never);
    f.board.tags = [{ name: 'UX', count: 0 }, { name: 'Architecture', count: 0 }];
    await f.controller.submitTranscript('Create Refactor Navigation, assign Mark and tag it U.X.');
    expect(f.controller.getView().phase).toBe('confirmation');
    expect(f.controller.getView().confirmation?.summary).toContain('Tags: UX');
    expect(f.generate).toHaveBeenCalledOnce();
    await f.controller.submitTranscript('Sure.');
    expect(f.execute).toHaveBeenCalledOnce();
    expect(f.generate).toHaveBeenCalledOnce();
    expect(f.callTool.mock.calls.find(([name]) => name === 'todos_create')?.[1]).toMatchObject({
      projectSlug: 'alpha', title: 'Refactor Navigation', assigneeUserId: 8, tags: ['UX'],
    });
  });
  it.each([{ ...all, unhandled: [{ text: 'schedule Tuesday', reason: 'unsupported' }] }, { version: 1, kind: 'not_create' }, { version: 1, kind: 'create' }])('blocks incomplete/unsupported before any query or review %#', async plan => {
    const f = fixture(plan as never); await f.controller.submitTranscript('Create a story');
    expect(f.controller.getView().phase).toBe('error'); expect(f.callTool).not.toHaveBeenCalled(); expect(f.execute).not.toHaveBeenCalled();
    await f.controller.confirm(); expect(f.execute).not.toHaveBeenCalled();
  });
  it('ignores late generation after close and performs no navigation or mutation', async () => {
    const f = fixture(); let finish!: (value: any) => void;
    f.generate.mockImplementationOnce(request => new Promise(resolve => { finish = value => resolve({ requestId: request.requestId, text: JSON.stringify(value) }); }));
    const work = f.controller.submitTranscript('Create Big Man'); await vi.waitFor(() => expect(f.generate).toHaveBeenCalledOnce());
    f.controller.close(); finish(all); await work;
    expect(f.controller.getView().phase).toBe('closed'); expect(f.callTool).not.toHaveBeenCalled(); expect(f.options.openTodo).not.toHaveBeenCalled();
  });
  it('reports unknown execution outcome and never retries after a lost response', async () => {
    const f = fixture(); f.execute.mockRejectedValueOnce(new Error('network'));
    await f.controller.submitTranscript('Create Big Man'); await f.controller.confirm(); await f.controller.confirm();
    expect(f.controller.getView().status).toMatchObject({ text: expect.stringContaining('could not be confirmed') });
    expect(f.execute).toHaveBeenCalledOnce();
  });
  it('traces the one-shot stages without raw output or repeated note bodies', async () => {
    const f = fixture(); localStorage.setItem('scrumboy_debug_voiceflow', '1'); const events: any[] = [];
    vi.spyOn(console, 'debug').mockImplementation((name, fields) => { if (name === 'VoiceFlow trace') events.push(fields); });
    await f.controller.submitTranscript('Create Big Man'); await f.controller.confirm();
    expect(events.filter(event => event.stage === 'planner_start')).toHaveLength(1);
    expect(events.find(event => event.stage === 'plan')).toMatchObject({ hasTitle: true, notesLength: 13, explicitAssignee: true });
    expect(events.at(-1)).toMatchObject({ stage: 'terminal', outcome: 'success', mutationsExecuted: 1 });
    expect(JSON.stringify(events)).not.toContain('Call tomorrow');
  });
  it('traces authoritative leftmost-lane defaulting explicitly', async () => {
    const f = fixture({ version: 1, kind: 'create', title: 'Big Man' } as never);
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: any[] = [];
    vi.spyOn(console, 'debug').mockImplementation((name, fields) => { if (name === 'VoiceFlow trace') events.push(fields); });
    f.board.columnOrder = [
      { key: 'not_started', name: 'Not Started', isDone: false },
      { key: 'doing', name: 'In Progress', isDone: false },
      { key: 'testing', name: 'Testing', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ];
    f.board.columns = { not_started: [], doing: [], testing: [], done: [] };
    await f.controller.submitTranscript('Create Big Man');
    expect(f.controller.getView().confirmation?.summary).toContain('Not Started');
    expect(events.find(event => event.stage === 'resolve' && 'defaultLane' in event)).toMatchObject({ defaultLane: true });
  });
});
describe('whole utterance confirmation', () => {
  it.each([...VOICE_REVIEW_CONFIRM_PHRASES, '  YES!  ', 'That’s fine.', 'Yes, please.', 'Sure, thing.'])('accepts %s', text => expect(voiceCreateDecision(text)).toBe('confirm'));
  it.each([...VOICE_REVIEW_CANCEL_PHRASES, 'No, thanks.'])('cancels %s', text => expect(voiceCreateDecision(text)).toBe('cancel'));
  it.each(['yes but assign Sarah instead', 'do not confirm', 'add notes yes', 'go ahead and delete Fred', 'not yes', 'yes? but no'])('rejects %s', text => expect(voiceCreateDecision(text)).toBe('unknown'));
});
