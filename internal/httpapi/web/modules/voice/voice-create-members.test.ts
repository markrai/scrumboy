// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { readVoiceCreateMembers } from './voice-create-members.js';

describe('Voice Create authoritative member adapter', () => {
  it('reads the same richer members_list projection used by production', async () => {
    const callTool = vi.fn(async () => ({
      items: [{ userId: 8, name: 'Mark Rai', email: 'mark@example.test', role: 'maintainer', createdAt: 'ignored' }],
    }));
    const signal = new AbortController().signal;

    const members = await readVoiceCreateMembers('alpha', signal, callTool as never);

    expect(callTool).toHaveBeenCalledWith('members_list', { projectSlug: 'alpha' }, { signal });
    expect(members).toEqual([{ userId: 8, name: 'Mark Rai', email: 'mark@example.test', role: 'maintainer' }]);
    expect(Object.isFrozen(members)).toBe(true);
    expect(Object.isFrozen(members[0])).toBe(true);
  });

  it('fails closed when given the reduced board member projection without email', async () => {
    const callTool = vi.fn(async () => ({ items: [{ userId: 8, name: 'Mark Rai', role: 'maintainer' }] }));

    await expect(readVoiceCreateMembers('alpha', new AbortController().signal, callTool as never)).rejects.toThrow('network');
  });
});
