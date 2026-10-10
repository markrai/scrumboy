import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

type FakeResponse = { ok: boolean; body: string; clone(): FakeResponse };
type Handler = (event: any) => void;

function response(body: string): FakeResponse {
  return { ok: true, body, clone() { return response(body); } };
}

/** Runs the real sw.js with fake caches and network. */
function loadServiceWorker(network: (path: string) => Promise<FakeResponse>) {
  const source = readFileSync(resolve(process.cwd(), 'sw.js'), 'utf8').replaceAll('{{VERSION}}', 'test');
  const handlers = new Map<string, Handler>();
  const store = new Map<string, FakeResponse>();
  const keyOf = (input: any) => new URL(typeof input === 'string' ? input : input.url, 'https://app.test').pathname;
  const cache = {
    add: async (url: string) => { store.set(keyOf(url), await network(keyOf(url))); },
    put: async (req: any, res: FakeResponse) => { store.set(keyOf(req), res); },
  };
  const caches = {
    open: async () => cache,
    keys: async () => [],
    delete: async () => true,
    match: async (input: any) => store.get(keyOf(input)),
  };
  const self = {
    location: new URL('https://app.test/'),
    addEventListener: (type: string, handler: Handler) => handlers.set(type, handler),
    clients: { claim: async () => {}, matchAll: async () => [], openWindow: async () => null },
    registration: { showNotification: async () => {} },
    skipWaiting: () => {},
  };
  const fetchImpl = (req: any) => network(keyOf(req));
  new Function('self', 'caches', 'fetch', 'console', source)(self, caches, fetchImpl, { log() {}, warn() {}, error() {} });

  async function install(): Promise<void> {
    let pending: Promise<unknown> = Promise.resolve();
    handlers.get('install')!({ waitUntil: (p: Promise<unknown>) => { pending = p; } });
    await pending;
  }

  async function request(path: string, init: { navigate?: boolean; method?: string } = {}): Promise<FakeResponse | 'bypassed'> {
    let responded: Promise<FakeResponse> | null = null;
    handlers.get('fetch')!({
      request: { url: `https://app.test${path}`, method: init.method ?? 'GET', mode: init.navigate ? 'navigate' : 'cors', destination: init.navigate ? 'document' : '' },
      respondWith: (p: Promise<FakeResponse>) => { responded = p; },
    });
    return responded ? await responded : 'bypassed';
  }

  return { install, request, store };
}

const MARKETING = '<html>marketing landing</html>';
const APP_SHELL = '<html>app shell</html>';

describe('service worker shell separation', () => {
  it('precaches /_app as the application shell, not /index.html', async () => {
    const sw = loadServiceWorker(async (path) => response(path === '/' ? MARKETING : APP_SHELL));
    await sw.install();
    expect(sw.store.get('/_app')?.body).toBe(APP_SHELL);
    expect(sw.store.has('/index.html')).toBe(false);
  });

  it('offline board and workspace navigations fall back to the app shell, never marketing HTML', async () => {
    let online = true;
    const sw = loadServiceWorker(async (path) => {
      if (!online) throw new TypeError('offline');
      return response(path === '/' ? MARKETING : APP_SHELL);
    });
    await sw.install();
    online = false;
    expect(((await sw.request('/ignite', { navigate: true })) as FakeResponse).body).toBe(APP_SHELL);
    expect(((await sw.request('/ignite/t/3', { navigate: true })) as FakeResponse).body).toBe(APP_SHELL);
    expect(((await sw.request('/_app', { navigate: true })) as FakeResponse).body).toBe(APP_SHELL);
    // The root keeps the document it last served (marketing in landing mode).
    expect(((await sw.request('/', { navigate: true })) as FakeResponse).body).toBe(MARKETING);
  });

  it('documents are network-first so a landing flag change is picked up online', async () => {
    let rootBody = MARKETING;
    const sw = loadServiceWorker(async (path) => response(path === '/' ? rootBody : APP_SHELL));
    await sw.install();
    rootBody = APP_SHELL;
    expect(((await sw.request('/', { navigate: true })) as FakeResponse).body).toBe(APP_SHELL);
    expect(sw.store.get('/_app')?.body).toBe(APP_SHELL);
  });

  it('never intercepts or caches API, publication, public board, or SSE requests', async () => {
    const sw = loadServiceWorker(async () => response('{}'));
    for (const path of [
      '/api/public/board/ignite',
      '/api/public/board/ignite/events',
      '/api/board/ignite/publication',
      '/api/me/realtime',
      '/api/auth/status',
    ]) {
      expect(await sw.request(path)).toBe('bypassed');
      expect(sw.store.has(path)).toBe(false);
    }
    expect(await sw.request('/api/board/ignite/publication', { method: 'PATCH' })).toBe('bypassed');
  });
});
