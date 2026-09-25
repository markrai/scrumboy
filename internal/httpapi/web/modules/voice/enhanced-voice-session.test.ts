// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { createVoiceFlowTrace } from './trace.js';
import { EnhancedVoiceSession, isVoiceCreateCandidate } from './enhanced-voice-session.js';
import type { AgentSession } from './agent-skills.js';
import type { AgentLoopView } from './agent-loop.js';
import { harness, skill } from './agent.test.utils.js';
import { createVoiceCreatePlanner } from './voice-create-planner.js';
import { VoiceCreateSession } from './voice-create-session.js';

const confirmation = (text = 'Review create') => ({ phase: 'confirmation' as const, text, danger: false, speechText: text });
const question = (text = 'Which one?') => ({ phase: 'question' as const, text, choices: [{ id: 'todo:374', label: '#374 · Goblin · Done' }] });
const success = (text = 'Done.') => ({ phase: 'success' as const, text });

type FakeEngine = {
  pending: boolean;
  confirmationPending: boolean;
  captureContext?: 'initial_create_capture';
  trace: ReturnType<typeof vi.fn>;
  adoptTrace: ReturnType<typeof vi.fn>;
  submit: ReturnType<typeof vi.fn>;
  confirm: ReturnType<typeof vi.fn>;
  cancel: ReturnType<typeof vi.fn>;
  choose: ReturnType<typeof vi.fn>;
  endTrace: ReturnType<typeof vi.fn>;
  cancelTrace: ReturnType<typeof vi.fn>;
  invalidate: ReturnType<typeof vi.fn>;
  setKeepListening: ReturnType<typeof vi.fn>;
};

function engine(result: AgentLoopView, context?: 'initial_create_capture'): FakeEngine {
  const trace = createVoiceFlowTrace();
  return {
    pending: false,
    confirmationPending: false,
    ...(context ? { captureContext: context } : {}),
    trace: vi.fn(() => trace),
    adoptTrace: vi.fn(),
    submit: vi.fn(async () => result),
    confirm: vi.fn(async () => result),
    cancel: vi.fn(() => success('Cancelled.')),
    choose: vi.fn(async () => result),
    endTrace: vi.fn(),
    cancelTrace: vi.fn(),
    invalidate: vi.fn(),
    setKeepListening: vi.fn(),
  };
}

function fixture() {
  const create = engine(confirmation(), 'initial_create_capture');
  const agent = engine(question());
  const agentSession = { activeTodo: null, keepListening: false } as AgentSession;
  const createWithContext = Object.assign(create, { context: vi.fn() });
  const agentWithContext = Object.assign(agent, { registry: { context: vi.fn() }, session: agentSession });
  const session = new EnhancedVoiceSession({
    create: createWithContext,
    agent: agentWithContext,
  });
  return { create, agent, agentSession, session };
}

