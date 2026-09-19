export { VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS } from './agent-evaluation.js';
/**
 * Device/application accuracy is valid only on a board that satisfies
 * `VOICE_AGENT_EVALUATION_BOARD_REQUIREMENTS`. Inapplicable cases are reported
 * separately and are not counted as semantic misses.
 */
export const VOICE_AGENT_EVALUATION_CASES = Object.freeze([
    {
        id: 'move-hash-to-done',
        transcript: 'Move #239 to done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceNumeric: 239, lane: 'done', allowClarification: false },
    },
    {
        id: 'move-story-hash-to-done',
        transcript: 'Move story #239 to done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceNumeric: 239, lane: 'done', allowClarification: false },
    },
    {
        id: 'move-number-to-done',
        transcript: 'Move number 239 to Done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceNumeric: 239, lane: 'done', allowClarification: false },
    },
    {
        id: 'move-hash-missing-lane',
        transcript: 'Move #239.',
        expected: { kind: 'clarify_skill', skill: 'todos.move', missing: 'lane', referenceNumeric: 239, allowClarification: true },
    },
    {
        id: 'move-to-done-missing-reference',
        transcript: 'Move to done.',
        expected: { kind: 'clarify_skill', skill: 'todos.move', missing: 'reference', lane: 'done', allowClarification: true },
    },
    {
        id: 'move-invalid-url-story',
        transcript: 'Move Invalid URL story to done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'Invalid URL', lane: 'done', allowClarification: false },
    },
    {
        id: 'move-invalid-urls-full-title',
        transcript: 'Move Invalid URLs should redirect to main login to done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'Invalid URL', lane: 'done', allowClarification: false },
    },
    {
        id: 'move-the-invalid-url-card',
        transcript: 'Move the Invalid URL card into Done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'Invalid URL', lane: 'done', allowClarification: false },
    },
    {
        id: 'move-goblins-in-washington',
        transcript: 'Move Goblins in Washington to Done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'Goblins in Washington', lane: 'done', allowClarification: false },
    },
    {
        id: 'move-goblin-ambiguous',
        transcript: 'Move Goblin to Done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'Goblin', lane: 'done', allowClarification: true },
    },
    {
        id: 'move-nonexistent-story',
        transcript: 'Move TotallyInventedStory to done.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceText: 'TotallyInventedStory', lane: 'done', allowClarification: false },
    },
    {
        id: 'move-nonexistent-lane',
        transcript: 'Move #239 to TotallyInventedLane.',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceNumeric: 239, lane: 'TotallyInventedLane', allowClarification: false },
    },
    {
        id: 'move-compound',
        transcript: 'Move #239 to done and tag it urgent',
        expected: { kind: 'skill_call', skill: 'todos.move', referenceNumeric: 239, lane: 'done', allowClarification: true },
    },
]);
