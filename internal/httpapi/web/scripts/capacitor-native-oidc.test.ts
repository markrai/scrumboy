// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  MobileOIDCCoordinator,
  PENDING_MOBILE_OIDC_KEY,
  parseMobileOIDCCallback,
  type NativeOIDCDependencies,
} from '../../../../mobile/capacitor/shell/native-oidc.js';
import type { ServerResponse, ServerTransport } from '../modules/platform/server-transport.js';

const state = 'c3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3M';
const code = 'Y2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2M';
const verifier = 'dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnZ2dnY';
const callbackURL = `com.markrai.scrumboy://oidc/callback?code=${code}&state=${state}`;

function response(status: number, body: unknown): ServerResponse {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: vi.fn(async () => body),
    blob: vi.fn(async () => new Blob()),
  };
}

function fixture(options: { launchURL?: string; now?: number; exchangeStatus?: number } = {}) {
  const values = new Map<string, string>();
  let urlListener: ((event: { url: string }) => void) | null = null;
  const preferences = {
    get: vi.fn(async ({ key }: { key: string }) => ({ value: values.get(key) ?? null })),
    set: vi.fn(async ({ key, value }: { key: string; value: string }) => { values.set(key, value); }),
    remove: vi.fn(async ({ key }: { key: string }) => { values.delete(key); }),
  };
  const app = {
    addListener: vi.fn(async (_name: 'appUrlOpen', listener: (event: { url: string }) => void) => {
      urlListener = listener;
      return { remove: vi.fn(async () => undefined) };
    }),
    getLaunchUrl: vi.fn(async () => options.launchURL ? { url: options.launchURL } : undefined),
  };
  const browser = {
    open: vi.fn(async () => undefined),
    close: vi.fn(async () => undefined),
  };
  const dispatchResult = vi.fn();
  const transport: ServerTransport = {
    request: vi.fn(async (path: string) => path.endsWith('/start')
      ? response(200, { authorizationUrl: 'https://idp.example/authorize?client_id=existing', flowState: state })
      : response(options.exchangeStatus ?? 200, { returnTo: '/dashboard?view=mine' })),
    openEventStream: vi.fn(),
    acquireResource: vi.fn(),
    logout: vi.fn(),
  } as unknown as ServerTransport;
  const deps: Partial<NativeOIDCDependencies> = {
    app: app as NativeOIDCDependencies['app'],
    browser,
    preferences,
    now: () => options.now ?? 1_000_000,
    randomBytes: (size) => new Uint8Array(size).fill(118),
    digestSHA256: async () => new Uint8Array(32).fill(9).buffer,
    dispatchResult,
  };
  return {
    coordinator: new MobileOIDCCoordinator(deps),
    app,
    browser,
    preferences,
    values,
    dispatchResult,
    transport,
    emitURL: (url: string) => {
      if (!urlListener) throw new Error('listener not installed');
      urlListener({ url });
    },
  };
}

function pending(origin = 'https://server.example', expiresAt = 1_500_000) {
  return JSON.stringify({
    version: 1,
    origin,
    state,
    verifier,
    returnTo: '/dashboard',
    createdAt: 900_000,
    expiresAt,
  });
}

beforeEach(() => {
  window.history.replaceState({}, '', '/');
});

describe('C4 native OIDC callback parsing', () => {
  it('accepts only the exact custom callback with one state and one result', () => {
    expect(parseMobileOIDCCallback(callbackURL)).toEqual({ code, state });
    expect(parseMobileOIDCCallback(`com.markrai.scrumboy://oidc/callback?error=provider&state=${state}`)).toEqual({ error: 'provider', state });
    for (const invalid of [
      `https://oidc/callback?code=${code}&state=${state}`,
      `com.markrai.scrumboy://other/callback?code=${code}&state=${state}`,
      `com.markrai.scrumboy://oidc/callback?code=${code}&state=${state}&extra=x`,
      `com.markrai.scrumboy://oidc/callback?code=${code}&state=${state}&state=${state}`,
      `com.markrai.scrumboy://oidc/callback?code=${code}&error=provider&state=${state}`,
      `com.markrai.scrumboy://oidc/callback?code=short&state=${state}`,
    ]) expect(parseMobileOIDCCallback(invalid)).toBeNull();
  });
});