describe('enhanced VoiceFlow deterministic router', () => {
  it.each([
    'Create',
    'Create Fred',
    'Create a story called Jonas',
    'Please create a story called Jonas',
    'Could you create a card called Jonas',
    'uh, create a story called Jonas, and assigned to Mark',
    'Hey, make a card. Call it Settings.',
    'Add a story',
    'Add a card',
    'Add a todo',
    'Add a task',
    'Add an item',
    'Add a story called Fred',
    'Add a card named Jonas',
    'Please add a story called Fred',
    'Can you add a todo called Jonas',
    'Create Fred and delete Bogus',
  ])('recognizes a clear Create candidate: %s', transcript => {
    expect(isVoiceCreateCandidate(transcript)).toBe(true);
  });

  it.each([
    'Open the story called Create Jonas',
    'Find Create Jonas',
    'Search for Create Jonas',
    'Look up Create Jonas',
    'Move Fred to Done',
    'Delete Fred',
    'Assign Fred to Mark',
    'Add the urgent tag to Fred',
    'Add urgent to Fred',
    'Add notes to Fred',
    'Add a note to Fred',
    'Add "call Mark" to Fred\'s notes',
    'Make Fred urgent',
    'Create a tag architecture',
  ])('keeps a non-Create request on the Agent path: %s', transcript => {
    expect(isVoiceCreateCandidate(transcript)).toBe(false);
  });

  it.each([
    ['Add a story', 'create'],
    ['Add a card called Jonas', 'create'],
    ['Add notes to Fred', 'agent'],
    ['Add the urgent tag to Fred', 'agent'],
  ] as const)('dispatches %s to the dedicated %s engine', async (transcript, expectedOwner) => {
    const f = fixture();
    f.create.submit.mockResolvedValueOnce(confirmation());
    f.agent.submit.mockResolvedValueOnce(question());
    const result = await f.session.submit(transcript, new AbortController().signal);
    expect(result.phase).toBe(expectedOwner === 'create' ? 'confirmation' : 'question');
    expect(f.session.currentOwner).toBe(expectedOwner);
    if (expectedOwner === 'create') {
      expect(f.create.submit).toHaveBeenCalledWith(transcript, expect.any(AbortSignal));
      expect(f.agent.submit).not.toHaveBeenCalled();
    } else {
      expect(f.agent.submit).toHaveBeenCalledWith(transcript, expect.any(AbortSignal));
      expect(f.create.submit).not.toHaveBeenCalled();
    }
  });

  it('routes a fresh Create once and keeps follow-ups on Create', async () => {
    const f = fixture();
    f.create.pending = true; f.create.confirmationPending = true;
    f.create.submit.mockResolvedValueOnce(confirmation()).mockResolvedValueOnce(success());
    const first = await f.session.submit('Create Fred', new AbortController().signal);
    expect(first.phase).toBe('confirmation');
    expect(f.create.submit).toHaveBeenCalledWith('Create Fred', expect.any(AbortSignal));
    expect(f.agent.submit).not.toHaveBeenCalled();
    const next = await f.session.submit('yes', new AbortController().signal);
    expect(next.phase).toBe('success');
    expect(f.create.submit).toHaveBeenCalledTimes(2);
    expect(f.agent.submit).not.toHaveBeenCalled();
    expect(f.session.currentOwner).toBeNull();
  });

  it('routes a fresh Agent request once and keeps a choice reply on Agent', async () => {
    const f = fixture();
    f.agent.pending = true;
    f.agent.submit.mockResolvedValueOnce(question()).mockResolvedValueOnce(success('Opened.'));
    expect((await f.session.submit('Open Goblin', new AbortController().signal)).phase).toBe('question');
    expect(f.agent.submit).toHaveBeenCalledOnce();
    expect(f.create.submit).not.toHaveBeenCalled();
    expect(f.session.currentOwner).toBe('agent');
    expect(f.session.pending).toBe(true);
    expect((await f.session.submit('#374', new AbortController().signal)).phase).toBe('success');
    expect(f.agent.submit).toHaveBeenCalledTimes(2);
    expect(f.create.submit).not.toHaveBeenCalled();
    expect(f.session.currentOwner).toBeNull();
  });

  it('keeps Create tag and binary clarification replies on Create', async () => {
    const f = fixture();
    f.create.pending = true;
    f.create.submit.mockResolvedValueOnce(question('Did you mean tag `bug`?')).mockResolvedValueOnce(success());
    expect((await f.session.submit('Create Fred and tag bug', new AbortController().signal)).phase).toBe('question');
    expect((await f.session.submit('yes', new AbortController().signal)).phase).toBe('success');
    expect(f.create.submit).toHaveBeenCalledTimes(2);
    expect(f.agent.submit).not.toHaveBeenCalled();
  });

  it('keeps a real Agent mutation confirmation and its reply on Agent', async () => {
    const f = fixture();
    f.agent.submit.mockImplementation(async (transcript: string) => {
      if (transcript === 'Move Fred to Done') {
        f.agent.pending = true;
        f.agent.confirmationPending = true;
        return confirmation('Move Fred to Done?');
      }
      f.agent.pending = false;
      f.agent.confirmationPending = false;
      return success('Moved.');
    });
    expect((await f.session.submit('Move Fred to Done', new AbortController().signal)).phase).toBe('confirmation');
    expect(f.session.currentOwner).toBe('agent');
    expect(f.session.pending).toBe(true);
    expect(f.agent.submit).toHaveBeenCalledWith('Move Fred to Done', expect.any(AbortSignal));
    expect(f.create.submit).not.toHaveBeenCalled();
    expect((await f.session.submit('yes', new AbortController().signal)).phase).toBe('success');
    expect(f.agent.submit).toHaveBeenLastCalledWith('yes', expect.any(AbortSignal));
    expect(f.agent.submit).toHaveBeenCalledTimes(2);
    expect(f.create.submit).not.toHaveBeenCalled();
    expect(f.session.currentOwner).toBeNull();
  });

  it('resets ownership between completed Create and Agent interactions in either order', async () => {
    const first = fixture();
    first.create.pending = true;
    first.create.submit.mockResolvedValueOnce(success('Created.'));
    await first.session.submit('Create Jonas', new AbortController().signal);
    first.agent.pending = true;
    first.agent.submit.mockResolvedValueOnce(success('Opened.'));
    await first.session.submit('Open Goblin', new AbortController().signal);
    expect(first.create.submit).toHaveBeenCalledOnce();
    expect(first.agent.submit).toHaveBeenCalledOnce();

    const second = fixture();
    second.agent.pending = true;
    second.agent.submit.mockResolvedValueOnce(success('Opened.'));
    await second.session.submit('Open Goblin', new AbortController().signal);
    second.create.pending = true;
    second.create.submit.mockResolvedValueOnce(success('Created.'));
    await second.session.submit('Create Jonas', new AbortController().signal);
    expect(second.agent.submit).toHaveBeenCalledOnce();
    expect(second.create.submit).toHaveBeenCalledOnce();
  });

  it('clears retained Agent activeTodo after successful Create and allows Create then Open', async () => {
    const f = fixture();
    f.agentSession.activeTodo = { kind: 'todo', localId: 355, rowId: 'row-355' };
    f.create.pending = true; f.create.confirmationPending = true;
    f.create.submit.mockResolvedValueOnce(success('Created.'));
    const result = await f.session.submit('Create Jonas', new AbortController().signal);
    expect(result.phase).toBe('success');
    expect(f.agentSession.activeTodo).toBeNull();
    expect(f.session.currentOwner).toBeNull();
  });

  it('uses the fresh enhanced capture context before routing', () => {
    const f = fixture();
    expect(f.session.captureContext).toBe('initial_enhanced_voiceflow_capture');
    expect(f.session.pending).toBe(false);
  });

  it('keeps provisional fresh-capture diagnostics on the eventual owner', async () => {
    const f = fixture();
    const provisional = f.session.trace();
    f.agent.pending = true;
    f.agent.submit.mockResolvedValueOnce(success('Opened.'));
    await f.session.submit('Open Goblin', new AbortController().signal);
    expect(f.agent.adoptTrace).toHaveBeenCalledWith(provisional);
  });

  it('routes a real fresh Create to one planner call and never to the Agent model', async () => {
    const h = harness();
    const generate = vi.fn(async (request: { requestId: string }) => ({ requestId: request.requestId, text: JSON.stringify({ version: 1, kind: 'create', title: 'Fred' }) }));
    const create = new VoiceCreateSession({
      ...h.options,
      planner: createVoiceCreatePlanner({ generate }),
      callTool: h.callTool as never,
      readTags: vi.fn(async () => h.board.tags.map(tag => ({ name: tag.name }))),
      execute: h.execute as never,
    });
    const session = new EnhancedVoiceSession({ create, agent: h.loop });
    const result = await session.submit('Create Fred', h.signal);
    expect(result.phase).toBe('confirmation');
    expect(generate).toHaveBeenCalledOnce();
    expect(h.model).not.toHaveBeenCalled();
  });

  it('routes a real fresh Open to the existing Agent model and not the Create planner', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), { kind: 'finish' }]);
    const generate = vi.fn();
    const create = new VoiceCreateSession({ ...h.options, planner: createVoiceCreatePlanner({ generate }) });
    const session = new EnhancedVoiceSession({ create, agent: h.loop });
    const result = await session.submit('Open Happy Birthday', h.signal);
    expect(result.phase).toBe('success');
    expect(h.model).toHaveBeenCalledOnce();
    expect(generate).not.toHaveBeenCalled();
  });
});
