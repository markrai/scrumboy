// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  VOICE_SPEECH_RATE_STORAGE_KEY,
  setVoiceSpeechRate,
  type VoiceSpeechRate,
} from '../core/voice-speech-rate-preferences.js';
import { prepareTextForSpeechSynthesis, speak } from './speech-output.js';

type TestUtterance = {
  text: string;
  rate: number;
  onend: (() => void) | null;
  onerror: (() => void) | null;
};

function installSpeechSynthesis() {
  const utterances: TestUtterance[] = [];
  class SpeechSynthesisUtteranceFake implements TestUtterance {
    rate = 1;
    onend: (() => void) | null = null;
    onerror: (() => void) | null = null;
    constructor(readonly text: string) {
      utterances.push(this);
    }
  }
  const synthesis = {
    cancel: vi.fn(),
    speak: vi.fn((utterance: TestUtterance) => queueMicrotask(() => utterance.onend?.())),
  };
  vi.stubGlobal('SpeechSynthesisUtterance', SpeechSynthesisUtteranceFake);
  Object.defineProperty(window, 'speechSynthesis', { configurable: true, value: synthesis });
  return { utterances, synthesis };
}

beforeEach(() => localStorage.clear());

afterEach(() => {
  localStorage.removeItem(VOICE_SPEECH_RATE_STORAGE_KEY);
  vi.unstubAllGlobals();
  Reflect.deleteProperty(window, 'speechSynthesis');
});

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

  it.each([
    [1, 1],
    [1.25, 1.25],
    [1.5, 1.5],
    [1.75, 1.75],
    [2, 2],
  ] as const)('passes product speech rate %s to Web Speech as %s', async (rate, expected) => {
    const { utterances } = installSpeechSynthesis();
    setVoiceSpeechRate(rate as VoiceSpeechRate);

    await speak('Done.');

    expect(utterances).toHaveLength(1);
    expect(utterances[0].rate).toBe(expected);
  });

  it('uses rate 1 by default and retains todo pronunciation preparation', async () => {
    const { utterances } = installSpeechSynthesis();

    await speak('Delete todo #13');

    expect(utterances[0]).toMatchObject({ text: 'Delete to do #13', rate: 1 });
  });

  it('samples the preference for each new utterance without changing the prior one', async () => {
    const { utterances } = installSpeechSynthesis();
    setVoiceSpeechRate(1.25);
    await speak('First.');
    setVoiceSpeechRate(2);
    await speak('Second.');

    expect(utterances.map(utterance => utterance.rate)).toEqual([1.25, 2]);
    expect(utterances[0].rate).toBe(1.25);
  });
});