describe('C4 native OIDC coordinator', () => {
  it('installs exactly one listener and captures a cold callback until transport configuration', async () => {
    const f = fixture({ launchURL: callbackURL });
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await Promise.all([f.coordinator.installURLCapture(), f.coordinator.installURLCapture()]);
    expect(f.app.addListener).toHaveBeenCalledOnce();
    expect(f.transport.request).not.toHaveBeenCalled();

    await f.coordinator.configure('https://server.example', f.transport);
    expect(f.transport.request).toHaveBeenCalledWith('/api/auth/oidc/mobile/exchange', expect.objectContaining({ method: 'POST' }));
    expect(f.dispatchResult).not.toHaveBeenCalled();
    f.coordinator.markProductReady();
    expect(f.dispatchResult).toHaveBeenCalledWith({ returnTo: '/dashboard?view=mine' });
    f.emitURL(callbackURL);
    await Promise.resolve();
    expect(f.dispatchResult).toHaveBeenCalledTimes(1);
    expect(f.browser.close).toHaveBeenCalledOnce();
    expect(f.values.has(PENDING_MOBILE_OIDC_KEY)).toBe(false);
  });

  it('processes a warm callback through the configured production transport path', async () => {
    const f = fixture();
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.installURLCapture();
    await f.coordinator.configure('https://server.example', f.transport);
    f.coordinator.markProductReady();
    f.emitURL(callbackURL);
    await vi.waitFor(() => expect(f.dispatchResult).toHaveBeenCalledWith({ returnTo: '/dashboard?view=mine' }));
    const options = vi.mocked(f.transport.request).mock.calls[0][1];
    expect(JSON.parse(String(options?.body))).toEqual({ code, state, verifier });
  });

  it('creates an independent 32-byte app proof, persists it before opening, and uses HTTPS externally', async () => {
    const f = fixture();
    await f.coordinator.configure('https://server.example', f.transport);
    await f.coordinator.start('/dashboard?view=mine');

    const [path, options] = vi.mocked(f.transport.request).mock.calls[0];
    expect(path).toBe('/api/auth/oidc/mobile/start');
    const body = JSON.parse(String(options?.body));
    expect(body).toMatchObject({ codeChallengeMethod: 'S256', returnTo: '/dashboard?view=mine' });
    expect(body.codeChallenge).toMatch(/^[A-Za-z0-9_-]{43}$/);
    const stored = JSON.parse(f.values.get(PENDING_MOBILE_OIDC_KEY) || '{}');
    expect(stored).toMatchObject({ version: 1, origin: 'https://server.example', state, returnTo: '/dashboard?view=mine' });
    expect(stored.verifier).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(f.preferences.set.mock.invocationCallOrder[0]).toBeLessThan(f.browser.open.mock.invocationCallOrder[0]);
    expect(f.browser.open).toHaveBeenCalledWith({ url: expect.stringMatching(/^https:\/\/idp\.example\//) });
  });

  it('supersedes an older pending start before creating the next one', async () => {
    const f = fixture();
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.configure('https://server.example', f.transport);
    await f.coordinator.start('/one');
    await f.coordinator.start('/two');
    expect(f.preferences.remove).toHaveBeenCalledTimes(2);
    expect(JSON.parse(f.values.get(PENDING_MOBILE_OIDC_KEY) || '{}').returnTo).toBe('/two');
  });

  it.each([
    ['different origin', pending('https://other.example')],
    ['expired flow', pending('https://server.example', 999_999)],
  ])('clears a %s before any exchange', async (_name, stored) => {
    const f = fixture();
    f.values.set(PENDING_MOBILE_OIDC_KEY, stored);
    await f.coordinator.installURLCapture();
    await f.coordinator.configure('https://server.example', f.transport);
    f.coordinator.markProductReady();
    f.emitURL(callbackURL);
    await vi.waitFor(() => expect(f.dispatchResult).toHaveBeenCalledWith({ error: 'state_invalid' }));
    expect(f.transport.request).not.toHaveBeenCalled();
    expect(f.values.has(PENDING_MOBILE_OIDC_KEY)).toBe(false);
  });

  it('clears terminal provider errors and publishes only a sanitized error', async () => {
    const f = fixture();
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.installURLCapture();
    await f.coordinator.configure('https://server.example', f.transport);
    f.coordinator.markProductReady();
    f.emitURL(`com.markrai.scrumboy://oidc/callback?error=attacker_text&state=${state}`);
    await vi.waitFor(() => expect(f.dispatchResult).toHaveBeenCalledWith({ error: 'generic' }));
    expect(f.transport.request).not.toHaveBeenCalled();
    expect(f.values.has(PENDING_MOBILE_OIDC_KEY)).toBe(false);
  });

  it('clears rejected exchanges and never publishes proof material', async () => {
    const f = fixture({ exchangeStatus: 401 });
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.installURLCapture();
    await f.coordinator.configure('https://server.example', f.transport);
    f.coordinator.markProductReady();
    f.emitURL(callbackURL);
    await vi.waitFor(() => expect(f.dispatchResult).toHaveBeenCalledWith({ error: 'token' }));
    expect(f.dispatchResult).not.toHaveBeenCalledWith(expect.objectContaining({ code: expect.anything(), state: expect.anything() }));
    expect(f.values.has(PENDING_MOBILE_OIDC_KEY)).toBe(false);
  });

  it('exposes explicit server-reset cleanup', async () => {
    const f = fixture();
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.clearPending();
    expect(f.values.has(PENDING_MOBILE_OIDC_KEY)).toBe(false);
  });

  it('suppresses a late exchange result after server-reset cleanup', async () => {
    const f = fixture();
    let resolveExchange!: (value: ServerResponse) => void;
    vi.mocked(f.transport.request).mockImplementation(() => new Promise((resolve) => { resolveExchange = resolve; }));
    f.values.set(PENDING_MOBILE_OIDC_KEY, pending());
    await f.coordinator.installURLCapture();
    await f.coordinator.configure('https://server.example', f.transport);
    f.coordinator.markProductReady();
    f.emitURL(callbackURL);
    await vi.waitFor(() => expect(f.transport.request).toHaveBeenCalledOnce());

    await f.coordinator.clearPending();
    resolveExchange(response(200, { returnTo: '/dashboard' }));
    await vi.waitFor(() => expect(f.browser.close).toHaveBeenCalledOnce());
    expect(f.dispatchResult).not.toHaveBeenCalled();
  });
});
