// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { harness, skill, finish } from './agent.test.utils.js';
import { VoiceAgentResourceHandles } from './agent-resources.js';
describe('bounded skill registry', () => {
  it.each([
    ['todos.create', { title: 'Fix OAuth', lane: 'Backlog' }, 'todos.create'],
    ['todos.rename', { reference: 'Happy Birthday', title: ' Login bug ' }, 'todos.update_title'],
    ['todos.replace_notes', { reference: 'Happy Birthday', text: 'Replacement' }, 'todos.replace_notes'],
    ['todos.unassign', { reference: 'Happy Birthday' }, 'todos.unassign'],
    ['todos.remove_tag', { reference: 'Happy Birthday', tag: 'urgent' }, 'todos.remove_tag'],
    ['todos.delete', { reference: 'Happy Birthday' }, 'todos.delete'],
  ])('prepares %s and uses the existing execution intent only after confirmation', async (name, args, intent) => {
    const h = harness([skill(name as string, args as object), finish]); h.todo.assigneeUserId = 8; h.todo.tags = ['urgent'];
    expect((await h.loop.submit('action', h.signal)).phase).toBe('confirmation'); expect(h.execute).not.toHaveBeenCalled();
    expect((await h.loop.confirm(h.signal)).phase).toBe('success'); expect(h.execute.mock.calls[0][0].intent).toBe(intent);
  });
  it('operation-aware duplicates select the actionable todo', async () => {
    const h = harness([skill('todos.move', { reference: 'Bogus', lane: 'Done' }), finish]); h.todo.title = 'Bogus';
    h.board.columns.done.push({ id: 92, localId: 353, title: 'Bogus', status: 'done', columnKey: 'done' });
    expect((await h.loop.submit('Move Bogus to Done', h.signal)).phase).toBe('confirmation');
    await h.loop.confirm(h.signal); expect(h.execute.mock.calls[0][0].entities.localId).toBe(355);
  });
  it.each([
    'Goblins in Washington',
    'goblins in washington',
    '"Goblins in Washington"',
    'story Goblins in Washington',
    'the story Goblins in Washington',
    'the todo Goblins in Washington',
    'the to-do Goblins in Washington',
    'the card named Goblins in Washington',
    'the task titled Goblins in Washington',
    'the item Goblins in Washington',
    '#369',
    '369',
    'number 369',
    'Number 369',
    'story number 369',
    'todo number 369',
  ])('prepares the exact #369 move from Agent reference %s without executing before confirmation', async reference => {
    const h = harness([skill('todos.move', { reference, lane: 'Done' }), finish]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;

    const view = await h.loop.submit('mark it done', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(view.text).toContain('Goblins in Washington');
    expect(view.text).toContain('Done');
    expect(h.execute).not.toHaveBeenCalled();

    expect((await h.loop.confirm(h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0]).toMatchObject({ intent: 'todos.move', entities: { localId: 369, toColumnKey: 'done' } });
  });
  it.each([
    'Billy Mongoose',
    'billy mongoose',
    '"Billy Mongoose"',
    'story Billy Mongoose',
    'the story Billy Mongoose',
    'the story called Billy Mongoose',
    '#369',
    '369',
    'Number 369',
  ])('prepares a dangerous #369 delete from Agent reference %s without opening or executing', async reference => {
    const h = harness([skill('todos.delete', { reference }), finish]);
    h.todo.title = 'Billy Mongoose';
    h.todo.localId = 369;

    const view = await h.loop.submit('I want you to delete Billy Mongoose', h.signal);
    expect(view).toMatchObject({ phase: 'confirmation', danger: true, confirmLabel: 'Delete' });
    expect(view.text).toBe('Delete todo #369: Billy Mongoose?');
    expect(h.options.openTodo).not.toHaveBeenCalled();
    expect(h.execute).not.toHaveBeenCalled();

    expect((await h.loop.confirm(h.signal)).phase).toBe('success');
    expect(h.execute).toHaveBeenCalledOnce();
    expect(h.execute.mock.calls[0][0]).toMatchObject({ intent: 'todos.delete', entities: { localId: 369 } });
    expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('uses stripped-title precedence for delete instead of a fuzzy wrapped shadow', async () => {
    const h = harness([skill('todos.delete', { reference: 'the story Billy Mongoose' }), finish]);
    h.todo.title = 'Billy Mongoose';
    h.todo.localId = 369;
    h.board.columns.backlog.push({ id: 92, localId: 410, title: 'The Story of Billy Mongoose', status: 'backlog', columnKey: 'backlog' });

    expect((await h.loop.submit('delete it', h.signal)).phase).toBe('confirmation');
    await h.loop.confirm(h.signal);
    expect(h.execute.mock.calls[0][0].entities.localId).toBe(369);
    expect(h.options.openTodo).not.toHaveBeenCalled();
  });
  it('does not let a fuzzy raw wrapper shadow the exact stripped title', async () => {
    const h = harness([skill('todos.move', { reference: 'the story Goblins in Washington', lane: 'Done' }), finish]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push({ id: 92, localId: 410, title: 'The Story of Goblins in Washington', status: 'backlog', columnKey: 'backlog' });

    expect((await h.loop.submit('mark it done', h.signal)).phase).toBe('confirmation');
    expect(h.execute).not.toHaveBeenCalled();
    await h.loop.confirm(h.signal);
    expect(h.execute.mock.calls[0][0].entities.localId).toBe(369);
  });
  it('lets an original literal wrapper-prefixed title win before wrapper normalization', async () => {
    const h = harness([skill('todos.move', { reference: 'the story Goblins in Washington', lane: 'Done' }), finish]);
    h.todo.title = 'Goblins in Washington';
    h.todo.localId = 369;
    h.board.columns.backlog.push({ id: 92, localId: 410, title: 'the story Goblins in Washington', status: 'backlog', columnKey: 'backlog' });

    const view = await h.loop.submit('mark it done', h.signal);
    expect(view.phase).toBe('confirmation');
    expect(h.execute).not.toHaveBeenCalled();
    await h.loop.confirm(h.signal);
    expect(h.execute.mock.calls[0][0].entities.localId).toBe(410);
  });
  it('fails closed when wrapper stripping leaves only an equally fuzzy shadow title', async () => {
    const h = harness([skill('todos.move', { reference: 'the story Goblins in Washington', lane: 'Done' })]);
    h.todo.title = 'The Story of Goblins in Washington';
    h.todo.localId = 410;

    const view = await h.loop.submit('mark it done', h.signal);
    expect(view).toMatchObject({ phase: 'error' });
    expect(h.execute).not.toHaveBeenCalled();
  });
  it('retains normal fuzzy resolution when the stripped reference is strictly stronger', async () => {
    const h = harness([skill('todos.move', { reference: 'the story Goblins in Washington', lane: 'Done' }), finish]);
    h.todo.title = 'Goblins in Washington Today';
    h.todo.localId = 369;

    expect((await h.loop.submit('mark it done', h.signal)).phase).toBe('confirmation');
    await h.loop.confirm(h.signal);
    expect(h.execute.mock.calls[0][0].entities.localId).toBe(369);
  });
  it.each(['todos.move', 'todos.assign', 'todos.unassign', 'todos.add_tag', 'todos.remove_tag', 'todos.rename', 'todos.replace_notes'])('%s preserves no-op behavior', async name => {
    const args = name === 'todos.move' ? { lane: 'Backlog' } : name === 'todos.assign' ? { member: 'Mark' }
      : name.includes('tag') ? { tag: 'urgent' } : name === 'todos.rename' ? { title: 'Happy Birthday' } : name === 'todos.replace_notes' ? { text: 'Existing' } : {};
    const h = harness([skill(name, { reference: 'Happy Birthday', ...args }), finish]);
    if (name === 'todos.assign') h.todo.assigneeUserId = 8;
    if (name === 'todos.add_tag') h.todo.tags = ['urgent'];
    expect((await h.loop.submit('already satisfied', h.signal)).phase).toBe('success'); expect(h.execute).not.toHaveBeenCalled();
  });
  it.each(['member', 'tag', 'lane'] as const)('rejects fabricated %s handles before authority lookup', async field => {
    const name = field === 'member' ? 'todos.assign' : field === 'tag' ? 'todos.add_tag' : 'todos.move';
    const h = harness([skill(name, { reference: 'Happy Birthday', [field]: `${field}_999` })]);
    expect((await h.loop.submit('invent resource', h.signal)).phase).toBe('error'); expect(h.callTool).not.toHaveBeenCalled();
  });
  it.each(['member', 'tag'] as const)('returns bounded %s choices and reuses only the selected handle', async resource => {
    const h = harness();
    if (resource === 'member') h.context().members = [{ userId: 8, name: 'Mark A', email: 'a@example.test', role: 'maintainer' }, { userId: 9, name: 'Mark B', email: 'b@example.test', role: 'maintainer' }];
    else h.board.tags = [{ name: 'urgent A' }, { name: 'urgent B' }];
    const name = resource === 'member' ? 'todos.assign' : 'todos.add_tag';
    h.steps.push(skill(name, { reference: 'Happy Birthday', [resource]: resource === 'member' ? 'Mark' : 'urgent' }), { kind: 'ask_user', text: 'Which?' });
    const question = await h.loop.submit('choose resource', h.signal);
    expect(question.choices).toHaveLength(2);
    const selectedRef = question.choices![1].id;
    h.steps.push(input => {
      const selectedCall = [...JSON.parse(input).trace].reverse().find(entry => entry.agent?.kind === 'skill_call').agent;
      expect(selectedCall.arguments[resource]).toBe(selectedRef);
      return finish;
    });
    expect((await h.loop.submit('the second', h.signal)).phase).toBe('confirmation');
    expect((await h.loop.confirm(h.signal)).phase).toBe('success'); expect(h.execute).toHaveBeenCalledOnce();
    if (resource === 'member') expect(h.execute.mock.calls[0][0].entities.assigneeUserId).toBe(9);
    else expect(h.execute.mock.calls[0][0].entities.tags).toContain('urgent B');
  });
  it('inspect returns only requested bounded fields', async () => {
    const h = harness([skill('todos.inspect', { reference: 'Happy Birthday', fields: ['notes'] }), finish]); h.todo.body = 'a'.repeat(10000);
    const result = await h.loop.submit('read notes', h.signal); expect(result.text.length).toBe(320);
    const resultInput = h.model.mock.calls[1][0]; expect(resultInput).not.toContain('Existing'); expect(resultInput.length).toBeLessThan(2048);
  });
  it('viewer cannot prepare mutation, but may open', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' })]); h.context().role = 'viewer';
    expect((await h.loop.submit('delete', h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
    h.steps.push(skill('todos.open', { reference: 'Happy Birthday' }), finish);
    expect((await h.loop.submit('open', h.signal)).phase).toBe('success');
  });
  it('handles cannot alias across tasks and are revoked on clear', () => {
    const first = new VoiceAgentResourceHandles(); const second = new VoiceAgentResourceHandles();
    const resource = { kind: 'todo' as const, localId: 355, rowId: 91 };
    const handle = first.issue(resource); expect(second.issue(resource)).not.toBe(handle);
    expect(() => second.get(handle, 'todo')).toThrow(); first.clear(); expect(() => first.get(handle, 'todo')).toThrow();
  });
});
