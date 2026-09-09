import { describe, expect, it } from 'vitest';
import { normalizeLookup, normalizeTitleReference, normalizeVoiceReviewUtterance, parseSpokenNumber, spokenReferenceIdentity } from './normalize.js';
import { canonicalizeTagName } from './tag-canonicalization.js';
import { classifyVoiceReviewDecision, normalizeConfirmationResponse, normalizeDisambiguationChoice, normalizeEntityAlias } from './vocabulary.js';

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
    expect(normalizeLookup('ＦＵＬＬＷＩＤＴＨ')).toBe('fullwidth');
  });

  it('normalizes review decisions as complete utterances only', () => {
    expect(normalizeVoiceReviewUtterance('  Sure.  ')).toBe('sure');
    expect(normalizeVoiceReviewUtterance('Yes, please.')).toBe('yes please');
    expect(normalizeVoiceReviewUtterance('No, thanks.')).toBe('no thanks');
    expect(normalizeVoiceReviewUtterance('That’s fine!')).toBe("that's fine");
    expect(classifyVoiceReviewDecision('Go ahead.')).toBe('confirm');
    expect(classifyVoiceReviewDecision('Sure, thing.')).toBe('confirm');
    expect(classifyVoiceReviewDecision('Don’t do it.')).toBe('cancel');
    expect(classifyVoiceReviewDecision('Sure, change the lane first.')).toBe('unknown');
    expect(classifyVoiceReviewDecision('Yes, but assign Sarah.')).toBe('unknown');
    expect(classifyVoiceReviewDecision('Okay, and tag it UX.')).toBe('unknown');
  });

  it('derives a compact identity only for safely spelled alphanumeric references', () => {
    for (const value of ['UX', 'U.X.', 'U. X.', 'U X', 'u-x', 'u x']) {
      expect(spokenReferenceIdentity(value).spelled).toBe('ux');
    }
    expect(spokenReferenceIdentity('A P I').spelled).toBe('api');
    expect(spokenReferenceIdentity('R&D').spelled).toBe('rd');
    expect(spokenReferenceIdentity('foo-bar').spelled).toBeNull();
    expect(spokenReferenceIdentity('user experience').spelled).toBeNull();
    expect(spokenReferenceIdentity('C++').spelled).toBeNull();
  });

  it('mirrors server tag canonicalization without changing lookup identity', () => {
    expect(canonicalizeTagName('  Make   Space  ')).toBe('make-space');
    expect(canonicalizeTagName('--Architecture---Review--')).toBe('architecture-review');
    expect(canonicalizeTagName('UX')).toBe('ux');
    for (const value of ['', '   ', 'R&D', 'C++', 'bad!', 'x'.repeat(33)]) {
      expect(canonicalizeTagName(value)).toBeNull();
    }
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
