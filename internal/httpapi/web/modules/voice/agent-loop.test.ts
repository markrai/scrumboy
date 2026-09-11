// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { harness, skill, finish, latestRef } from './agent.test.utils.js';
import { AGENT_LIMITS } from './agent-protocol.js';
describe('bounded local agent loop', () => {
  it.each(['todos.append_notes', 'todos.replace_notes'])('separates full visual and complete condensed %s confirmation speech', async name => {
    const notes = 'Dictated text. '.repeat(60);
    const h = harness([skill(name, { reference: 'Happy Birthday', text: notes }), skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' }), finish]);
    const view = await h.loop.submit('Update notes and add urgent', h.signal);
    expect(view.phase).toBe('confirmation');
    if (view.phase !== 'confirmation') throw new Error('Expected confirmation');
    expect(view.text.length).toBeGreaterThan(600); expect(view.text).toContain(notes);
    expect(view.speechText).toContain(name === 'todos.append_notes' ? 'Add the dictated text' : 'Replace the notes');
    expect(view.speechText).toContain('urgent'); expect(view.speechText!.length).toBeLessThanOrEqual(600);
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('marks confirmation speech unavailable rather than dropping a later proposal', async () => {
    const title = 'X'.repeat(200);
    const h = harness([skill('todos.move', { reference: title, lane: 'Done' }), skill('todos.assign', { reference: title, member: 'Mark' }), skill('todos.append_notes', { reference: title, text: 'Paragraph. '.repeat(80) }), skill('todos.rename', { reference: title, title: 'Renamed' }), finish]);
    h.todo.title = title;
    const view = await h.loop.submit('Prepare three changes', h.signal);
    expect(view.phase).toBe('confirmation');
    if (view.phase !== 'confirmation') throw new Error('Expected confirmation');
    expect(view.text).toContain('Renamed'); expect(view.speechText).toBeNull(); expect(h.execute).not.toHaveBeenCalled();
  });
  it.each([
    ["Open Bird's Eye View", "Bird's Eye View"], ["Open the todo Bird's Eye View", "Bird's Eye View"],
    ["Open the story called Bird's Eye View", "Bird's Eye View"], ["Open the card named Bird's Eye View", "Bird's Eye View"],
    ['Open story named Settings', 'Settings'], ['Open the todo Search', 'Search'],
  ])('preserves literal title with stubbed model: %s', async (utterance, title) => {
    const h = harness([skill('todos.open', { reference: title }), finish]); h.todo.title = title;
    expect((await h.loop.submit(utterance, h.signal)).phase).toBe('success');
    expect(h.options.openTodo).toHaveBeenCalledWith(355); expect(h.execute).not.toHaveBeenCalled();
    expect(h.model.mock.calls[0][0]).toContain(utterance);
  });
  it('opens one strong named-story match exactly once without mutation or confirmation', async () => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), finish]);
    h.todo.title = 'Goblins in Burtonsville';
    const view = await h.loop.submit('Find Goblin', h.signal);
    expect(view.phase).toBe('success');
    expect(h.options.openTodo).toHaveBeenCalledOnce();
    expect(h.options.openTodo).toHaveBeenCalledWith(355);
    expect(h.execute).not.toHaveBeenCalled();
    expect(h.loop.confirmationPending).toBe(false);
  });
  it('offers all three Goblin matches, then opens only the selected issued todoRef', async () => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Burtonsville';
    h.board.columns.done.push(
      { id: 92, localId: 356, title: 'Goblins in Washington', status: 'done', columnKey: 'done' },
      { id: 93, localId: 357, title: 'Goblins on the way', status: 'done', columnKey: 'done' },
    );
    const question = await h.loop.submit('Open the Goblin story', h.signal);
    expect(question).toMatchObject({
      phase: 'question',
      text: 'Which one? #355 · Goblins in Burtonsville · Backlog; #356 · Goblins in Washington · Done; #357 · Goblins on the way · Done',
      choices: [
        { label: '#355 · Goblins in Burtonsville · Backlog' },
        { label: '#356 · Goblins in Washington · Done' },
        { label: '#357 · Goblins on the way · Done' },
      ],
    });
    expect(question.choices).toHaveLength(3);
    expect(h.options.openTodo).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();

    const opened = await h.loop.submit('the Washington one', h.signal);
    expect(opened.phase).toBe('success');
    expect(h.options.openTodo).toHaveBeenCalledOnce();
    expect(h.options.openTodo).toHaveBeenCalledWith(356);
    expect(h.model).toHaveBeenCalledTimes(2);
    expect(h.execute).not.toHaveBeenCalled();
    expect(h.loop.confirmationPending).toBe(false);
  });
  it.each(['#374', '374', 'Number 374'])('maps offered story number %s to the same issued todoRef without a choice-model turn', async reply => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Burtonsville'; h.todo.localId = 372;
    h.board.columns.backlog.push(
      { id: 92, localId: 373, title: 'Goblins in Washington', status: 'backlog', columnKey: 'backlog' },
      { id: 93, localId: 374, title: 'Goblins on the way', status: 'backlog', columnKey: 'backlog' },
    );
    const question = await h.loop.submit('Open the Goblin story', h.signal);
    const offeredRef = question.choices![2].id;
    expect(offeredRef).toMatch(/^todo_/);
    const run = vi.spyOn(h.registry, 'run');
    expect((await h.loop.submit(reply, h.signal)).phase).toBe('success');
    expect(run.mock.calls[0][0]).toEqual(skill('todos.open', { todoRef: offeredRef }));
    expect(h.options.openTodo).toHaveBeenCalledOnce();
    expect(h.options.openTodo).toHaveBeenCalledWith(374);
    expect(h.model).toHaveBeenCalledTimes(2);
  });
  it.each([
    ['the Washington one', 373, 1],
    ['the first one', 372, 0],
    ['the second one', 373, 1],
  ])('resolves unique offered natural choice %s locally', async (reply, localId, choiceIndex) => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Burtonsville'; h.todo.localId = 372;
    h.board.columns.backlog.push(
      { id: 92, localId: 373, title: 'Goblins in Washington', status: 'backlog', columnKey: 'backlog' },
      { id: 93, localId: 374, title: 'Goblins on the way', status: 'backlog', columnKey: 'backlog' },
    );
    const question = await h.loop.submit('Open the Goblin story', h.signal);
    expect(question.choices![choiceIndex].id).toMatch(/^todo_/);
    expect((await h.loop.submit(reply, h.signal)).phase).toBe('success');
    expect(h.options.openTodo).toHaveBeenCalledWith(localId);
    expect(h.model).toHaveBeenCalledTimes(2);
  });
  it.each(['Done', 'whatever you want', 'the Washington one #374', 'the first and second one'])('does not locally select a non-unique, unrelated or conflicting choice reply: %s', async reply => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Burtonsville'; h.todo.localId = 372;
    h.board.columns.done.push(
      { id: 92, localId: 373, title: 'Goblins in Washington', status: 'done', columnKey: 'done' },
      { id: 93, localId: 374, title: 'Goblins on the way', status: 'done', columnKey: 'done' },
    );
    expect((await h.loop.submit('Open the Goblin story', h.signal)).phase).toBe('question');
    h.steps.push({ kind: 'ask_user', text: 'Please choose one offered story.' });
    const stillPending = await h.loop.submit(reply, h.signal);
    expect(stillPending.phase).toBe('question');
    expect(stillPending.choices).toHaveLength(3);
    expect(JSON.parse(h.model.mock.calls[2][0]).pending).toMatchObject({ kind: 'choice', resource: 'todo' });
    expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('prepares the exact physical mark-as-Done move when Nano emits the contracted skill', async () => {
    const expected = skill('todos.move', { reference: 'Goblins in Washington', lane: 'Done' });
    const h = harness([expected, finish]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    const run = vi.spyOn(h.registry, 'run');

    const view = await h.loop.submit('Mark the story "Goblins in Washington" as "Done"', h.signal);

    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Goblins in Washington');
    expect(view.text).toContain('Done');
    expect(run.mock.calls[0][0]).toEqual(expected);
    expect(h.loop.currentState).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('keeps real move ambiguity as pendingChoice and resolves an offered number locally', async () => {
    const h = harness([skill('todos.move', { reference: 'Goblins in Washington', lane: 'Done' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push({ id: 92, localId: 370, title: 'Goblins in Washington', status: 'backlog', columnKey: 'backlog' });
    const run = vi.spyOn(h.registry, 'run');

    const question = await h.loop.submit('Mark Goblins in Washington as Done', h.signal);
    expect(question.phase).toBe('question');
    expect(question.choices).toHaveLength(2);
    expect(h.loop.currentState).toEqual({ kind: 'choice' });
    const offeredRef = question.choices![0].id;
    h.steps.push(finish);
    h.events.length = 0;

    const confirmation = await h.loop.submit('#369', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(run.mock.calls[1][0]).toEqual(skill('todos.move', { todoRef: offeredRef, lane: 'Done' }));
    expect(h.events.indexOf('todos_get')).toBeLessThan(h.events.indexOf('model'));
    expect(h.model).toHaveBeenCalledTimes(3);
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('retains a structured missing move reference and fills #369 locally', async () => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const events: Record<string, unknown>[] = [];
    const debug = vi.spyOn(console, 'debug').mockImplementation((name, fields) => {
      if (name === 'VoiceFlow trace') events.push(fields as Record<string, unknown>);
    });
    try {
      const h = harness([
        { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
        finish,
      ]);
      h.todo.title = 'Goblins in Washington';
      h.todo.localId = 369;
      const run = vi.spyOn(h.registry, 'run');

      const question = await h.loop.submit('Mark a story as Done', h.signal);
      expect(question).toEqual({ phase: 'question', text: 'Which story?' });
      expect(h.loop.currentState).toEqual({
        kind: 'clarification',
        skillClarification: { skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference' },
      });
      expect(run).not.toHaveBeenCalled();
      expect(h.model).toHaveBeenCalledOnce();
      h.events.length = 0;

      const confirmation = await h.loop.submit('#369', h.signal);
      expect(confirmation.phase).toBe('confirmation');
      expect(run.mock.calls[0][0]).toEqual(skill('todos.move', { lane: 'Done', reference: '#369' }));
      expect(h.events.indexOf('todos_get')).toBeLessThan(h.events.indexOf('model'));
      expect(h.model).toHaveBeenCalledTimes(2);
      expect(h.loop.currentState).toEqual({ kind: 'confirmation', proposalCount: 1 });
      expect(h.execute).not.toHaveBeenCalled();
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'resolve', clarificationKind: 'skill_argument', skill: 'todos.move', missing: 'reference',
      }));
      expect(events).toContainEqual(expect.objectContaining({
        stage: 'interpret', source: 'local_clarification', clarificationKind: 'skill_argument', skill: 'todos.move', missing: 'reference', clarificationResolved: true,
      }));
    } finally {
      debug.mockRestore();
      localStorage.removeItem('scrumboy_debug_voiceflow');
    }
  });
  it('retains a structured move reference and fills the missing lane locally', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: 'Goblins in Washington' }, missing: 'lane', text: 'Which lane?' },
      finish,
    ]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    const run = vi.spyOn(h.registry, 'run');

    expect((await h.loop.submit('Move Goblins in Washington', h.signal)).phase).toBe('question');
    expect(h.loop.currentState).toEqual({
      kind: 'clarification',
      skillClarification: { skill: 'todos.move', arguments: { reference: 'Goblins in Washington' }, missing: 'lane' },
    });
    const confirmation = await h.loop.submit('Done', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(run.mock.calls[0][0]).toEqual(skill('todos.move', { reference: 'Goblins in Washington', lane: 'Done' }));
    expect(h.model).toHaveBeenCalledTimes(2);
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('hands ambiguity from a locally completed clarification to pendingChoice', async () => {
    const h = harness([
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
      { kind: 'ask_user', text: 'Which one?' },
    ]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push({ id: 92, localId: 370, title: 'Goblins in Burtonsville', status: 'backlog', columnKey: 'backlog' });

    expect((await h.loop.submit('Mark a story as Done', h.signal)).phase).toBe('question');
    const choices = await h.loop.submit('Goblin', h.signal);
    expect(choices.phase).toBe('question');
    expect(choices.choices).toHaveLength(2);
    expect(h.loop.currentState).toEqual({ kind: 'choice' });

    h.steps.push(finish);
    const confirmation = await h.loop.submit('#369', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('gives a plain ask_user no local entity-selection authority', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which story?' },
      { kind: 'ask_user', text: 'Please describe the story.' },
    ]);
    const run = vi.spyOn(h.registry, 'run');

    expect(await h.loop.submit('Mark a story as Done', h.signal)).toEqual({ phase: 'question', text: 'Which story?' });
    expect(h.loop.currentState).toEqual({ kind: 'clarification' });
    expect(await h.loop.submit('#369', h.signal)).toEqual({ phase: 'question', text: 'Please describe the story.' });
    expect(h.model).toHaveBeenCalledTimes(2);
    expect(run).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('continues compound work after a deterministic Open choice and confirms the selected Todo mutation', async () => {
    const h = harness([skill('todos.open', { reference: 'Goblin' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Goblins in Burtonsville'; h.todo.localId = 372;
    const washington = { id: 92, localId: 373, title: 'Goblins in Washington', status: 'done', columnKey: 'done', body: '' };
    const onTheWay = { id: 93, localId: 374, title: 'Goblins on the way', status: 'done', columnKey: 'done', body: '' };
    h.board.columns.done.push(washington, onTheWay);
    const question = await h.loop.submit('Open Goblin and append "call Mark" to its notes', h.signal);
    const selectedRef = question.choices![2].id;
    h.steps.push(
      input => {
        expect(JSON.parse(input).pending).toEqual({ kind: 'idle' });
        expect(latestRef(input)).toBe(selectedRef);
        return skill('todos.append_notes', { todoRef: selectedRef, text: 'call Mark' });
      },
      finish,
      { kind: 'confirm' },
    );
    const confirmation = await h.loop.submit('#374', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(confirmation.text).toContain('call Mark');
    expect(h.options.openTodo).toHaveBeenCalledOnce();
    expect(h.options.openTodo).toHaveBeenCalledWith(374);
    expect(h.execute).not.toHaveBeenCalled();
    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(onTheWay.body).toBe('call Mark');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0].entities.localId).toBe(374);
  });
  it('returns the existing not-found result without opening or mutation', async () => {
    const h = harness([skill('todos.open', { reference: 'Purple Elephant' })]);
    const view = await h.loop.submit('Find Purple Elephant', h.signal);
    expect(view).toEqual({ phase: 'error', text: 'No strong todo title match was found in this project.' });
    expect(h.options.openTodo).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();
    expect(h.loop.confirmationPending).toBe(false);
  });
  it('opens and proposes notes from one compound task, then fresh-confirms exactly once', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), input => skill('todos.append_notes', { todoRef: latestRef(input), text: 'How are you?' }), finish, { kind: 'confirm' }]);
    const view = await h.loop.submit('Open Happy Birthday and add to the notes section: How are you?', h.signal);
    expect(view.phase).toBe('confirmation'); expect(h.options.openTodo).toHaveBeenCalledOnce(); expect(h.execute).not.toHaveBeenCalled();
    expect(view.text).toContain('How are you?');
    h.events.length = 0;
    expect((await h.loop.submit('yeah go ahead', h.signal)).phase).toBe('success');
    expect(h.todo.body).toBe('Existing\nHow are you?'); expect(h.execute).toHaveBeenCalledOnce();
    expect(h.events.filter(event => event === 'todos_get')).toHaveLength(1);
    expect(h.events.indexOf('todos_get')).toBeLessThan(h.events.indexOf('execute:todos.append_notes'));
    await h.loop.confirm(h.signal); expect(h.execute).toHaveBeenCalledOnce();
  });
  it('move plus assign has one combined confirmation and preflights both before either executes', async () => {
    const h = harness([skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }), skill('todos.assign', { reference: 'Happy Birthday', member: 'Mark' }), finish, { kind: 'confirm' }]);
    const view = await h.loop.submit('Move Happy Birthday to Done and assign it to Mark', h.signal);
    expect(view.phase).toBe('confirmation'); expect(view.text).toContain('Mark'); expect(view.text).toContain('Done');
    h.events.length = 0; await h.loop.submit('yeah go ahead', h.signal);
    expect(h.execute.mock.calls.map(([ir]) => ir.intent)).toEqual(['todos.move', 'todos.assign']);
    const first = h.events.findIndex(event => event.startsWith('execute:'));
    expect(h.events.slice(0, first).filter(event => event === 'todos_get')).toHaveLength(2);
    expect(h.events.slice(first)).not.toContain('todos_get');
  });
  it('retains only active identity for rename plus tag and fresh-resolves it', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' })], true);
    await h.loop.submit('Open Happy Birthday', h.signal);
    h.steps.push(skill('todos.rename', { reference: 'this', title: 'Login bug' }), skill('todos.add_tag', { reference: 'this', tag: 'urgent' }), finish, { kind: 'confirm' });
    const view = await h.loop.submit('Rename this to Login bug and add the urgent tag', h.signal);
    expect(view.phase).toBe('confirmation'); await h.loop.submit('yes', h.signal);
    expect(h.todo.title).toBe('Login bug'); expect(h.todo.tags).toEqual(['urgent']);
    expect(h.execute).toHaveBeenCalledTimes(2);
  });
  it('supports pronoun notes in a retained session, with no prior trace retained', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' })], true);
    await h.loop.submit('Open Happy Birthday', h.signal);
    h.steps.push(skill('todos.append_notes', { reference: 'its', text: 'How are you?' }), finish);
    expect((await h.loop.submit('Add How are you to its notes', h.signal)).phase).toBe('confirmation');
    const nextInput = JSON.parse(h.model.mock.calls[1][0]);
    expect(nextInput.activeTodoAvailable).toBe(true); expect(nextInput.trace).toHaveLength(1);
    h.todo.id = 999;
    expect((await h.loop.confirm(h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it.each(['The backlog one', 'Number 353'])('selects only offered handles for %s', async reply => {
    const h = harness([skill('todos.open', { reference: 'Bogus' }), { kind: 'ask_user', text: 'Choose imaginary option 999' }]);
    h.todo.title = 'Bogus'; h.todo.localId = 353;
    h.board.columns.done.push({ id: 92, localId: 354, title: 'Bogus', status: 'done', columnKey: 'done' });
    const question = await h.loop.submit('Open Bogus', h.signal);
    expect(question.phase).toBe('question'); expect(question.text).not.toContain('imaginary'); expect(h.options.openTodo).not.toHaveBeenCalled();
    expect((await h.loop.submit(reply, h.signal)).phase).toBe('success'); expect(h.options.openTodo).toHaveBeenCalledWith(353);
  });
  it('rejects a previously issued but currently unoffered handle', async () => {
    let old = '';
    const h = harness([skill('todos.resolve', { reference: 'Happy Birthday' }), input => { old = latestRef(input); return skill('todos.open', { reference: 'Bogus' }); }, { kind: 'ask_user', text: 'Which?' }]);
    h.board.columns.done.push({ id: 92, localId: 353, title: 'Bogus', status: 'done' }, { id: 93, localId: 354, title: 'Bogus', status: 'done' });
    await h.loop.submit('Find Happy Birthday then open Bogus', h.signal);
    h.steps.push(skill('todos.open', { todoRef: old }));
    expect((await h.loop.submit('the other one', h.signal)).phase).toBe('error'); expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('rejects fabricated handles without lookup', async () => {
    const h = harness([skill('todos.open', { todoRef: 'todo_999' })]);
    expect((await h.loop.submit('open it', h.signal)).phase).toBe('error'); expect(h.callTool).not.toHaveBeenCalled(); expect(h.execute).not.toHaveBeenCalled();
  });
  it('rejects invented skills with one local repair and no fallback', async () => {
    const bad = skill('ui.birds_eye_view', {}); const h = harness([bad, bad]);
    expect((await h.loop.submit('Turn on Bird’s Eye View mode', h.signal)).phase).toBe('error');
    expect(h.model).toHaveBeenCalledTimes(2); expect(h.callTool).not.toHaveBeenCalled();
  });
  it.each([true, false])('repairs malformed JSON once; repair valid=%s', async valid => {
    const h = harness(['broken', valid ? finish : 'still broken']);
    expect((await h.loop.submit('hello', h.signal)).phase).toBe(valid ? 'success' : 'error');
    expect(h.model).toHaveBeenCalledTimes(2); expect(JSON.parse(h.model.mock.calls[1][0]).repair).toContain('Expected strict JSON');
  });
  it('stops repeated resolve calls at the skill limit with no further model invocation', async () => {
    const h = harness(Array.from({ length: 20 }, () => skill('todos.resolve', { reference: 'Happy Birthday' })));
    expect((await h.loop.submit('keep resolving', h.signal)).phase).toBe('error'); expect(h.model).toHaveBeenCalledTimes(AGENT_LIMITS.skillCalls);
  });
  it('counts repairs and clarification across the total model-step bound', async () => {
    const h = harness(Array.from({ length: 20 }, () => ({ kind: 'ask_user', text: 'Which title?' })));
    for (let index = 0; index < AGENT_LIMITS.modelSteps; index++) expect((await h.loop.submit('unsure', h.signal)).phase).toBe('question');
    expect((await h.loop.submit('unsure', h.signal)).phase).toBe('error'); expect(h.model).toHaveBeenCalledTimes(AGENT_LIMITS.modelSteps);
  });
  it.each(['decline', 'cancel'] as const)('natural %s clears proposals without mutation', async kind => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish, { kind }]);
    await h.loop.submit('delete it', h.signal); await h.loop.submit(kind === 'decline' ? 'no thanks' : 'cancel that', h.signal);
    expect(h.execute).not.toHaveBeenCalled(); expect(h.loop.pending).toBe(false);
  });
  it('fails closed when an unsupported create dependency attempts a future identity', async () => {
    const h = harness([skill('todos.create', { title: 'Fix OAuth', lane: 'Backlog' }), skill('todos.assign', { todoRef: 'todo_future', member: 'Mark' })]);
    expect((await h.loop.submit('Create Fix OAuth in Backlog and assign it to Mark', h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it('allows a safe clarification for create dependency without execution', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Create first, then assign in a new task?' }]);
    expect((await h.loop.submit('Create Fix OAuth and assign it to Mark', h.signal)).phase).toBe('question'); expect(h.execute).not.toHaveBeenCalled();
  });
  it.each(['project', 'account', 'server'] as const)('invalidates %s context', async kind => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); await h.loop.submit('delete', h.signal);
    if (kind === 'server') h.loop.invalidate(); else h.setContext({ ...h.context(), ...(kind === 'project' ? { projectId: 2 } : { userId: 8 }) });
    expect((await h.loop.confirm(h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it('returns authoritative analytics without a beautification call', async () => {
    const h = harness([skill('analytics.count_completed', { range: 'this_week' }), finish]);
    const result = await h.loop.submit('count completed this week', h.signal);
    expect(result.text).toContain('3 stories'); expect(h.model).toHaveBeenCalledTimes(2);
    expect(h.callTool).toHaveBeenCalledWith('todos_countCompleted', expect.objectContaining({ period: 'this-week' }), expect.anything());
  });
  it('does not send board, member list, tags, IDs, or unrelated notes to the model', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), finish]); h.todo.body = 'SECRET_NOTES';
    await h.loop.submit('Open Happy Birthday', h.signal);
    const inputs = JSON.stringify(h.model.mock.calls);
    for (const secret of ['SECRET_NOTES', 'mark@example.test', 'urgent', 'projectId', 'columnKey', 'userId', 'localId']) expect(inputs).not.toContain(secret);
  });
});

describe('state-aware voice agent protocol lifecycle', () => {
  const pendingOf = (h: ReturnType<typeof harness>, call: number) => JSON.parse(h.model.mock.calls[call][0]).pending;

  it('keeps a prepared create through a finish carrying harmless text (device case 1)', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which lane?' },
      skill('todos.create', { title: 'hey, the night is young', lane: 'Backlog' }),
      '{"kind":"finish","text":"Done"}',
      { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('create a to-do called hey, the night is young.', h.signal)).phase).toBe('question');
    expect(pendingOf(h, 0)).toEqual({ kind: 'idle' });

    const confirmation = await h.loop.submit('in backlog', h.signal);
    expect(confirmation.phase).toBe('confirmation');
    expect(confirmation.text).toMatch(/hey, the night is young/i);
    expect(h.execute).not.toHaveBeenCalled();
    expect(h.model).toHaveBeenCalledTimes(3);
    expect(pendingOf(h, 1)).toEqual({ kind: 'clarification' });
    expect(pendingOf(h, 2)).toEqual({ kind: 'proposals_ready', proposalCount: 1 });

    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0].intent).toBe('todos.create');
  });

  it('reads a spoken "done" during clarification as the Done lane (device case 2)', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which lane should Oh My God go in?' },
      skill('todos.create', { title: 'Oh My God', lane: 'Done' }),
      finish,
      { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('create a story called Oh My God.', h.signal)).phase).toBe('question');
    expect(h.loop.confirmationPending).toBe(false);

    const view = await h.loop.submit('done.', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Oh My God');
    expect(pendingOf(h, 1)).toEqual({ kind: 'clarification' });
    expect(h.execute).not.toHaveBeenCalled();

    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0].entities.columnKey).toBe('done');
  });

  it('repairs a confirmation action emitted during clarification instead of failing the task', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which lane should Oh My God go in?' },
      { kind: 'confirm' },
      skill('todos.create', { title: 'Oh My God', lane: 'Done' }),
      finish,
    ]);
    await h.loop.submit('create a story called Oh My God.', h.signal);
    expect((await h.loop.submit('done.', h.signal)).phase).toBe('confirmation');
    const repair = JSON.parse(h.model.mock.calls[2][0]).repair;
    expect(repair).toContain('Envelope confirm is not allowed in state clarification');
    expect(repair).toContain('Current state: clarification');
    expect(repair).toContain('Allowed envelope kinds: skill_call, clarify_skill, ask_user');
    expect(repair).not.toContain('"kind":"confirm"');
  });

  it('repairs a finish emitted for a spoken done clarification and creates in Done', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which lane should Oh My God go in?' },
      finish,
      skill('todos.create', { title: 'Oh My God', lane: 'Done' }),
      finish,
    ]);
    expect((await h.loop.submit('create a story called Oh My God.', h.signal)).phase).toBe('question');
    const view = await h.loop.submit('done.', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Oh My God');
    expect(h.execute).not.toHaveBeenCalled();
    expect(pendingOf(h, 1)).toEqual({ kind: 'clarification' });
    const repair = JSON.parse(h.model.mock.calls[2][0]).repair;
    expect(repair).toContain('Envelope finish is not allowed in state clarification');
    expect(repair).toContain('Current state: clarification');
    expect(repair).toContain('Allowed envelope kinds: skill_call, clarify_skill, ask_user');
    expect(pendingOf(h, 3)).toEqual({ kind: 'proposals_ready', proposalCount: 1 });
  });

  it('cannot terminate an unanswered clarification through finish', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Which lane?' }, finish, finish]);
    expect((await h.loop.submit('create a story called Oh My God.', h.signal)).phase).toBe('question');
    const view = await h.loop.submit('done.', h.signal);
    expect(view.phase).toBe('error');
    expect(view.text).not.toContain('No changes needed');
    expect(h.loop.pending).toBe(false);
    expect(h.execute).not.toHaveBeenCalled();
    expect(JSON.parse(h.model.mock.calls[2][0]).repair).toContain('Envelope finish is not allowed in state clarification');
  });

  it('never reports confirmation while a free-text clarification is outstanding', async () => {
    const h = harness([{ kind: 'ask_user', text: 'Which lane?' }, { kind: 'ask_user', text: 'Still which lane?' }]);
    expect((await h.loop.submit('create a story', h.signal)).phase).toBe('question');
    expect(h.loop.confirmationPending).toBe(false);
    expect((await h.loop.submit('done.', h.signal)).phase).toBe('question');
    expect(pendingOf(h, 1)).toEqual({ kind: 'clarification' });
    expect(h.loop.confirmationPending).toBe(false);
  });

  it('never reports clarification while confirmation is outstanding', async () => {
    const h = harness([
      skill('todos.create', { title: 'Oh My God', lane: 'Done' }), finish,
      { kind: 'ask_user', text: 'Which lane?' }, { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('create Oh My God in Done', h.signal)).phase).toBe('confirmation');
    expect(h.loop.confirmationPending).toBe(true);
    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(pendingOf(h, 2)).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(pendingOf(h, 3)).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(JSON.parse(h.model.mock.calls[3][0]).repair).toContain('Envelope ask_user is not allowed in state confirmation');
    expect(h.execute).toHaveBeenCalledOnce();
  });

  it('repairs a finish emitted for yes while confirmation is pending and then confirms once', async () => {
    const h = harness([
      skill('todos.create', { title: 'Oh My God', lane: 'Done' }), finish,
      finish, { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('create a story called Oh My God.', h.signal)).phase).toBe('confirmation');
    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(pendingOf(h, 2)).toEqual({ kind: 'confirmation', proposalCount: 1 });
    const repair = JSON.parse(h.model.mock.calls[3][0]).repair;
    expect(repair).toContain('Envelope finish is not allowed in state confirmation');
    expect(repair).toContain('Current state: confirmation');
    expect(repair).toContain('Allowed envelope kinds: skill_call, confirm, decline, cancel');
    expect(pendingOf(h, 3)).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(h.execute).toHaveBeenCalledOnce();
  });

  it('qualified additional work leaves confirmation, then finishes from proposals_ready into a new confirmation', async () => {
    const h = harness([
      skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }), finish,
      skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' }), finish,
      { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('Move Happy Birthday to Done', h.signal)).phase).toBe('confirmation');
    expect(h.loop.confirmationPending).toBe(true);
    const next = await h.loop.submit('Yes but also tag it urgent', h.signal);
    expect(next.phase).toBe('confirmation');
    expect(next.text).toContain('urgent');
    expect(pendingOf(h, 2)).toEqual({ kind: 'confirmation', proposalCount: 1 });
    expect(pendingOf(h, 3)).toEqual({ kind: 'proposals_ready', proposalCount: 2 });
    expect(h.execute).not.toHaveBeenCalled();
    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledTimes(2);
  });

  it('prepares a compound batch across proposals_ready turns before one confirmation', async () => {
    const h = harness([
      skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }),
      skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' }),
      finish,
      { kind: 'confirm' },
    ]);
    const view = await h.loop.submit('Move Happy Birthday to Done and tag it urgent', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Done'); expect(view.text).toContain('urgent');
    expect(pendingOf(h, 0)).toEqual({ kind: 'idle' });
    expect(pendingOf(h, 1)).toEqual({ kind: 'proposals_ready', proposalCount: 1 });
    expect(pendingOf(h, 2)).toEqual({ kind: 'proposals_ready', proposalCount: 2 });
    expect(h.execute).not.toHaveBeenCalled();

    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(h.execute.mock.calls.map(([ir]) => ir.intent)).toEqual(['todos.move', 'todos.add_tag']);
  });

  it('recovers a premature confirm after create as finish, then confirms once (device case A)', async () => {
    localStorage.setItem('scrumboy_debug_voiceflow', '1');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => undefined);
    const h = harness([
      skill('todos.create', { title: 'I Am King' }),
      { kind: 'confirm' },
      { kind: 'confirm' },
    ]);
    try {
      const view = await h.loop.submit('create a story called I Am King.', h.signal);
      expect(view.phase).toBe('confirmation');
      expect(view.text).toMatch(/I Am King/i);
      expect(h.execute).not.toHaveBeenCalled();
      expect(h.model).toHaveBeenCalledTimes(2);
      expect(pendingOf(h, 1)).toEqual({ kind: 'proposals_ready', proposalCount: 1 });
      expect(JSON.parse(h.model.mock.calls[1][0]).repair).toBeUndefined();
      expect(debug.mock.calls.some(([, details]) => details?.stage === 'interpret' && details?.state === 'proposals_ready' && details?.interpretationKind === 'finish' && details?.recoveredFrom === 'confirm')).toBe(true);
      expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
      expect(h.execute).toHaveBeenCalledOnce();
      expect(h.execute.mock.calls[0][0].intent).toBe('todos.create');
    } finally {
      debug.mockRestore();
      localStorage.removeItem('scrumboy_debug_voiceflow');
    }
  });

  it('recovers a premature confirm after a Backlog clarification (device case B)', async () => {
    const h = harness([
      { kind: 'ask_user', text: 'Which lane?' },
      skill('todos.create', { title: 'I Am a Boy', lane: 'Backlog' }),
      { kind: 'confirm' },
      { kind: 'confirm' },
    ]);
    expect((await h.loop.submit('create a story called I Am a Boy', h.signal)).phase).toBe('question');
    const view = await h.loop.submit('backlog.', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toMatch(/I Am a Boy/i);
    expect(h.execute).not.toHaveBeenCalled();
    expect(pendingOf(h, 1)).toEqual({ kind: 'clarification' });
    expect(pendingOf(h, 2)).toEqual({ kind: 'proposals_ready', proposalCount: 1 });
    expect((await h.loop.submit('yes', h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0].entities.columnKey).toBe('backlog');
  });

  it.each(['idle', 'clarification', 'choice'] as const)('does not recover confirm in %s', async state => {
    if (state === 'idle') {
      const h = harness([{ kind: 'confirm' }, { kind: 'confirm' }]);
      expect((await h.loop.submit('create a story called I Am King.', h.signal)).phase).toBe('error');
      expect(JSON.parse(h.model.mock.calls[1][0]).repair).toContain('Envelope confirm is not allowed in state idle');
      expect(h.execute).not.toHaveBeenCalled();
      return;
    }
    if (state === 'clarification') {
      const h = harness([{ kind: 'ask_user', text: 'Which lane?' }, { kind: 'confirm' }, { kind: 'confirm' }]);
      await h.loop.submit('create a story called I Am a Boy', h.signal);
      expect((await h.loop.submit('backlog.', h.signal)).phase).toBe('error');
      expect(JSON.parse(h.model.mock.calls[2][0]).repair).toContain('Envelope confirm is not allowed in state clarification');
      expect(h.execute).not.toHaveBeenCalled();
      return;
    }
    const h = harness([skill('todos.open', { reference: 'Bogus' }), { kind: 'ask_user', text: 'Which one?' }, { kind: 'confirm' }, { kind: 'confirm' }]);
    h.todo.title = 'Bogus'; h.todo.localId = 353;
    h.board.columns.done.push({ id: 92, localId: 354, title: 'Bogus', status: 'done', columnKey: 'done' });
    expect((await h.loop.submit('Open Bogus', h.signal)).phase).toBe('question');
    expect((await h.loop.submit('an unrelated answer', h.signal)).phase).toBe('error');
    expect(JSON.parse(h.model.mock.calls[3][0]).repair).toContain('Envelope confirm is not allowed in state choice');
    expect(h.options.openTodo).not.toHaveBeenCalled();
  });

  it('repairs an illegal confirm after Open instead of treating the task as finished', async () => {
    const h = harness([skill('todos.open', { reference: 'Happy Birthday' }), { kind: 'confirm' }, finish]);
    const view = await h.loop.submit('Open Happy Birthday and continue the requested work', h.signal);
    expect(view.phase).toBe('success');
    expect(h.options.openTodo).toHaveBeenCalledOnce();
    expect(h.model).toHaveBeenCalledTimes(3);
    expect(JSON.parse(h.model.mock.calls[2][0]).repair).toContain('Envelope confirm is not allowed in state idle');
  });

  it.each(['decline', 'cancel'] as const)('does not coerce proposals_ready %s into finish', async kind => {
    const h = harness([skill('todos.create', { title: 'I Am King' }), { kind }, { kind }]);
    expect((await h.loop.submit('create a story called I Am King.', h.signal)).phase).toBe('error');
    expect(h.execute).not.toHaveBeenCalled();
    expect(h.loop.confirmationPending).toBe(false);
    expect(JSON.parse(h.model.mock.calls[2][0]).repair).toContain(`Envelope ${kind} is not allowed in state proposals_ready`);
  });

  it('reports the choice state to the model when a reply cannot be matched locally', async () => {
    const h = harness([skill('todos.open', { reference: 'Bogus' }), { kind: 'ask_user', text: 'Which one?' }]);
    h.todo.title = 'Bogus'; h.todo.localId = 353;
    h.board.columns.done.push({ id: 92, localId: 354, title: 'Bogus', status: 'done', columnKey: 'done' });
    expect((await h.loop.submit('Open Bogus', h.signal)).phase).toBe('question');
    h.steps.push(input => skill('todos.open', { todoRef: JSON.parse(input).pending.choices[0].handle }), finish);
    expect((await h.loop.submit('pick whichever', h.signal)).phase).toBe('success');
    expect(pendingOf(h, 2)).toMatchObject({ kind: 'choice', resource: 'todo' });
  });
});
