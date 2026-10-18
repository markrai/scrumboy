// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest';
import { combineVoiceCreateTags, readVoiceCreateTags } from './voice-create-tags.js';
import { setBoard, setSlug } from '../state/mutations.js';

afterEach(() => {
  setBoard(null);
  setSlug(null);
});

describe('Voice Create authoritative tag adapter', () => {
  it('reads only the active project tag projection from the loaded board', async () => {
    const signal = new AbortController().signal;
    setSlug('alpha team');
    setBoard({
      project: { id: 1, name: 'Alpha', slug: 'alpha team', dominantColor: '#000000' },
      tags: [
        { name: 'backend', count: 2 },
        { name: 'mobile', count: 1 },
        { name: 'historical', count: 0 },
      ],
      columns: {},
    });

    const tags = await readVoiceCreateTags('alpha team', signal);

    expect(tags).toEqual([{ name: 'backend' }, { name: 'mobile' }]);
    expect(Object.isFrozen(tags)).toBe(true);
    expect(tags.every(Object.isFrozen)).toBe(true);
  });

  it('deduplicates by the server-owned canonical write name', () => {
    expect(combineVoiceCreateTags(
      [
        { name: 'make-space', canonicalName: 'make-space' },
        { name: 'make space', canonicalName: 'make-space' },
        { name: 'mobile', canonicalName: 'mobile' },
      ],
    )).toEqual([{ name: 'make-space' }, { name: 'mobile' }]);
  });

  it('does not use JavaScript case folding to invent Unicode tag identity', () => {
    expect(combineVoiceCreateTags([
      { name: 'i', canonicalName: 'i' },
      { name: 'i\u0307', canonicalName: 'i\u0307' },
      { name: 'İ', canonicalName: 'i' },
    ])).toEqual([{ name: 'i' }, { name: 'i\u0307' }]);
  });

  it('retains genuinely distinct labels that collide only under spoken acronym matching', () => {
    expect(combineVoiceCreateTags([{ name: 'RD' }, { name: 'R&D' }]))
      .toEqual([{ name: 'R&D' }, { name: 'RD' }]);
  });

  it('omits retained legacy rows that have no name-based write identity', () => {
    expect(combineVoiceCreateTags([
      { name: 'bad!', canonicalName: '' },
      { name: 'backend', canonicalName: 'backend' },
    ])).toEqual([{ name: 'backend' }]);
  });

  it('fails closed on a malformed authority response', async () => {
    setSlug('alpha');
    setBoard({
      project: { id: 1, name: 'Alpha', slug: 'alpha', dominantColor: '#000000' },
      tags: [{ name: '', count: 1 }],
      columns: {},
    });
    await expect(readVoiceCreateTags('alpha', new AbortController().signal)).rejects.toThrow('network');
  });
});
