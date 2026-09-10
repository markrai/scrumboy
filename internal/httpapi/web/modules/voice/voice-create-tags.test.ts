// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import { combineVoiceCreateTags, readVoiceCreateTags } from './voice-create-tags.js';

describe('Voice Create authoritative tag adapter', () => {
  it('combines the full current-project catalogue with the caller-owned cross-project library', async () => {
    const signal = new AbortController().signal;
    const fetcher = vi.fn(async (path: string) => path === '/api/tags/mine'
      ? [{ tagId: 31, name: 'architecture', deleteScope: 'mine' }, { tagId: 32, name: 'mobile' }]
      : [{ tagId: 11, name: 'backend', count: 2 }, { tagId: 12, name: 'mobile', count: 1 }]);

    const tags = await readVoiceCreateTags('alpha team', signal, fetcher);

    expect(fetcher).toHaveBeenCalledWith('/api/board/alpha%20team/tags', { signal });
    expect(fetcher).toHaveBeenCalledWith('/api/tags/mine', { signal });
    expect(tags).toEqual([{ name: 'architecture' }, { name: 'backend' }, { name: 'mobile' }]);
    expect(Object.isFrozen(tags)).toBe(true);
    expect(tags.every(Object.isFrozen)).toBe(true);
  });

  it('deduplicates one logical tag with project representation precedence', () => {
    expect(combineVoiceCreateTags(
      [{ name: 'make-space' }, { name: 'mobile' }],
      [{ name: 'make space' }, { name: 'mobile' }],
    )).toEqual([{ name: 'make-space' }, { name: 'mobile' }]);
  });

  it('retains genuinely distinct labels that collide only under spoken acronym matching', () => {
    expect(combineVoiceCreateTags([{ name: 'RD' }], [{ name: 'R&D' }]))
      .toEqual([{ name: 'R&D' }, { name: 'RD' }]);
  });

  it('fails closed on a malformed authority response', async () => {
    const fetcher = vi.fn(async (path: string) => path === '/api/tags/mine' ? [{ label: 'private' }] : []);
    await expect(readVoiceCreateTags('alpha', new AbortController().signal, fetcher)).rejects.toThrow('network');
  });
});
