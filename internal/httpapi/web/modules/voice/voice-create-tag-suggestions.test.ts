// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import {
  findVoiceCreateTagSuggestions,
  isVoiceCreateTagDistanceOne,
  VOICE_CREATE_TAG_TYPO_MIN_LENGTH,
} from './voice-create-tag-suggestions.js';

const authority = (...names: string[]) => names.map(name => ({ name }));
const suggest = (reference: string, ...names: string[]) => findVoiceCreateTagSuggestions(reference, authority(...names));

describe('Voice Create authoritative tag suggestions', () => {
  it('suggests only the one-terminal-s counterpart in either direction', () => {
    expect(suggest('Bugs', 'bug')).toEqual({ matches: ['bug'], kind: 'terminal_s' });
    expect(suggest('Bug', 'bugs')).toEqual({ matches: ['bugs'], kind: 'terminal_s' });
    expect(suggest('Tests', 'test')).toEqual({ matches: ['test'], kind: 'terminal_s' });
  });

  it('supports exactly one insertion, deletion, substitution, or adjacent transposition', () => {
    expect(VOICE_CREATE_TAG_TYPO_MIN_LENGTH).toBe(4);
    expect(suggest('archtecture', 'architecture')).toEqual({ matches: ['architecture'], kind: 'edit_distance_1' });
    expect(suggest('mobille', 'mobile')).toEqual({ matches: ['mobile'], kind: 'edit_distance_1' });
    expect(suggest('mobilf', 'mobile')).toEqual({ matches: ['mobile'], kind: 'edit_distance_1' });
    expect(suggest('moblie', 'mobile')).toEqual({ matches: ['mobile'], kind: 'edit_distance_1' });
    expect(isVoiceCreateTagDistanceOne('mobile', 'totally-different')).toBe(false);
  });

  it('does not lower the threshold for a complete non-match', () => {
    expect(suggest('banana', 'bug', 'mobile', 'architecture')).toEqual({ matches: [], kind: 'none' });
  });

  it('protects short tags from generic typo suggestions', () => {
    expect(suggest('uix', 'ux', 'ui', 'ai')).toEqual({ matches: [], kind: 'none' });
    expect(suggest('bog', 'bug', 'bag')).toEqual({ matches: [], kind: 'none' });
  });

  it('returns every equal candidate without arbitrary ordering selection', () => {
    expect(suggest('bock', 'book', 'back')).toEqual({ matches: ['book', 'back'], kind: 'edit_distance_1' });
  });

  it('preserves authoritative spelling and deduplicates identical rows', () => {
    expect(suggest('Archtecture', 'Architecture', 'Architecture'))
      .toEqual({ matches: ['Architecture'], kind: 'edit_distance_1' });
  });
});
