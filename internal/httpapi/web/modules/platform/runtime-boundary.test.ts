// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from '../api.js';
import { buildPublicTodoUrl } from '../dialogs/todo-links.js';
import * as i18n from '../i18n/index.js';
import {
  loadMermaidSemanticEdgeConfig,
  resetMermaidSemanticEdgeConfigCacheForTests,
} from '../mermaid-semantic-edges.js';
import {
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from './runtime.js';
import type { ServerEventStream, ServerTransport } from './server-transport.js';

function fakeTransport(request = vi.fn()): ServerTransport {
  return {
    request,
    openEventStream: vi.fn(() => ({
      onopen: null,
      onmessage: null,
      onerror: null,
      close: vi.fn(),
    } satisfies ServerEventStream)),
    acquireResource: vi.fn(async (path: string) => ({
      url: `blob:mobile/${encodeURIComponent(path)}`,
      release: vi.fn(),
    })),
    logout: vi.fn().mockResolvedValue(undefined),
  };
}

function installFakeMobile(transport: ServerTransport): AppRuntime {
  const runtime: AppRuntime = {
    kind: 'capacitor',
    assetOrigin: () => 'https://localhost',
    serverOrigin: () => 'https://server.example',
    publicLinkOrigin: () => 'https://public.example',
    supportsPWA: () => false,
    supportsWebPush: () => false,
    transport: () => transport,
  };
  installAppRuntime(runtime);
  return runtime;
}

afterEach(() => {
  resetAppRuntimeForTests();
  i18n.resetI18nForTests();
  resetMermaidSemanticEdgeConfigCacheForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('server versus packaged-asset boundary', () => {
  it('routes server APIs through the fake transport while locale/config assets stay local', async () => {
    const request = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: false }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ slug: 'alpha', todos: [] }), { status: 200 }));
    installFakeMobile(fakeTransport(request));
    const assetFetch = vi.fn(async (path: string) => {
      if (path.startsWith('/dist/i18n/locales/')) {
        return new Response(JSON.stringify({ sample: path }), { status: 200 });
      }
      if (path === '/mermaid-semantic-edges.json') {
        return new Response(JSON.stringify({
          positiveColor: '#00ff00',
          negativeColor: '#ff0000',
          pairs: [{ positive: 'yes', negative: 'no' }],
        }), { status: 200 });
      }
      throw new Error(`unexpected asset fetch: ${path}`);
    });
    vi.stubGlobal('fetch', assetFetch);

    await expect(apiFetch('/api/auth/status')).resolves.toEqual({ authenticated: false });
    await expect(apiFetch('/api/board/alpha')).resolves.toEqual({ slug: 'alpha', todos: [] });
    await i18n.initI18n({ locale: 'de', storage: null });
    await loadMermaidSemanticEdgeConfig();

    expect(request).toHaveBeenCalledWith('/api/auth/status', {
      headers: { 'Content-Type': 'application/json', 'X-Scrumboy': '1' },
    });
    expect(request).toHaveBeenCalledWith('/api/board/alpha', {
      headers: { 'Content-Type': 'application/json', 'X-Scrumboy': '1' },
    });
    expect(assetFetch.mock.calls.map(([path]) => path)).toEqual([
      '/dist/i18n/locales/en.json',
      '/dist/i18n/locales/de.json',
      '/mermaid-semantic-edges.json',
    ]);
    expect(request).not.toHaveBeenCalledWith(expect.stringContaining('/dist/'), expect.anything());
    expect(request).not.toHaveBeenCalledWith('/mermaid-semantic-edges.json', expect.anything());
  });

  it('uses current origin for browser public links and selected public origin for fake mobile', () => {
    expect(buildPublicTodoUrl('alpha', 7)).toBe(`${window.location.origin}/alpha/t/7`);
    installFakeMobile(fakeTransport());
    expect(buildPublicTodoUrl('alpha', 7)).toBe('https://public.example/alpha/t/7');
  });

  it('lets a fake native transport complete logout without document navigation', async () => {
    const transport = fakeTransport();
    installFakeMobile(transport);

    await transport.logout();

    expect(transport.logout).toHaveBeenCalledOnce();
    expect(document.body.querySelector('form[action="/api/auth/logout"]')).toBeNull();
  });
});
