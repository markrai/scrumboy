// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { harness, skill, finish } from './agent.test.utils.js';
import { AGENT_LIMITS } from './agent-protocol.js';
describe('authoritative mutation proposals', () => {
  it.each(['notes', 'permission', 'member', 'lane', 'tag'])('preflight fails the whole batch after changed %s', async changed => {
    const h = harness([skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }),
      changed === 'member' ? skill('todos.assign', { reference: 'Happy Birthday', member: 'Mark' })
        : changed === 'tag' ? skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' })
        : skill('todos.append_notes', { reference: 'Happy Birthday', text: 'Hello' }), finish]);
    expect((await h.loop.submit('compound', h.signal)).phase).toBe('confirmation');
    if (changed === 'notes') h.todo.body = 'Someone changed this';
    if (changed === 'permission') h.context().role = 'viewer';
    if (changed === 'member') h.context().members = [];
    if (changed === 'tag') h.board.tags = [];
    if (changed === 'lane') { h.board.columnOrder = h.board.columnOrder!.filter(lane => lane.key !== 'done'); delete h.board.columns.done; }
    expect((await h.loop.confirm(h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it('reports exact succeeded/failed/unattempted operations and stops without rollback', async () => {
    const h = harness([skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }), skill('todos.assign', { reference: 'Happy Birthday', member: 'Mark' }), skill('todos.rename', { reference: 'Happy Birthday', title: 'New title' }), finish]);
    await h.loop.submit('compound', h.signal);
    h.execute.mockImplementationOnce(async () => ({ ok: true })).mockRejectedValueOnce(new Error('network'));
    const result = await h.loop.confirm(h.signal);
    expect(result.phase).toBe('error'); expect(result.text).toContain('Succeeded: Move'); expect(result.text).toContain('Failed or unconfirmed: Assign'); expect(result.text).toContain('Not attempted:'); expect(result.text).toContain('New title');
    expect(h.execute).toHaveBeenCalledTimes(2); expect(h.loop.pending).toBe(false);
  });
  it('does not report mutation failure when only post-execution refresh fails', async () => {
    const h = harness([skill('todos.delete', { reference: 'Happy Birthday' }), finish]); await h.loop.submit('delete', h.signal);
    h.options.refreshBoard.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('offline'));
    const result = await h.loop.confirm(h.signal);
    expect(result.phase).toBe('error'); expect(result.text).toContain('Succeeded: Delete'); expect(result.text).toContain('Failed or unconfirmed: —');
    expect(h.execute).toHaveBeenCalledOnce();
  });
  it('rejects a fifth proposal without executing any', async () => {
    const h = harness();
    for (let index = 0; index <= AGENT_LIMITS.proposals; index++) {
      const title = `Target ${index}`;
      h.board.columns.backlog.push({ id: 100 + index, localId: 100 + index, title, status: 'backlog' });
      h.steps.push(skill('todos.delete', { reference: title }));
    }
    expect((await h.loop.submit('delete five todos', h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it.each(['todos.append_notes', 'todos.add_tag'])('rejects overlapping %s writes rather than losing an earlier effect', async name => {
    const args = name === 'todos.append_notes' ? { text: 'Hi' } : { tag: 'urgent' };
    const h = harness([skill(name, { reference: 'Happy Birthday', ...args }), skill(name, { reference: 'Happy Birthday', ...args })]);
    expect((await h.loop.submit('repeat update', h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
  it('additional confirmation work requires a new full confirmation', async () => {
    const h = harness([skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }), finish,
      skill('todos.add_tag', { reference: 'Happy Birthday', tag: 'urgent' }), finish, { kind: 'confirm' }]);
    await h.loop.submit('Move Happy Birthday to Done', h.signal);
    const view = await h.loop.submit('Yes but also tag it urgent', h.signal);
    expect(view.phase).toBe('confirmation'); expect(view.text).toContain('urgent'); expect(h.execute).not.toHaveBeenCalled();
    await h.loop.submit('yeah go ahead', h.signal); expect(h.execute).toHaveBeenCalledTimes(2);
  });
  it('no-op after preparation invalidates review instead of executing part of it', async () => {
    const h = harness([skill('todos.move', { reference: 'Happy Birthday', lane: 'Done' }), finish]); await h.loop.submit('move', h.signal);
    h.todo.columnKey = 'done';
    expect((await h.loop.confirm(h.signal)).phase).toBe('error'); expect(h.execute).not.toHaveBeenCalled();
  });
});
