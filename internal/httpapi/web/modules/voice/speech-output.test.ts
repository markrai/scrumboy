import { describe, expect, it } from 'vitest';
import { prepareTextForSpeechSynthesis } from './speech-output.js';

describe('voice speech output', () => {
  it.each([
    ['Delete todo #369: Billy Mongoose?', 'Delete to do #369: Billy Mongoose?'],
    ['Open todo #13: Fix login', 'Open to do #13: Fix login'],
    ['Move todo #13: Fix login to Done', 'Move to do #13: Fix login to Done'],
    ['Assign todo #13: Fix login to Ada', 'Assign to do #13: Fix login to Ada'],
    ['Unassign todo #13', 'Unassign to do #13'],
  ])('pronounces the owned command noun in %s', (input, expected) => {
    expect(prepareTextForSpeechSynthesis(input)).toBe(expected);
  });

  it('preserves todo in a resolved title', () => {
    expect(prepareTextForSpeechSynthesis('Delete todo #13: Fix todo pronunciation. Confirm?')).toBe(
      'Delete to do #13: Fix todo pronunciation. Confirm?',
    );
  });

  it('preserves todo in a quoted create title', () => {
    expect(prepareTextForSpeechSynthesis('Create todo "todo list redesign". Confirm?')).toBe(
      'Create to do "todo list redesign". Confirm?',
    );
  });

  it.each([
    ['Add tag todo to todo #13', 'Add tag todo to to do #13'],
    ['Remove tag todo from todo #13', 'Remove tag todo from to do #13'],
  ])('rewrites only the owned noun in tag command %s', (input, expected) => {
    expect(prepareTextForSpeechSynthesis(input)).toBe(expected);
  });

  it('preserves arbitrary and plural text', () => {
    expect(prepareTextForSpeechSynthesis('Fix todo pronunciation')).toBe('Fix todo pronunciation');
    expect(prepareTextForSpeechSynthesis('Review todos before launch')).toBe('Review todos before launch');
  });

  it('preserves already-safe text', () => {
    expect(prepareTextForSpeechSynthesis('Ready for confirmation.')).toBe('Ready for confirmation.');
  });
});
