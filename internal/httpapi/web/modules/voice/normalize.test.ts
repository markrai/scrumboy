import { describe, expect, it } from 'vitest';
import { normalizeLookup, normalizeTitleReference, parseSpokenNumber } from './normalize.js';
import { normalizeConfirmationResponse, normalizeDisambiguationChoice, normalizeEntityAlias } from './vocabulary.js';

describe('voice command normalization', () => {
  it('normalizes supported story ID forms', () => {
    expect(parseSpokenNumber('56')).toEqual({ value: 56, ambiguous: false });
    expect(parseSpokenNumber('#56')).toEqual({ value: 56, ambiguous: false });
    expect(parseSpokenNumber('# one')).toEqual({ value: 1, ambiguous: false });
    expect(parseSpokenNumber('number one')).toEqual({ value: 1, ambiguous: false });
    expect(parseSpokenNumber('number 1')).toEqual({ value: 1, ambiguous: false });
    expect(parseSpokenNumber('id one')).toEqual({ value: 1, ambiguous: false });
    expect(parseSpokenNumber('id 1')).toEqual({ value: 1, ambiguous: false });
    expect(parseSpokenNumber('twelve')).toEqual({ value: 12, ambiguous: false });
    expect(parseSpokenNumber('one two')).toEqual({ value: 12, ambiguous: true });
    expect(parseSpokenNumber('number one two')).toEqual({ value: 12, ambiguous: true });
    expect(parseSpokenNumber('twenty one')).toEqual({ value: 21, ambiguous: false });
    expect(parseSpokenNumber('fifty six')).toEqual({ value: 56, ambiguous: false });
    expect(parseSpokenNumber('one hundred two')).toEqual({ value: 102, ambiguous: false });
    expect(parseSpokenNumber('number')).toBeNull();
    expect(parseSpokenNumber('id')).toBeNull();
  });

  it('marks digit-word sequences as ambiguous IDs', () => {
    expect(parseSpokenNumber('five six')).toEqual({ value: 56, ambiguous: true });
  });

  it('normalizes lookup phrases without preserving punctuation variants', () => {
    expect(normalizeLookup('In-Progress!')).toBe('in progress');
    expect(normalizeLookup('"Ada Lovelace"')).toBe('ada lovelace');
  });

  it('normalizes title suffix number markers deterministically', () => {
    expect(normalizeTitleReference('notification test number 3')).toBe('notification test 3');
    expect(normalizeTitleReference('notification test number three')).toBe('notification test 3');
    expect(normalizeTitleReference('notification test #3')).toBe('notification test 3');
    expect(normalizeTitleReference('notification test no 3')).toBe('notification test 3');
    expect(normalizeTitleReference('notification test num 3')).toBe('notification test 3');
  });

  it('normalizes command vocabulary aliases', () => {
    expect(normalizeEntityAlias('story')).toBe('todo');
    expect(normalizeEntityAlias('stories')).toBe('todo');
    expect(normalizeEntityAlias('todos')).toBe('todo');
    expect(normalizeEntityAlias('to do')).toBe('todo');
    expect(normalizeEntityAlias('to-do')).toBe('todo');
    expect(normalizeEntityAlias('to dos')).toBe('todo');
    expect(normalizeConfirmationResponse('yeah')).toBe('yes');
    expect(normalizeConfirmationResponse('nope')).toBe('no');
    expect(normalizeConfirmationResponse('stop')).toBe('cancel');
    expect(normalizeConfirmationResponse('maybe')).toBeNull();
  });

  it('normalizes constrained disambiguation choices only', () => {
    expect(normalizeDisambiguationChoice('first one')).toBe('option_1');
    expect(normalizeDisambiguationChoice('number two')).toBe('option_2');
    expect(normalizeDisambiguationChoice('3')).toBe('option_3');
    expect(normalizeDisambiguationChoice('the login one')).toBeNull();
  });
});
