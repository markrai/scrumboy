// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ScrumboyTransportPlugin, NativeTransportEvent } from '../../../../mobile/capacitor/shell/native-plugin.js';
import { NativeServerTransport } from '../../../../mobile/capacitor/shell/native-server-transport.js';
import {
  clearScrumboyWebState,
  createCapacitorRuntime,
  installRuntimeAndStartProduct,
} from '../../../../mobile/capacitor/shell/native-runtime.js';
import type { AppCapabilityRegistry } from '../modules/platform/client-capabilities.js';
import type { AppRuntime } from '../modules/platform/runtime.js';
import type { ServerTransport } from '../modules/platform/server-transport.js';

function base64(value: string): string {
  return btoa(value);
}

function pluginFake(overrides: Partial<ScrumboyTransportPlugin> = {}): ScrumboyTransportPlugin {
  return {
    probeServer: vi.fn(),
    configure: vi.fn(),
    request: vi.fn(async () => ({ status: 200, bodyBase64: base64('{}') })),
    cancelRequest: vi.fn(async () => undefined),
    openEventStream: vi.fn(async () => undefined),
    closeEventStream: vi.fn(async () => undefined),
    acquireResource: vi.fn(async () => ({ handle: 'resource-1', fileUri: 'file:///private/resource-1' })),
    releaseResource: vi.fn(async () => undefined),
    logout: vi.fn(async () => undefined),
    resetForServerChange: vi.fn(async () => undefined),
    addListener: vi.fn(async () => ({ remove: vi.fn(async () => undefined) })),
    ...overrides,
  };
}

function transportStub(): ServerTransport {
  return {
    request: vi.fn(),
    openEventStream: vi.fn(),
    acquireResource: vi.fn(),
    logout: vi.fn(),
  } as unknown as ServerTransport;
}

