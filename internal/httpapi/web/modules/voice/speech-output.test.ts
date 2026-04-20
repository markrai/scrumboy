import { describe, expect, it } from 'vitest';
import { prepareTextForSpeechSynthesis } from './speech-output.js';

describe('voice speech output', () => {
  it('pronounces command entity todo as to do', () => {
    expect(prepareTextForSpeechSynthesis('Delete todo #13. Confirm?')).toBe('Delete to do #13. Confirm?');
    expect(prepareTextForSpeechSynthesis('Create todo "Fix login". Confirm?')).toBe('Create to do "Fix login". Confirm?');
  });

  it('preserves non-entity text', () => {
    expect(prepareTextForSpeechSynthesis('Delete todo #13: Fix todo pronunciation. Confirm?')).toBe(
      'Delete to do #13: Fix todo pronunciation. Confirm?',
    );
    expect(prepareTextForSpeechSynthesis('Fix todo pronunciation')).toBe('Fix todo pronunciation');
  });
});
