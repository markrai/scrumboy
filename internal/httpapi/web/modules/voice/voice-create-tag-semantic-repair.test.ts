// @vitest-environment happy-dom
import { describe, expect, it, vi } from 'vitest';
import {
  createVoiceCreateTagRepairRequest,
  createVoiceCreateTagSemanticRepair,
  parseVoiceCreateTagRepairResult,
  VOICE_CREATE_TAG_REPAIR_PROMPT,
  VOICE_CREATE_TAG_REPAIR_VERSION,
} from './voice-create-tag-semantic-repair.js';

const transcript = 'create a story called Quasar assigned to mark and tag It architecture.';
const request = () => createVoiceCreateTagRepairRequest(
  transcript,
  ['tag it architecture'],
  [0],
  [{ name: 'architecture' }, { name: 'IT' }, { name: 'infrastructure' }, { name: 'backend' }],
)!;

describe('Voice Create closed-world tag semantic repair', () => {
  it('retrieves only a bounded lexical candidate set without resolving it', () => {
    const value = request();
    expect(value.references).toEqual([{
      referenceIndex: 0,
      reference: 'tag it architecture',
      candidates: [
        { id: 'candidate-1', name: 'architecture' },
        { id: 'candidate-2', name: 'IT' },
      ],
    }]);
  });

  it('returns no request when any failed reference lacks lexical evidence or candidates are unsafe', () => {
    expect(createVoiceCreateTagRepairRequest(transcript, ['It nonexisting'], [0], [{ name: 'architecture' }])).toBeNull();
    expect(createVoiceCreateTagRepairRequest(transcript, ['tag architecture'], [0], [{ name: 'architecture' }, { name: 'Architecture' }])).toBeNull();
  });

  it('selects only an identifier supplied for the same failed reference', async () => {
    const generate = vi.fn(async (input: { requestId: string }) => ({
      requestId: input.requestId,
      text: JSON.stringify({ version: 1, selections: [{ referenceIndex: 0, tagId: 'candidate-1' }] }),
    }));
    const repair = createVoiceCreateTagSemanticRepair({ generate });
    await expect(repair(request(), new AbortController().signal)).resolves.toEqual({
      result: 'selected',
      candidateCount: 2,
      bindings: [{ referenceIndex: 0, tag: 'architecture' }],
    });
    expect(generate).toHaveBeenCalledOnce();
    expect(generate.mock.calls[0][0]).toMatchObject({
      requestId: expect.stringContaining(VOICE_CREATE_TAG_REPAIR_VERSION),
      instructions: VOICE_CREATE_TAG_REPAIR_PROMPT,
      maximumOutputTokens: 128,
    });
    expect(JSON.parse(generate.mock.calls[0][0].input)).toMatchObject({
      originalUtterance: transcript,
      failedReferences: [{ reference: 'tag it architecture' }],
    });
  });

  it.each([
    ['unknown candidate', JSON.stringify({ version: 1, selections: [{ referenceIndex: 0, tagId: 'security' }] })],
    ['unknown reference', JSON.stringify({ version: 1, selections: [{ referenceIndex: 9, tagId: 'candidate-1' }] })],
    ['multiple tags for one reference', JSON.stringify({ version: 1, selections: [
      { referenceIndex: 0, tagId: 'candidate-1' },
      { referenceIndex: 0, tagId: 'candidate-2' },
    ] })],
    ['unknown field', JSON.stringify({ version: 1, selections: [{ referenceIndex: 0, tagId: 'candidate-1', name: 'architecture' }] })],
    ['malformed JSON', '{"version":1,'],
  ])('rejects %s without producing a binding', (_name, raw) => {
    expect(parseVoiceCreateTagRepairResult(raw, request())).toMatchObject({ result: expect.stringMatching(/invalid|ambiguous/), bindings: [] });
  });

  it('treats an empty or incomplete selection as no safe repair', () => {
    expect(parseVoiceCreateTagRepairResult('{"version":1,"selections":[]}', request()))
      .toEqual({ result: 'none', candidateCount: 2, bindings: [] });
  });
});
