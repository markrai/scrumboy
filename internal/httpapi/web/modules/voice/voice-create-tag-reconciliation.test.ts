// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import {
  reconcileVoiceCreateTagReferences,
  VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN,
} from './voice-create-tag-reconciliation.js';
import { VoiceCreatePlanError } from './voice-create-plan.js';

const authority = (...names: string[]) => names.map(name => ({ name }));
const reconcile = (references: string[], names: string[]) => reconcileVoiceCreateTagReferences(references, authority(...names));

function expectTagFailure(references: string[], names: string[], result: 'unavailable' | 'ambiguous', candidateCount: number) {
  try {
    reconcile(references, names);
    throw new Error('expected tag failure');
  } catch (error) {
    expect(error).toBeInstanceOf(VoiceCreatePlanError);
    expect(error).toMatchObject({ code: 'tag', details: { entityType: 'tag', result, candidateCount } });
  }
}

describe('Voice Create planner tag-sequence reconciliation', () => {
  it('coalesces split two- and three-letter planner output through existing spoken identity', () => {
    expect(VOICE_CREATE_TAG_RECONCILIATION_MAX_RUN).toBe(5);
    expect(reconcile(['U', 'X'], ['ux'])).toEqual({ tags: ['ux'], referenceNormalizationApplied: true });
    expect(reconcile(['A', 'P', 'I'], ['api'])).toEqual({ tags: ['api'], referenceNormalizationApplied: true });
  });

  it('protects a complete independent interpretation even when the joined tag exists', () => {
    expect(reconcile(['U', 'X'], ['u', 'x', 'ux']))
      .toEqual({ tags: ['u', 'x'], referenceNormalizationApplied: false });
  });

  it('fails closed when exactly one entry resolves instead of consuming it into a repair', () => {
    expectTagFailure(['U', 'X'], ['u', 'ux'], 'unavailable', 0);
  });

  it('fails closed when the joined spoken identity is ambiguous', () => {
    expectTagFailure(['U', 'X'], ['ux', 'U&X'], 'ambiguous', 2);
  });

  it('fails closed instead of partitioning one unresolved run into adjacent acronym repairs', () => {
    expectTagFailure(['U', 'X', 'A', 'P', 'I'], ['ux', 'api'], 'unavailable', 0);
  });

  it('coalesces only the local unresolved run beside legitimate tags', () => {
    expect(reconcile(['mobile', 'U', 'X'], ['mobile', 'ux']))
      .toEqual({ tags: ['mobile', 'ux'], referenceNormalizationApplied: true });
    expect(reconcile(['U', 'X', 'backend'], ['ux', 'backend']))
      .toEqual({ tags: ['ux', 'backend'], referenceNormalizationApplied: true });
  });

  it('preserves ordinary multiple tags independently', () => {
    expect(reconcile(['mobile', 'ux'], ['mobile', 'ux']))
      .toEqual({ tags: ['mobile', 'ux'], referenceNormalizationApplied: false });
    expect(reconcile(['mobile', 'backend'], ['mobile', 'backend']))
      .toEqual({ tags: ['mobile', 'backend'], referenceNormalizationApplied: false });
  });

  it.each([
    [['UX'], ['ux'], ['ux'], false],
    [['U.X.'], ['ux'], ['ux'], true],
    [['Architecture'], ['architecture'], ['architecture'], false],
    [['mobile'], ['mobile'], ['mobile'], false],
  ] as const)('keeps single-reference behavior for %j', (references, names, expected, normalized) => {
    expect(reconcile([...references], [...names]))
      .toEqual({ tags: [...expected], referenceNormalizationApplied: normalized });
  });

  it('keeps unknown tags fail-closed', () => {
    expectTagFailure(['TotallyInventedTag'], ['mobile', 'ux'], 'unavailable', 0);
  });

  it('does not create ambiguity from duplicate authoritative rows', () => {
    expect(reconcile(['U', 'X'], ['ux', 'ux']))
      .toEqual({ tags: ['ux'], referenceNormalizationApplied: true });
  });
});
