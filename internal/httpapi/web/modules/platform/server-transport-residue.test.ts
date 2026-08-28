import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, relative } from 'node:path';

const modulesRoot = fileURLToPath(new URL('../', import.meta.url));

function productionTypeScriptFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return productionTypeScriptFiles(path);
    if (!entry.name.endsWith('.ts') || entry.name.endsWith('.test.ts')) return [];
    return [path];
  });
}

function locationsFor(pattern: RegExp): string[] {
  const locations: string[] = [];
  for (const file of productionTypeScriptFiles(modulesRoot)) {
    const source = readFileSync(file, 'utf8');
    for (const match of source.matchAll(pattern)) {
      const line = source.slice(0, match.index).split('\n').length;
      locations.push(`${relative(modulesRoot, file).replaceAll('\\', '/')}:${line}`);
    }
  }
  return locations.sort();
}

describe('server transport residue', () => {
  it('allows direct fetch only for the browser transport and packaged assets', () => {
    expect(locationsFor(/\bfetch\s*\(/g)).toEqual([
      'i18n/index.ts:678',
      'mermaid-semantic-edges.ts:110',
      'platform/browser-server-transport.ts:13',
      'views/board-prefetch-cache.ts:22',
    ]);
  });

  it('constructs EventSource only in the browser transport', () => {
    expect(locationsFor(/new\s+EventSource\s*\(/g)).toEqual([
      'platform/browser-server-transport.ts:18',
    ]);
  });

  it('uses only asynchronous acquired resources for authenticated display URLs', () => {
    expect(locationsFor(/\bresourceUrl\b/g)).toEqual([]);
    expect(locationsFor(/\bacquireResource\s*\(/g).map((location) => location.split(':')[0])).toEqual([
      'platform/browser-server-transport.ts',
      'platform/runtime.ts',
      'platform/server-transport.ts',
      'wallpaper.ts',
    ]);
  });

  it('uses location.origin for server/public semantics only inside the browser adapter', () => {
    expect(locationsFor(/(?:window\.)?location\.origin/g)).toEqual([
      'platform/browser-server-transport.ts:17',
    ]);
  });
});
