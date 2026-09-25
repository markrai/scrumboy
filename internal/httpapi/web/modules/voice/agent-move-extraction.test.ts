import { describe, expect, it } from 'vitest';
import type { Board } from '../types.js';
import { interpretApplicationEnvelope } from './agent-interpretation.js';
import {
  extractDeterministicMoveCall,
  extractMissingMoveSlotClarification,
  extractMoveSlotCandidates,
  extractMoveSlots,
  hasCompoundRequestCue,
  isCompleteStandaloneMove,
  recognizeMoveCommand,
} from './agent-move-extraction.js';

function boardWith(titles: readonly string[] = [], laneNames: readonly string[] = ['Backlog', 'Done']): Board {
  const columnOrder = laneNames.map((name, index) => ({
    key: name.toLowerCase().replace(/\s+/g, '_'), name, isDone: index === laneNames.length - 1,
  }));
  const columns = Object.fromEntries(columnOrder.map(lane => [lane.key, []]));
  const firstLane = columnOrder[0];
  columns[firstLane.key] = titles.map((title, index) => ({
    id: index + 1, localId: index + 1, title, status: firstLane.key, columnKey: firstLane.key,
  }));
  return {
    project: { id: 1, slug: 'alpha', name: 'Alpha', creatorUserId: 7, dominantColor: '#123456' },
    tags: [], columnOrder, columns,
  };
}