function lookupUnknownCapability(runtime: AppRuntime): unknown {
  return Reflect.apply(runtime.capability, runtime, ['test-only']);
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  window.history.replaceState({}, '', '/');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('C2 Capacitor runtime', () => {
  it('keeps packaged assets local while selecting the server for APIs and public links', () => {
    const transport = transportStub();
    const startInteractiveOIDC = vi.fn(async () => undefined);
    const runtime = createCapacitorRuntime('https://server.example', transport, startInteractiveOIDC);

    expect(runtime.kind).toBe('capacitor');
    expect(runtime.assetOrigin()).toBe(window.location.origin);
    expect(runtime.serverOrigin()).toBe('https://server.example');
    expect(runtime.publicLinkOrigin()).toBe('https://server.example');
    expect(runtime.supportsPWA()).toBe(false);
    expect(runtime.supportsWebPush()).toBe(false);
    expect(runtime.supportsInteractiveOIDC()).toBe(true);
    void runtime.startInteractiveOIDC('/dashboard');
    expect(startInteractiveOIDC).toHaveBeenCalledWith('/dashboard');
    expect(runtime.transport()).toBe(transport);
    expect(lookupUnknownCapability(runtime)).toBeNull();
  });

  it('delegates capability lookup to the injected registry', () => {
    const get = vi.fn(() => null);
    const capabilities: AppCapabilityRegistry = { get };
    const runtime = createCapacitorRuntime(
      'https://server.example',
      transportStub(),
      undefined,
      capabilities,
    );

    expect(lookupUnknownCapability(runtime)).toBeNull();
    expect(get).toHaveBeenCalledWith('test-only');
  });

  it('installs the runtime before importing packaged app.js', async () => {
    const order: string[] = [];
    const get = vi.fn(() => null);
    const capabilities: AppCapabilityRegistry = { get };
    let installedRuntime: AppRuntime | null = null;
    const installAppRuntime = vi.fn((runtime: AppRuntime) => {
      installedRuntime = runtime;
      order.push('runtime-installed');
    });
    const importer = vi.fn(async (path: string) => {
      order.push(`import:${path}`);
      if (path === '/app.js') {
        expect(installedRuntime).not.toBeNull();
        expect(lookupUnknownCapability(installedRuntime!)).toBeNull();
      }
      return path === '/dist/platform/runtime.js' ? { installAppRuntime } : {};
    });

    await installRuntimeAndStartProduct(
      'https://server.example',
      transportStub(),
      importer,
      undefined,
      capabilities,
    );

    expect(order).toEqual([
      'import:/dist/platform/runtime.js',
      'runtime-installed',
      'import:/app.js',
    ]);
    expect(get).toHaveBeenCalledWith('test-only');
  });

  it('clears user-scoped WebView state while retaining device-global preferences', () => {
    localStorage.setItem('scrumboy.locale', 'de');
    localStorage.setItem('scrumboy_theme', 'dark');
    localStorage.setItem('scrumboy.keybindings', '{}');
    localStorage.setItem('scrumboy_assignment_notify_muted', '1');
    localStorage.setItem('scrumboy.current-user', 'private');
    sessionStorage.setItem('scrumboy.temp', 'private');

    clearScrumboyWebState();

    expect(localStorage.getItem('scrumboy.locale')).toBe('de');
    expect(localStorage.getItem('scrumboy_theme')).toBe('dark');
    expect(localStorage.getItem('scrumboy.keybindings')).toBe('{}');
    expect(localStorage.getItem('scrumboy_assignment_notify_muted')).toBe('1');
    expect(localStorage.getItem('scrumboy.current-user')).toBeNull();
    expect(sessionStorage.length).toBe(0);
  });
});

describe('C2 native ServerTransport adapter', () => {
  it('maps native status/body without exposing response headers or cookies', async () => {
    const plugin = pluginFake({
      request: vi.fn(async () => ({ status: 401, bodyBase64: base64('{"error":"unauthorized"}') })),
    });
    const transport = new NativeServerTransport({ plugin });

    const response = await transport.request('/api/auth/status', {
      headers: { 'X-Scrumboy': '1' },
    });

    expect(response.status).toBe(401);
    expect(response.ok).toBe(false);
    expect(await response.json()).toEqual({ error: 'unauthorized' });
    expect(Object.keys(response).sort()).toEqual(['ok', 'status']);
    expect(plugin.request).toHaveBeenCalledWith(expect.objectContaining({
      path: '/api/auth/status',
      headers: { 'X-Scrumboy': '1' },
    }));
  });

  it('maps AbortSignal to native cancellation and never returns a late response', async () => {
    let resolveRequest!: (value: { status: number; bodyBase64: string }) => void;
    const plugin = pluginFake({
      request: vi.fn(() => new Promise((resolve) => { resolveRequest = resolve; })),
    });
    const transport = new NativeServerTransport({ plugin });
    const controller = new AbortController();
    const pending = transport.request('/api/boards', { signal: controller.signal });
    await vi.waitFor(() => expect(plugin.request).toHaveBeenCalledOnce());

    controller.abort();
    resolveRequest({ status: 200, bodyBase64: base64('{}') });

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    expect(plugin.cancelRequest).toHaveBeenCalledWith({
      requestId: expect.any(String),
    });
  });

  it('installs the SSE listener before opening and routes only live matching streams', async () => {
    let listener!: (event: NativeTransportEvent) => void;
    let releaseListener!: () => void;
    const plugin = pluginFake({
      addListener: vi.fn((_name, callback) => {
        listener = callback;
        return new Promise((resolve) => {
          releaseListener = () => resolve({ remove: vi.fn(async () => undefined) });
        });
      }),
    });
    const transport = new NativeServerTransport({ plugin });
    const stream = transport.openEventStream('/api/me/realtime');
    const onopen = vi.fn();
    const onmessage = vi.fn();
    const onerror = vi.fn();
    stream.onopen = onopen;
    stream.onmessage = onmessage;
    stream.onerror = onerror;

    expect(plugin.addListener).toHaveBeenCalledOnce();
    expect(plugin.openEventStream).not.toHaveBeenCalled();
    releaseListener();
    await vi.waitFor(() => expect(plugin.openEventStream).toHaveBeenCalledOnce());
    const streamId = vi.mocked(plugin.openEventStream).mock.calls[0][0].streamId;
    listener({ streamId: 'other', kind: 'message', data: 'ignored' });
    listener({ streamId, kind: 'open' });
    listener({ streamId, kind: 'message', data: 'hello' });
    listener({ streamId, kind: 'error', code: 'connect_failure' });

    expect(onopen).toHaveBeenCalledOnce();
    expect(onmessage).toHaveBeenCalledOnce();
    expect(onmessage.mock.calls[0][0].data).toBe('hello');
    expect(onerror).toHaveBeenCalledOnce();

    stream.close();
    listener({ streamId, kind: 'message', data: 'late' });
    expect(onmessage).toHaveBeenCalledOnce();
    expect(plugin.closeEventStream).toHaveBeenCalledWith({ streamId });
  });

  it('converts resource URLs and releases native ownership idempotently', async () => {
    const plugin = pluginFake();
    const convertFileSrc = vi.fn((uri: string) => `capacitor://localhost/_file_${uri}`);
    const transport = new NativeServerTransport({ plugin, convertFileSrc });

    const resource = await transport.acquireResource('/api/user/wallpaper/image?revision=7');

    expect(convertFileSrc).toHaveBeenCalledWith('file:///private/resource-1');
    expect(resource.url).toContain('capacitor://localhost/_file_');
    resource.release();
    resource.release();
    await vi.waitFor(() => expect(plugin.releaseResource).toHaveBeenCalledTimes(1));
  });

  it('releases a late acquired resource after abort', async () => {
    let resolveResource!: (value: { handle: string; fileUri: string }) => void;
    const plugin = pluginFake({
      acquireResource: vi.fn(() => new Promise((resolve) => { resolveResource = resolve; })),
    });
    const transport = new NativeServerTransport({ plugin });
    const controller = new AbortController();
    const pending = transport.acquireResource('/api/user/wallpaper/image', { signal: controller.signal });
    await vi.waitFor(() => expect(plugin.acquireResource).toHaveBeenCalledOnce());

    controller.abort();
    resolveResource({ handle: 'late-resource', fileUri: 'file:///private/late' });

    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    expect(plugin.releaseResource).toHaveBeenCalledWith({ handle: 'late-resource' });
  });

  it('logout clears native and scoped web state without touching server selection', async () => {
    localStorage.setItem('scrumboy.locale', 'en');
    localStorage.setItem('user-private', 'secret');
    const plugin = pluginFake();
    const onLogout = vi.fn(() => clearScrumboyWebState());
    const transport = new NativeServerTransport({ plugin, onLogout });

    await transport.logout();

    expect(plugin.logout).toHaveBeenCalledOnce();
    expect(localStorage.getItem('user-private')).toBeNull();
    expect(localStorage.getItem('scrumboy.locale')).toBe('en');
  });
});
