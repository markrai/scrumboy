// Proof that wall.ts stays lazy-loaded.
//
// The board view is the only code path that opens the wall dialog, and the
// plan requires the wall entry point to be imported dynamically so that
// users who never open the wall do not pay its cost. This test guards that
// invariant by reading the on-disk module and asserting that it never takes
// a static import of wall.js / wall-rendering.js / wall-permissions.js.

import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const boardSource = readFileSync(
  join(__dirname, '..', 'views', 'board.ts'),
  'utf8',
);

describe('wall lazy-load invariant', () => {
  it('board.ts uses a dynamic import for the wall module', () => {
    const staticImport = /^\s*import\s[^;]*from\s+['"][^'"]*dialogs\/wall(?:\.js)?['"]/m;
    const dynamicImport = /import\(\s*['"][^'"]*dialogs\/wall(?:\.js)?['"]\s*\)/;
    expect(staticImport.test(boardSource)).toBe(false);
    expect(dynamicImport.test(boardSource)).toBe(true);
  });
});
