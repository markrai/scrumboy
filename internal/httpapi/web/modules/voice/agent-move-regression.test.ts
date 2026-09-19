// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { harness, skill } from './agent.test.utils.js';

function story239(h: ReturnType<typeof harness>) {
  h.todo.title = 'Invalid URLs should redirect to main login';
  h.todo.localId = 239;
}

describe('AI VoiceFlow move regressions', () => {
  it.each([
    'Move #239 to done.',
    'Move story #239 to done.',
    'Move number 239 to Done.',
  ])('completes %s without a model turn or missing-lane clarification', async utterance => {
    const h = harness([]);
    story239(h);
    const run = vi.spyOn(h.registry, 'run');
    const view = await h.loop.submit(utterance, h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('#239');
    expect(view.text).toMatch(/Done/i);
    expect(h.model).not.toHaveBeenCalled();
    expect(h.loop.currentState).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(run.mock.calls[0][0]).toEqual(skill('todos.move', { reference: '#239', lane: 'Done' }));
    expect(h.execute).not.toHaveBeenCalled();
    expect((await h.loop.confirm(h.signal)).phase).toBe('success');
    expect(h.execute.mock.calls[0][0]).toMatchObject({ intent: 'todos.move', entities: { localId: 239, toColumnKey: 'done' } });
  });

  it('does not accept a model Which lane? for a complete numeric move', async () => {
    const h = harness([]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    const view = await h.loop.submit('Move Goblins in Washington to done.', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).not.toMatch(/Which lane/i);
    expect(h.model).not.toHaveBeenCalled();
    expect(h.loop.currentState).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('still asks which lane for Move #239, then locally merges done', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
    ]);
    story239(h);
    const question = await h.loop.submit('Move #239.', h.signal);
    expect(question).toEqual({ phase: 'question', text: 'Which lane?' });
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane' },
    });
    const confirmation = await h.loop.submit('done', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(confirmation.text).toContain('#239');
    expect(h.model).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('local-merges Done after Move #239 even when the model used ask_user', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Which lane?' }]);
    story239(h);
    expect(await h.loop.submit('Move #239.', h.signal)).toEqual({ phase: 'question', text: 'Which lane?' });
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane' },
    });
    const confirmation = await h.loop.submit('Done', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.model).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('does not accept a model-invented Done lane for Move #239', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
    ]);
    story239(h);
    expect(await h.loop.submit('Move #239.', h.signal)).toEqual({ phase: 'question', text: 'Which lane?' });
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane' },
    });
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('does not locally finish a compound move when the model asks which lane', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
      { kind: 'ask_user', text: 'Which lane?' },
    ]);
    story239(h);
    const view = await h.loop.submit('Move #239 to done and tag it urgent', h.signal);
    expect(view.phase).toBe('question');
    expect(view.text).toMatch(/Which lane/i);
    expect(h.loop.currentState.kind).not.toBe('confirmation');
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('still asks which story for Move to done, then locally merges #239', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
    ]);
    story239(h);
    expect(await h.loop.submit('Move to done.', h.signal)).toEqual({ phase: 'question', text: 'Which story?' });
    const confirmation = await h.loop.submit('#239', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.model).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('local-merges #239 after Move to done even when the model used ask_user', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Which story?' }]);
    story239(h);
    expect(await h.loop.submit('Move to done.', h.signal)).toEqual({ phase: 'question', text: 'Which story?' });
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference' },
    });
    const confirmation = await h.loop.submit('#239', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.model).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('local-merges #239 after equivalent ask_user wording for Move to done', async () => {
    const h = harness([{ kind: 'ask_user', text: 'What should I move?' }]);
    story239(h);
    expect(await h.loop.submit('Move to done.', h.signal)).toEqual({ phase: 'question', text: 'Which story?' });
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference' },
    });
    const confirmation = await h.loop.submit('#239', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.model).toHaveBeenCalledOnce();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it.each([
    'Invalid URL story',
    'Invalid URL card',
    'Invalid URL todo',
    'Invalid URLs should redirect to main login',
    'the Invalid URL card',
  ])(
    'resolves %s against the unique Invalid URLs title without a model turn',
    async reference => {
      const h = harness([]);
      story239(h);
      const view = await h.loop.submit(`Move ${reference} ${reference.startsWith('the') ? 'into' : 'to'} done.`, h.signal);
      expect(view.phase).toBe('confirmation');
      expect(view.text).toContain('Invalid URLs should redirect to main login');
      expect(view.text).not.toMatch(/Which lane/i);
      expect(h.model).not.toHaveBeenCalled();
      expect(h.loop.currentState).toEqual({ kind: 'confirmation', proposalCount: 1 });
      expect((await h.loop.confirm(h.signal)).phase).toBe('success');
      expect(h.execute.mock.calls[0][0]).toMatchObject({ intent: 'todos.move', entities: { localId: 239, toColumnKey: 'done' } });
    },
  );

  it('completes a unique title move when the model returns unparseable JSON twice', async () => {
    const h = harness(['broken', 'still broken']);
    story239(h);
    const view = await h.loop.submit('Move Invalid URL story to done.', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Invalid URLs should redirect to main login');
    expect(h.model).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('recovers an ambiguous title move after unparseable JSON as a choice, not a guessed story', async () => {
    const h = harness(['broken', { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push(
      { id: 92, localId: 370, title: 'Goblins in Burtonsville', status: 'backlog', columnKey: 'backlog' },
      { id: 93, localId: 371, title: 'Goblins on the way', status: 'backlog', columnKey: 'backlog' },
    );
    const question = await h.loop.submit('Move Goblin to Done.', h.signal);
    expect(question.phase).toBe('question');
    expect(question.choices).toHaveLength(3);
    expect(question.text).not.toMatch(/could not finish that safely/i);
    expect(h.loop.currentState).toEqual({ kind: 'choice' });
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('keeps ambiguous Goblin titles fail-closed as a choice, not a guessed move', async () => {
    const h = harness([
      { kind: 'skill_call', skill: 'todos.move', arguments: { reference: 'Goblin', lane: 'Done' } },
      { kind: 'ask_user', text: 'Which one?' },
    ]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push(
      { id: 92, localId: 370, title: 'Goblins in Burtonsville', status: 'backlog', columnKey: 'backlog' },
      { id: 93, localId: 371, title: 'Goblins on the way', status: 'backlog', columnKey: 'backlog' },
    );
    const question = await h.loop.submit('Move Goblin to Done.', h.signal);
    expect(question.phase).toBe('question');
    expect(question.choices).toHaveLength(3);
    expect(h.loop.currentState).toEqual({ kind: 'choice' });
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('returns the specific missing-title error instead of generic safeFailure', async () => {
    const h = harness(['broken', 'still broken']);
    const view = await h.loop.submit('Move TotallyInventedStory to done.', h.signal);
    expect(view.phase).toBe('error');
    expect(view.text).not.toMatch(/could not finish that safely/i);
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('returns the specific missing-lane error for a numeric move to an unknown destination', async () => {
    const h = harness([]);
    story239(h);
    const view = await h.loop.submit('Move #239 to TotallyInventedLane.', h.signal);
    expect(view.phase).toBe('error');
    expect(view.text).not.toMatch(/could not finish that safely/i);
    expect(h.model).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('asks which story after unparseable JSON for Move to done', async () => {
    const h = harness(['broken', 'still broken']);
    story239(h);
    const question = await h.loop.submit('Move to done.', h.signal);
    expect(question).toEqual({ phase: 'question', text: 'Which story?' });
    const confirmation = await h.loop.submit('#239', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('does not let a failed finish destroy a prepared standalone move', async () => {
    const h = harness([]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    const view = await h.loop.submit('Move Goblins in Washington to Done', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(h.model).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();
  });

  it('traces a returned missing-lane failure as lane_resolution', async () => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: Record<string, unknown>[] = [];
    const debug = vi.spyOn(console, 'debug').mockImplementation((name, fields) => {
      if (name === 'VoiceFlow trace') events.push(fields as Record<string, unknown>);
    });
    try {
      const h = harness([]);
      story239(h);
      const view = await h.loop.submit('Move #239 to TotallyInventedLane.', h.signal);
      expect(view.phase).toBe('error');
      expect(view.text).not.toMatch(/could not finish that safely/i);
      expect(events).toContainEqual(expect.objectContaining({
        source: 'resolve', reason: 'not_found', safeFailureStage: 'lane_resolution', resource: 'lane',
      }));
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'terminal', outcome: 'resolution_failure', safeFailureStage: 'lane_resolution',
      }));
    } finally {
      debug.mockRestore();
      localStorage.removeItem('scrumboy_debug_voiceflow');
    }
  });

  it('traces a missing-title resolution failure separately from generic interpret failure', async () => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: Record<string, unknown>[] = [];
    const debug = vi.spyOn(console, 'debug').mockImplementation((name, fields) => {
      if (name === 'VoiceFlow trace') events.push(fields as Record<string, unknown>);
    });
    try {
      const h = harness(['broken', 'still broken']);
      const view = await h.loop.submit('Move TotallyInventedStory to done.', h.signal);
      expect(view.phase).toBe('error');
      expect(view.text).not.toMatch(/could not finish that safely/i);
      expect(events).toContainEqual(expect.objectContaining({
        source: 'resolve', reason: 'not_found', safeFailureStage: 'target_resolution', resource: 'todo',
      }));
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'terminal', outcome: 'resolution_failure', safeFailureStage: 'target_resolution',
      }));
    } finally {
      debug.mockRestore();
      localStorage.removeItem('scrumboy_debug_voiceflow');
    }
  });

  it('traces protocol repair exhaustion separately from a generic interpret failure', async () => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: Record<string, unknown>[] = [];
    const debug = vi.spyOn(console, 'debug').mockImplementation((name, fields) => {
      if (name === 'VoiceFlow trace') events.push(fields as Record<string, unknown>);
    });
    try {
      const h = harness(['broken', 'still broken']);
      expect((await h.loop.submit('hello', h.signal)).phase).toBe('error');
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'interpret', result: 'failure', safeFailureStage: 'protocol_repair_exhaustion',
      }));
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'terminal', outcome: 'protocol_repair_exhaustion_failure',
      }));
      expect(h.execute).not.toHaveBeenCalled();
    } finally {
      debug.mockRestore();
      localStorage.removeItem('scrumboy_debug_voiceflow');
    }
  });
});
