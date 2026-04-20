import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function readSource(path: string): string {
  return readFileSync(new URL(path, import.meta.url), 'utf8');
}

describe('voice command dormant loading', () => {
  it('keeps voice modules out of startup and service worker paths', () => {
    const app = readSource('../../app.js');
    const router = readSource('../router.ts');
    const bootstrap = readSource('../views/board-load-bootstrap.ts');
    const sw = readSource('../../sw.js');

    for (const source of [app, router, bootstrap, sw]) {
      expect(source).not.toMatch(/voice\/|dist\/voice/);
    }
  });

  it('keeps board activation as a single dynamic import', () => {
    const board = readSource('../views/board.ts');

    expect(board).not.toMatch(/import\s+(?!\()[^;]*from\s+['"][^'"]*voice\//s);
    expect(board.match(/import\(["']\.\.\/voice\/flow\.js["']\)/g) ?? []).toHaveLength(1);
  });
});
