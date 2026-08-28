// @vitest-environment happy-dom

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { transformIndex } from './capacitor-web-artifact-lib.mjs';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = resolve(webRoot, '..', '..', '..');
const bootstrapSource = readFileSync(
  resolve(repositoryRoot, 'mobile', 'capacitor', 'shell', 'bootstrap.js'),
  'utf8',
);
const authoritativeIndex = readFileSync(resolve(webRoot, 'index.html'), 'utf8');

describe('C1 mobile bootstrap', () => {
  beforeEach(() => {
    vi.unstubAllGlobals();
    document.head.innerHTML = '<meta name="scrumboy-runtime" content="capacitor">';
    document.body.innerHTML = '<div id="app"></div><div id="product-shell"></div>';
  });

  it('renders only the packaged local status without starting network APIs', () => {
    const fetchSpy = vi.fn();
    const eventSourceSpy = vi.fn();
    const xhrSpy = vi.fn();
    vi.stubGlobal('fetch', fetchSpy);
    vi.stubGlobal('EventSource', class {
      constructor() {
        eventSourceSpy();
      }
    });
    vi.stubGlobal('XMLHttpRequest', class {
      constructor() {
        xhrSpy();
      }
    });

    Function(bootstrapSource)();

    expect(document.body.textContent).toContain('Scrumboy');
    expect(document.body.textContent).toContain('Mobile shell ready.');
    expect(document.body.textContent).toContain('Server connection will be configured in the next phase.');
    expect(document.querySelector('#product-shell')).toBeNull();
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(eventSourceSpy).not.toHaveBeenCalled();
    expect(xhrSpy).not.toHaveBeenCalled();
  });

  it('fails closed without the packaged Capacitor marker', () => {
    document.head.innerHTML = '';

    expect(() => Function(bootstrapSource)()).toThrow(
      'Refusing to start the mobile bootstrap outside the packaged Capacitor runtime',
    );
  });

  it('replaces product startup with the bootstrap while retaining local packaged references', () => {
    const mobileIndex = transformIndex(authoritativeIndex, 'c1-test-version');
    const bootstrapPosition = mobileIndex.indexOf('src="/bootstrap.js"');

    expect(bootstrapPosition).toBeGreaterThan(-1);
    expect(mobileIndex).not.toMatch(/<script\b[^>]*\bsrc="\/app\.js/);
    expect(mobileIndex).not.toMatch(/\b(?:src|href|content)="https?:\/\//i);
    expect(bootstrapSource).not.toMatch(/\b(?:fetch|EventSource|XMLHttpRequest)\s*\(/);
    expect(bootstrapSource).not.toMatch(/\bhttps?:\/\//i);
    expect(bootstrapSource).not.toMatch(/\bimport\s*\(|\/app\.js\b/);
  });
});