describe('application-owned VoiceFlow move recognition', () => {
  it.each([
    ['Move #239 to done.', '#239', 'done'],
    ['Move story #239 to done.', '#239', 'done'],
    ['Move number 239 to Done.', '#239', 'Done'],
    ['Move 239 to done', '#239', 'done'],
  ])('extracts a complete numeric move from %s', (utterance, reference, lane) => {
    expect(extractDeterministicMoveCall(utterance)).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference, lane },
    });
    expect(extractMoveSlots(utterance)).toEqual({ reference, numeric: true, lane });
  });

  it.each([
    ['Move Invalid URL story to done.', 'Invalid URL story', 'done'],
    ['Move Invalid URLs should redirect to main login to done.', 'Invalid URLs should redirect to main login', 'done'],
    ['Move the Invalid URL card into Done.', 'the Invalid URL card', 'Done'],
    ['Move Goblin to Done.', 'Goblin', 'Done'],
    ['Move TotallyInventedStory to TotallyInventedLane.', 'TotallyInventedStory', 'TotallyInventedLane'],
  ])('recognizes title and lane syntax without resolving board truth: %s', (utterance, reference, lane) => {
    expect(extractDeterministicMoveCall(utterance, boardWith([reference]))).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference, lane },
    });
  });

  it('returns every destination partition before authoritative lane selection', () => {
    expect(extractMoveSlotCandidates('Move #239 to Ready to Deploy')).toEqual([
      { reference: '#239', numeric: true, lane: 'Ready to Deploy' },
      { reference: '#239 to Ready', numeric: false, lane: 'Deploy' },
    ]);
    expect(extractMoveSlots('Move #239 to Ready to Deploy', boardWith([], ['Backlog', 'Ready to Deploy']))).toEqual({
      reference: '#239', numeric: true, lane: 'Ready to Deploy',
    });
  });

  it('uses the unique authoritative lane to split a named target containing destination words', () => {
    const board = boardWith(['Invalid URLs should redirect to main login', 'Widget'], ['Backlog', 'Done', 'Ready to Deploy']);
    expect(extractMoveSlots('Move Invalid URLs should redirect to main login to Done', board)).toEqual({
      reference: 'Invalid URLs should redirect to main login', numeric: false, lane: 'Done',
    });
    expect(extractMoveSlots('Move Widget to Ready to Deploy', board)).toEqual({
      reference: 'Widget', numeric: false, lane: 'Ready to Deploy',
    });
  });

  it('abstains when multiple destination partitions resolve to real lanes', () => {
    const board = boardWith(['Widget'], ['Backlog', 'Ready to Deploy', 'Deploy']);
    expect(recognizeMoveCommand('Move Widget to Ready to Deploy', board)).toBeNull();
  });

  it('recognizes missing slots without asking the model', () => {
    expect(extractDeterministicMoveCall('Move #239.')).toBeNull();
    expect(extractMissingMoveSlotClarification('Move #239.')).toEqual({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { reference: '#239' }, missing: 'lane', text: 'Which lane?',
    });
    expect(extractMissingMoveSlotClarification('Move to done.')).toEqual({
      kind: 'clarify_skill', skill: 'todos.move', arguments: { lane: 'done' }, missing: 'reference', text: 'Which story?',
    });
  });

  it.each([
    'Move Research and Development to Done.',
    'Move Salt & Pepper to Done.',
    'Move Jack and Jill to Done.',
  ])('does not mistake a title conjunction for compound work: %s', utterance => {
    expect(hasCompoundRequestCue(utterance)).toBe(false);
    expect(extractDeterministicMoveCall(utterance)?.skill).toBe('todos.move');
  });

  it.each([
    'Find and Replace',
    'Search and Replace',
    'Tag and Assign Permissions',
    'Archive and Delete Old Data',
  ])('treats an operation-like phrase as literal when it is an exact title: %s', title => {
    const board = boardWith([title]);
    const utterance = `Move ${title} to Done`;
    expect(hasCompoundRequestCue(utterance, board)).toBe(false);
    expect(extractDeterministicMoveCall(utterance, board)).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: title, lane: 'Done' },
    });
  });

  it('treats an operation-like phrase as literal when it is an authoritative lane', () => {
    const board = boardWith([], ['Backlog', 'Review and Archive']);
    const utterance = 'Move #239 to Review and Archive';
    expect(hasCompoundRequestCue(utterance, board)).toBe(false);
    expect(extractDeterministicMoveCall(utterance, board)).toEqual({
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'Review and Archive' },
    });
  });

  it.each([
    'Move #239 to done and tag it urgent',
    'Move #239 to done, then add the urgent tag',
    'Move #239 to done; open it',
    'Move to done and archive it',
    'Move #239 and after that tag it urgent',
  ])('abstains when a second operation is present: %s', utterance => {
    expect(hasCompoundRequestCue(utterance)).toBe(true);
    expect(recognizeMoveCommand(utterance)).toBeNull();
  });

  it('abstains for uncertain or non-move syntax', () => {
    expect(recognizeMoveCommand('Move story.')).toBeNull();
    expect(recognizeMoveCommand('Could this be done?')).toBeNull();
  });

  it('keeps terminality separate from interpretation provenance', () => {
    expect(interpretApplicationEnvelope('Move #239 to done.', { kind: 'ask_user', text: 'Which lane?' }, 'idle')).toEqual({
      source: 'deterministic_move',
      completion: 'finish_after_effect',
      envelope: { kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'done' } },
    });
    expect(interpretApplicationEnvelope('Move #239 to done and tag it urgent', {
      kind: 'skill_call', skill: 'todos.move', arguments: { reference: '#239', lane: 'Done' },
    }, 'idle')).toMatchObject({ source: 'model', completion: 'continue' });
  });

  it('uses the same production interpretation to recover malformed-model cases in evaluation', () => {
    expect(interpretApplicationEnvelope('Move #239.', null, 'idle')?.envelope).toMatchObject({
      kind: 'clarify_skill', skill: 'todos.move', missing: 'lane',
    });
    expect(interpretApplicationEnvelope('Move #239 to done.', null, 'clarification')).toBeNull();
  });

  it('distinguishes standalone completion from compound continuation', () => {
    expect(isCompleteStandaloneMove('Move #239 to done and tag it urgent')).toBe(false);
    expect(isCompleteStandaloneMove('Move #239 to done.')).toBe(true);
  });
});
