// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import type { Board, Todo } from '../types.js';
import {
  extractHighConfidenceMoveCall,
  extractMissingMoveSlotClarification,
  extractMoveSlots,
  hasCompoundRequestCue,
  isCompleteStandaloneMove,
  recoverMoveAfterParseFailure,
  recoverPresentMoveArguments,
  uniqueDirectMoveTitle,
} from './agent-move-extraction.js';

function todo(overrides: Partial<Todo> & Pick<Todo, 'localId' | 'title'>): Todo {
  return { id: overrides.localId, status: 'backlog', columnKey: 'backlog', ...overrides };
}

function board(stories: Todo[] = [
  todo({ localId: 239, title: 'Invalid URLs should redirect to main login' }),
]): Board {
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', creatorUserId: 7, dominantColor: '#123456' },
    tags: [],
    columnOrder: [
      { key: 'backlog', name: 'Backlog', isDone: false },
      { key: 'done', name: 'Done', isDone: true },
    ],
    columns: { backlog: stories, done: [] },
  };
}

describe('high-confidence VoiceFlow move extraction', () => {
  it.each([
    'Move #239 to done.',
    'Move story #239 to done.',
    'Move number 239 to Done.',
    'Move 239 to done',
  ])('extracts a complete numeric move from %s', utterance => {
    expect(extractHighConfidenceMoveCall(utterance, board())).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'Done' },
    });
    expect(extractMoveSlots(utterance, board())).toEqual({ reference: '#239', numeric: true, lane: 'Done' });
  });

  it.each([
    'Move Invalid URL story to done.',
    'Move Invalid URLs should redirect to main login to done.',
    'Move the Invalid URL card into Done.',
    'Move Goblins in Washington to Done.',
  ])('skips the model for a unique title move: %s', utterance => {
    const stories = [
      todo({ localId: 239, title: 'Invalid URLs should redirect to main login' }),
      todo({ localId: 369, title: 'Goblins in Washington' }),
    ];
    expect(extractHighConfidenceMoveCall(utterance, board(stories))?.kind).toBe('skill_call');
    expect(extractHighConfidenceMoveCall(utterance, board(stories))?.arguments.lane).toBe('Done');
  });

  it('does not skip the model for an ambiguous Goblin title', () => {
    const stories = [
      todo({ localId: 369, title: 'Goblins in Washington' }),
      todo({ localId: 370, title: 'Goblins in Burtonsville' }),
      todo({ localId: 371, title: 'Goblins on the way' }),
    ];
    expect(uniqueDirectMoveTitle('Goblin', board(stories))).toBe(false);
    expect(extractHighConfidenceMoveCall('Move Goblin to Done.', board(stories))).toBeNull();
    expect(extractMoveSlots('Move Goblin to Done.', board(stories))).toEqual({
      reference: 'Goblin', numeric: false, lane: 'Done',
    });
  });

  it('keeps exact-title-first when a literal wrapper title exists', () => {
    const stories = [
      todo({ localId: 410, title: 'Invalid URL story' }),
      todo({ localId: 239, title: 'Invalid URLs should redirect to main login' }),
    ];
    expect(extractHighConfidenceMoveCall('Move Invalid URL story to done.', board(stories))).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: 'Invalid URL story', lane: 'Done' },
    });
  });

  it('does not treat a missing lane as a complete move', () => {
    expect(extractHighConfidenceMoveCall('Move #239.', board())).toBeNull();
    expect(extractMoveSlots('Move #239.', board())).toEqual({ reference: '#239', numeric: true, lane: null });
    expect(extractMissingMoveSlotClarification('Move #239.', board())).toEqual({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?',
    });
  });

  it('does not treat a missing numeric target as a complete move', () => {
    expect(extractHighConfidenceMoveCall('Move to done.', board())).toBeNull();
    expect(extractMoveSlots('Move to done.', board())).toEqual({ reference: null, numeric: false, lane: 'Done' });
    expect(extractMissingMoveSlotClarification('Move to done.', board())).toEqual({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?',
    });
  });

  it('does not skip the model for compound work', () => {
    expect(hasCompoundRequestCue('Move #239 to done and tag it urgent')).toBe(true);
    expect(extractHighConfidenceMoveCall('Move #239 to done and tag it urgent', board())).toBeNull();
    expect(extractMissingMoveSlotClarification('Move to done and archive it', board())).toBeNull();
  });

  it('rejects a clarify_skill that claims a plainly present lane is missing', () => {
    const recovered = recoverPresentMoveArguments(
      'Move #239 to done.',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
      board(),
      'idle',
    );
    expect(recovered).toEqual({
      recoveredFrom: 'present_move_slots',
      envelope: { kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'Done' } },
    });
  });

  it('converts Which lane? ask_user into a complete move when both slots are present', () => {
    const recovered = recoverPresentMoveArguments(
      'Move #239 to done.',
      { kind: 'ask_user', text: 'Which lane?' },
      board(),
      'idle',
    );
    expect(recovered?.envelope).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'Done' },
    });
  });

  it('keeps a genuine missing-lane clarification', () => {
    expect(recoverPresentMoveArguments(
      'Move #239.',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
      board(),
      'idle',
    )).toBeNull();
  });

  it('keeps a genuine missing-reference clarification', () => {
    expect(recoverPresentMoveArguments(
      'Move to done.',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
      board(),
      'idle',
    )).toBeNull();
  });

  it('turns a Which lane? ask_user into structured clarification when only the target is present', () => {
    const recovered = recoverPresentMoveArguments(
      'Move #239.',
      { kind: 'ask_user', text: 'Which lane?' },
      board(),
      'idle',
    );
    expect(recovered).toEqual({
      recoveredFrom: 'ask_user_to_clarify_skill',
      envelope: { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
    });
  });

  it('turns a Which story? ask_user into structured clarification when only the lane is present', () => {
    const recovered = recoverPresentMoveArguments(
      'Move to done.',
      { kind: 'ask_user', text: 'Which story?' },
      board(),
      'idle',
    );
    expect(recovered).toEqual({
      recoveredFrom: 'ask_user_to_clarify_skill',
      envelope: { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
    });
  });

  it('still retains a missing-target draft when ask_user uses equivalent wording', () => {
    const recovered = recoverPresentMoveArguments(
      'Move to done.',
      { kind: 'ask_user', text: 'What should I move?' },
      board(),
      'idle',
    );
    expect(recovered).toEqual({
      recoveredFrom: 'ask_user_to_clarify_skill',
      envelope: { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
    });
  });

  it('does not give a non-move ask_user missing-slot authority', () => {
    expect(recoverPresentMoveArguments(
      'Delete a story',
      { kind: 'ask_user', text: 'Which story?' },
      board(),
      'idle',
    )).toBeNull();
  });

  it('does not complete Move #239 from a model-invented Done lane', () => {
    expect(recoverPresentMoveArguments(
      'Move #239.',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'Done' }, missing: 'reference', text: 'Which story?' },
      board(),
      'idle',
    )).toEqual({
      recoveredFrom: 'present_move_slots',
      envelope: { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
    });
  });

  it('does not intercept a compound move as a complete skill_call', () => {
    expect(recoverPresentMoveArguments(
      'Move #239 to done and tag it urgent',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?' },
      board(),
      'idle',
    )).toBeNull();
    expect(extractHighConfidenceMoveCall('Move #239 to done and tag it urgent', board())).toBeNull();
  });

  it('recovers a title move that already names a lane', () => {
    const recovered = recoverPresentMoveArguments(
      'Move Invalid URL story to done.',
      { kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: 'Invalid URL story' }, missing: 'lane', text: 'Which lane?' },
      board(),
      'idle',
    );
    expect(recovered?.envelope).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: 'Invalid URL story', lane: 'Done' },
    });
  });

  it('recovers a unique title move after unparseable model JSON', () => {
    const recovered = recoverMoveAfterParseFailure('Move Invalid URL story to done.', board(), 'idle');
    expect(recovered).toEqual({
      recoveredFrom: 'model_parse_failure',
      envelope: { kind: 'skill_call', skill: 'todos.move', arguments: { reference: 'Invalid URL story', lane: 'Done' } },
    });
  });

  it('recovers a missing-slot draft after unparseable model JSON', () => {
    expect(recoverMoveAfterParseFailure('Move to done.', board(), 'idle')?.envelope).toMatchObject({
      kind: 'clarify_skill', skill: 'todos.move', missing: 'reference',
    });
    expect(recoverMoveAfterParseFailure('Move #239.', board(), 'idle')?.envelope).toMatchObject({
      kind: 'clarify_skill', skill: 'todos.move', missing: 'lane',
    });
  });

  it('does not finish compound prepared work locally', () => {
    expect(isCompleteStandaloneMove('Move #239 to done and tag it urgent', board())).toBe(false);
    expect(isCompleteStandaloneMove('Move #239 to done.', board())).toBe(true);
  });
});
