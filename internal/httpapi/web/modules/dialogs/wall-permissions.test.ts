import { describe, expect, it } from 'vitest';
import { canEditWall } from './wall-permissions.js';

describe('canEditWall', () => {
  it('allows maintainers and contributors', () => {
    expect(canEditWall('maintainer')).toBe(true);
    expect(canEditWall('contributor')).toBe(true);
  });

  it('rejects viewers and unknown roles', () => {
    expect(canEditWall('viewer')).toBe(false);
    expect(canEditWall('')).toBe(false);
    expect(canEditWall(null)).toBe(false);
    expect(canEditWall(undefined)).toBe(false);
    expect(canEditWall('owner')).toBe(false); // owner is not a wall role in this project model
  });
});
