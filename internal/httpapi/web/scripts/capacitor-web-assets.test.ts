import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { relative, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  collectLocalAssetReferences,
  explicitAssetAllowlist,
  localCssUrlPattern,
  localQuotedAssetPattern,
  transformIndex,
  webRoot,
} from './capacitor-web-artifact-lib.mjs';

function listProductionModuleSources(directory: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const fullPath = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...listProductionModuleSources(fullPath));
      continue;
    }
    if (!entry.isFile() || !entry.name.endsWith('.ts') || entry.name.endsWith('.test.ts')) {
      continue;
    }
    files.push(fullPath);
  }
  return files.sort();
}

function sourceLabel(filePath: string): string {
  return relative(webRoot, filePath).replaceAll('\\', '/');
}

function collectProductLocalAssets(): Array<{ file: string; raw: string; relativePath: string }> {
  const assets: Array<{ file: string; raw: string; relativePath: string }> = [];
  const quotedPattern = localQuotedAssetPattern();
  for (const filePath of listProductionModuleSources(resolve(webRoot, 'modules'))) {
    const source = readFileSync(filePath, 'utf8');
    for (const { raw, relativePath } of collectLocalAssetReferences(source, quotedPattern)) {
      assets.push({ file: sourceLabel(filePath), raw, relativePath });
    }
  }

  // Capacitor packages the transformed index, which strips the PWA manifest.
  const packagedIndex = transformIndex(readFileSync(resolve(webRoot, 'index.html'), 'utf8'), 'allowlist-test');
  for (const { raw, relativePath } of collectLocalAssetReferences(packagedIndex, localQuotedAssetPattern())) {
    assets.push({ file: 'index.html', raw, relativePath });
  }

  const styles = readFileSync(resolve(webRoot, 'styles.css'), 'utf8');
  for (const { raw, relativePath } of collectLocalAssetReferences(styles, localCssUrlPattern())) {
    assets.push({ file: 'styles.css', raw, relativePath });
  }
  return assets;
}

describe('C2 Capacitor product asset allowlist', () => {
  it('packages every local static asset referenced by the product UI', () => {
    const assets = collectProductLocalAssets();
    const paths = new Set(assets.map((asset) => asset.relativePath));
    expect(paths.has('archive.svg')).toBe(true);
    expect(paths.has('fonts/kalam.ttf')).toBe(true);
    expect(paths.has('favicon.ico')).toBe(true);

    const allowlist = new Set(explicitAssetAllowlist());
    const missing = assets.flatMap(({ file, raw, relativePath }) => {
      const onAllowlist = allowlist.has(relativePath);
      const onDisk = existsSync(resolve(webRoot, relativePath)) && statSync(resolve(webRoot, relativePath)).isFile();
      if (onAllowlist && onDisk) return [];
      const reasons = [
        onAllowlist ? null : 'not on Capacitor allowlist',
        onDisk ? null : 'missing on disk',
      ].filter(Boolean);
      return [`${file} references ${raw} (${reasons.join('; ')})`];
    });
    expect(missing).toEqual([]);
  });
});
