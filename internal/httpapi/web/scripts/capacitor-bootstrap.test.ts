// @vitest-environment happy-dom

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { explicitAssetAllowlist, transformIndex } from './capacitor-web-artifact-lib.mjs';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = resolve(webRoot, '..', '..', '..');
const shellRoot = resolve(repositoryRoot, 'mobile', 'capacitor', 'shell');
const bootstrapEntry = readFileSync(resolve(shellRoot, 'bootstrap.ts'), 'utf8');
const bootstrapCore = readFileSync(resolve(shellRoot, 'bootstrap-core.ts'), 'utf8');
const authoritativeIndex = readFileSync(resolve(webRoot, 'index.html'), 'utf8');

describe('C2 mobile bootstrap artifact boundary', () => {
  it('keeps product startup behind the packaged bootstrap', () => {
    const mobileIndex = transformIndex(authoritativeIndex, 'c2-test-version');
    const bootstrapPosition = mobileIndex.indexOf('src="/bootstrap.js"');

    expect(bootstrapPosition).toBeGreaterThan(-1);
    expect(mobileIndex).not.toMatch(/<script\b[^>]*\bsrc="\/app\.js/);
    expect(mobileIndex).not.toMatch(/\b(?:src|href|content)="https?:\/\//i);
    expect(bootstrapEntry).toContain("import { startMobileBootstrap } from './bootstrap-core.js'");
    expect(bootstrapEntry).toContain("import { installNativeLifecycle } from './native-lifecycle.js'");
    expect(bootstrapEntry).toContain("import { nativeOIDC } from './native-oidc.js'");
    expect(bootstrapEntry).toContain("import { createLocalTextGenerationComposition } from './local-text-generation-capability.js'");
    expect(bootstrapEntry).toContain('const localTextGeneration = createLocalTextGenerationComposition()');
    expect(bootstrapEntry).toContain('void Promise.all([');
    expect(bootstrapEntry).toContain('installNativeLifecycle()');
    expect(bootstrapEntry).toContain('nativeOIDC.installURLCapture()');
    expect(bootstrapEntry).toContain(']).then(() => startMobileBootstrap({');
    expect(bootstrapEntry).toContain('capabilities: localTextGeneration.registry');
    expect(bootstrapEntry).toContain('invalidateCapabilities: localTextGeneration.invalidate');
    expect(bootstrapCore.indexOf('installRuntimeAndStartProduct')).toBeGreaterThan(-1);
  });

  it('contains no hard-coded server or remote WebView navigation', () => {
    const source = `${bootstrapEntry}\n${bootstrapCore}`;
    expect(source).not.toMatch(/\bhttps?:\/\//i);
    expect(source).not.toMatch(/\b(?:server\.url|allowNavigation)\b/);
    expect(source).not.toMatch(/\b(?:location\.assign|window\.open)\s*\(/);
  });

  it('packages the server-default project thumbnail for the app-local origin', () => {
    expect(explicitAssetAllowlist()).toContain('scrumboy.png');
    expect(readFileSync(resolve(webRoot, 'scrumboy.png')).byteLength).toBeGreaterThan(0);
  });
});
