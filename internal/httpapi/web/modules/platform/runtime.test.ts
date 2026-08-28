// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserServerTransport } from './browser-server-transport.js';
import {
  getAppRuntime,
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from './runtime.js';
import type { ServerEventStream, ServerTransport } from './server-transport.js';

afterEach(() => {
  resetAppRuntimeForTests();
  document.head.querySelector('meta[name="scrumboy-runtime"]')?.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('runtime origin and feature boundaries', () => {
  it('keeps all browser origins aligned and browser capabilities enabled', () => {
    const runtime = getAppRuntime();
    expect(runtime.kind).toBe('browser');
    expect(runtime.assetOrigin()).toBe(window.location.origin);
    expect(runtime.serverOrigin()).toBe(window.location.origin);
    expect(runtime.publicLinkOrigin()).toBe(window.location.origin);
    expect(runtime.supportsPWA()).toBe(true);
    expect(runtime.supportsWebPush()).toBe(true);
    expect(runtime.supportsInteractiveOIDC()).toBe(true);
  });

  it('recognizes the generated mobile marker without importing Capacitor', async () => {
    const marker = document.createElement('meta');
    marker.name = 'scrumboy-runtime';
    marker.content = 'capacitor';
    document.head.appendChild(marker);

    const runtime = getAppRuntime();
    expect(runtime.kind).toBe('capacitor');
    expect(runtime.assetOrigin()).toBe(window.location.origin);
    expect(runtime.supportsPWA()).toBe(false);
    expect(runtime.supportsWebPush()).toBe(false);
    expect(runtime.supportsInteractiveOIDC()).toBe(false);
    await expect(runtime.transport().request('/api/auth/status')).rejects.toThrow('has not been installed');
  });

  it('keeps asset, server, and public-link origins distinct in a fake mobile runtime', () => {
    const transport = {} as ServerTransport;
    const runtime: AppRuntime = {
      kind: 'capacitor',
      assetOrigin: () => 'https://localhost',
      serverOrigin: () => 'https://scrumboy.example',
      publicLinkOrigin: () => 'https://public.scrumboy.example',
      supportsPWA: () => false,
      supportsWebPush: () => false,
      supportsInteractiveOIDC: () => false,
      transport: () => transport,
    };
    installAppRuntime(runtime);

    expect(getAppRuntime().assetOrigin()).toBe('https://localhost');
    expect(getAppRuntime().serverOrigin()).toBe('https://scrumboy.example');
    expect(getAppRuntime().publicLinkOrigin()).toBe('https://public.scrumboy.example');
    expect(getAppRuntime().transport()).toBe(transport);
  });
});

describe('BrowserServerTransport parity', () => {
  it('passes request paths and options to fetch unchanged', async () => {
    const response = new Response(JSON.stringify({ ok: true }), { status: 200 });
    const fetchMock = vi.fn().mockResolvedValue(response);
    vi.stubGlobal('fetch', fetchMock);
    const transport = new BrowserServerTransport();
    const body = '{"name":"unchanged"}';
    const options = {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Scrumboy': '1' },
      body,
    };

    await expect(transport.request('/api/example', options)).resolves.toBe(response);
    expect(fetchMock).toHaveBeenCalledWith('/api/example', options);
  });

  it('preserves multipart payloads without adding a content-type header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const form = new FormData();
    form.append('file', new Blob(['x']), 'x.txt');

    await new BrowserServerTransport().request('/api/upload', {
      method: 'POST',
      headers: { 'X-Scrumboy': '1' },
      body: form,
    });

    expect(fetchMock).toHaveBeenCalledWith('/api/upload', {
      method: 'POST',
      headers: { 'X-Scrumboy': '1' },
      body: form,
    });
  });

  it('constructs the same absolute browser EventSource URL and exposes close', () => {
    const close = vi.fn();
    const EventSourceMock = vi.fn(() => ({ onopen: null, onmessage: null, onerror: null, close }));
    vi.stubGlobal('EventSource', EventSourceMock);

    const stream = new BrowserServerTransport().openEventStream('/api/me/realtime');

    expect(EventSourceMock).toHaveBeenCalledWith(new URL('/api/me/realtime', window.location.origin).toString());
    stream.close();
    expect(close).toHaveBeenCalledOnce();
  });

  it('acquires browser server resources asynchronously without fetching or changing the URL', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const acquisition = new BrowserServerTransport()
      .acquireResource('/api/user/wallpaper/image?rev=3');
    let settled = false;
    const observed = acquisition.then((resource) => {
      settled = true;
      return resource;
    });

    expect(settled).toBe(false);
    const resource = await observed;
    expect(resource.url).toBe('/api/user/wallpaper/image?rev=3');
    expect(fetchMock).not.toHaveBeenCalled();
    expect(() => {
      resource.release();
      resource.release();
    }).not.toThrow();
  });

  it('rejects an already-aborted browser resource acquisition without fetching', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    controller.abort();

    await expect(new BrowserServerTransport().acquireResource('/api/resource', {
      signal: controller.signal,
    })).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('retains the document-form logout path', async () => {
    const submit = vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {});

    await new BrowserServerTransport().logout();

    const form = document.body.querySelector('form');
    expect(form?.method).toBe('POST');
    expect(form?.getAttribute('action')).toBe('/api/auth/logout');
    expect(submit).toHaveBeenCalledOnce();
  });
});

export function createFakeStream(): ServerEventStream {
  return { onopen: null, onmessage: null, onerror: null, close: vi.fn() };
}
