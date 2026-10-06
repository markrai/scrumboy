import { App, type AppLaunchUrl, type URLOpenListenerEvent } from '@capacitor/app';
import { Browser } from '@capacitor/browser';
import type { PluginListenerHandle } from '@capacitor/core';
import { Preferences } from '@capacitor/preferences';
import type { ServerTransport } from '../../../internal/httpapi/web/modules/platform/server-transport.js';

export const NATIVE_OIDC_RESULT_EVENT = 'scrumboy:native-oidc-result';
export const PENDING_MOBILE_OIDC_KEY = 'scrumboy.oidc.pending.v1';

const CALLBACK_PROTOCOL = 'com.markrai.scrumboy:';
const CALLBACK_HOST = 'oidc';
const CALLBACK_PATH = '/callback';
const FLOW_TTL_MS = 10 * 60 * 1000;
const KNOWN_ERRORS = new Set([
  'state_invalid',
  'provider',
  'token',
  'email',
  'auth_time',
  'link_required',
]);

type PreferenceStore = Pick<typeof Preferences, 'get' | 'set' | 'remove'>;
type NativeBrowser = Pick<typeof Browser, 'open' | 'close'>;
type NativeApp = {
  addListener(
    eventName: 'appUrlOpen',
    listener: (event: URLOpenListenerEvent) => void,
  ): Promise<PluginListenerHandle>;
  getLaunchUrl(): Promise<AppLaunchUrl | undefined>;
};

type PendingMobileOIDC = {
  version: 1;
  origin: string;
  state: string;
  verifier: string;
  returnTo: string;
  createdAt: number;
  expiresAt: number;
};

type ParsedCallback =
  | { state: string; code: string; error?: never }
  | { state: string; error: string; code?: never };

type ProductResult = { returnTo: string } | { error: string };

export interface NativeOIDCCoordinator {
  installURLCapture(): Promise<void>;
  configure(origin: string, transport: ServerTransport): Promise<void>;
  start(returnTo: string): Promise<void>;
  clearPending(): Promise<void>;
  markProductReady(): void;
}

export interface NativeOIDCDependencies {
  app: NativeApp;
  browser: NativeBrowser;
  preferences: PreferenceStore;
  now(): number;
  randomBytes(size: number): Uint8Array;
  digestSHA256(value: Uint8Array): Promise<ArrayBuffer>;
  dispatchResult(result: ProductResult): void;
}

const defaults: NativeOIDCDependencies = {
  app: App,
  browser: Browser,
  preferences: Preferences,
  now: () => Date.now(),
  randomBytes: (size) => {
    const value = new Uint8Array(size);
    globalThis.crypto.getRandomValues(value);
    return value;
  },
  digestSHA256: (value) => globalThis.crypto.subtle.digest('SHA-256', value.slice().buffer as ArrayBuffer),
  dispatchResult: (result) => window.dispatchEvent(new CustomEvent(NATIVE_OIDC_RESULT_EVENT, { detail: result })),
};

function base64URL(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function validProof(value: string): boolean {
  return /^[A-Za-z0-9_-]{43}$/.test(value);
}

function sanitizeReturnTo(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return '/';
  try {
    const parsed = new URL(value, 'https://scrumboy.invalid');
    return parsed.origin === 'https://scrumboy.invalid' ? `${parsed.pathname}${parsed.search}${parsed.hash}` : '/';
  } catch {
    return '/';
  }
}

function sanitizeError(value: string): string {
  return KNOWN_ERRORS.has(value) ? value : 'generic';
}

export function parseMobileOIDCCallback(rawURL: string): ParsedCallback | null {
  let parsed: URL;
  try {
    parsed = new URL(rawURL);
  } catch {
    return null;
  }
  if (parsed.protocol !== CALLBACK_PROTOCOL || parsed.hostname !== CALLBACK_HOST || parsed.pathname !== CALLBACK_PATH) return null;
  if (parsed.username || parsed.password || parsed.port || parsed.hash) return null;
  const keys = [...parsed.searchParams.keys()];
  if (keys.some((key) => key !== 'state' && key !== 'code' && key !== 'error')) return null;
  const states = parsed.searchParams.getAll('state');
  const codes = parsed.searchParams.getAll('code');
  const errors = parsed.searchParams.getAll('error');
  if (states.length !== 1 || !validProof(states[0])) return null;
  if (codes.length === 1 && errors.length === 0 && validProof(codes[0])) return { state: states[0], code: codes[0] };
  if (errors.length === 1 && codes.length === 0 && errors[0] !== '') return { state: states[0], error: errors[0] };
  return null;
}

function parsePending(value: string | null): PendingMobileOIDC | null {
  if (!value) return null;
  try {
    const pending = JSON.parse(value) as Partial<PendingMobileOIDC>;
    if (pending.version !== 1 || typeof pending.origin !== 'string' || !validProof(pending.state || '') ||
        !validProof(pending.verifier || '') || typeof pending.returnTo !== 'string' ||
        typeof pending.createdAt !== 'number' || typeof pending.expiresAt !== 'number') return null;
    return pending as PendingMobileOIDC;
  } catch {
    return null;
  }
}

export class MobileOIDCCoordinator implements NativeOIDCCoordinator {
  readonly #deps: NativeOIDCDependencies;
  #capturePromise: Promise<void> | null = null;
  #origin: string | null = null;
  #transport: ServerTransport | null = null;
  #queuedCallback: string | null = null;
  #callbackSerial: Promise<void> = Promise.resolve();
  #startSerial: Promise<void> = Promise.resolve();
  #pendingGeneration = 0;
  readonly #capturedCallbacks = new Set<string>();
  #productReady = false;
  #queuedProductResult: ProductResult | null = null;

  constructor(overrides: Partial<NativeOIDCDependencies> = {}) {
    this.#deps = { ...defaults, ...overrides };
  }

  installURLCapture(): Promise<void> {
    if (this.#capturePromise) return this.#capturePromise;
    this.#capturePromise = (async () => {
      const listener = this.#deps.app.addListener('appUrlOpen', ({ url }) => this.#capture(url));
      const launch = this.#deps.app.getLaunchUrl();
      await listener;
      const launchURL = await launch;
      if (launchURL?.url) this.#capture(launchURL.url);
    })().catch((error) => {
      this.#capturePromise = null;
      throw error;
    });
    return this.#capturePromise;
  }

  async configure(origin: string, transport: ServerTransport): Promise<void> {
    this.#origin = origin;
    this.#transport = transport;
    const pending = await this.#loadPending();
    if (pending && (pending.origin !== origin || pending.expiresAt <= this.#deps.now())) {
      await this.clearPending();
    }
    if (this.#queuedCallback) {
      const callback = this.#queuedCallback;
      this.#queuedCallback = null;
      await this.#enqueueCallback(callback);
    }
  }

  async start(returnTo: string): Promise<void> {
    this.#startSerial = this.#startSerial.catch(() => undefined).then(() => this.#startOnce(returnTo));
    return this.#startSerial;
  }

  async #startOnce(returnTo: string): Promise<void> {
    if (!this.#origin || !this.#transport) throw new Error('Native OIDC is not configured');
    await this.clearPending();

    const verifier = base64URL(this.#deps.randomBytes(32));
    const challenge = base64URL(new Uint8Array(await this.#deps.digestSHA256(new TextEncoder().encode(verifier))));
    const safeReturnTo = sanitizeReturnTo(returnTo);
    const response = await this.#transport.request('/api/auth/oidc/mobile/start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Scrumboy': '1' },
      body: JSON.stringify({ codeChallenge: challenge, codeChallengeMethod: 'S256', returnTo: safeReturnTo }),
    });
    if (!response.ok) throw new Error('Native OIDC start was rejected');
    const body = await response.json() as { authorizationUrl?: unknown; flowState?: unknown };
    if (typeof body.authorizationUrl !== 'string' || typeof body.flowState !== 'string' || !validProof(body.flowState)) {
      throw new Error('Native OIDC start returned an invalid response');
    }
    const authorizationURL = new URL(body.authorizationUrl);
    if (authorizationURL.protocol !== 'https:') throw new Error('Native OIDC authorization must use HTTPS');

    const now = this.#deps.now();
    const pending: PendingMobileOIDC = {
      version: 1,
      origin: this.#origin,
      state: body.flowState,
      verifier,
      returnTo: safeReturnTo,
      createdAt: now,
      expiresAt: now + FLOW_TTL_MS,
    };
    await this.#deps.preferences.set({ key: PENDING_MOBILE_OIDC_KEY, value: JSON.stringify(pending) });
    try {
      await this.#deps.browser.open({ url: authorizationURL.toString() });
    } catch (error) {
      await this.clearPending();
      throw error;
    }
  }

  async clearPending(): Promise<void> {
    this.#pendingGeneration += 1;
    await this.#removePending();
  }

  markProductReady(): void {
    this.#productReady = true;
    if (this.#queuedProductResult) {
      const result = this.#queuedProductResult;
      this.#queuedProductResult = null;
      this.#deps.dispatchResult(result);
    }
  }

  #capture(rawURL: string): void {
    const callback = parseMobileOIDCCallback(rawURL);
    if (!callback) return;
    if (this.#capturedCallbacks.has(rawURL)) return;
    this.#capturedCallbacks.add(rawURL);
    if (!this.#origin || !this.#transport) {
      this.#queuedCallback = rawURL;
      return;
    }
    void this.#enqueueCallback(rawURL);
  }

  #enqueueCallback(rawURL: string): Promise<void> {
    this.#callbackSerial = this.#callbackSerial.then(() => this.#processCallback(rawURL));
    return this.#callbackSerial;
  }

  async #loadPending(): Promise<PendingMobileOIDC | null> {
    const stored = await this.#deps.preferences.get({ key: PENDING_MOBILE_OIDC_KEY });
    const pending = parsePending(stored.value);
    if (stored.value && !pending) await this.clearPending();
    return pending;
  }

  async #removePending(): Promise<void> {
    await this.#deps.preferences.remove({ key: PENDING_MOBILE_OIDC_KEY });
  }

  async #processCallback(rawURL: string): Promise<void> {
    const callback = parseMobileOIDCCallback(rawURL);
    if (!callback || !this.#origin || !this.#transport) return;
    const callbackGeneration = this.#pendingGeneration;
    const pending = await this.#loadPending();
    if (!pending || pending.origin !== this.#origin || pending.expiresAt <= this.#deps.now() || callback.state !== pending.state) {
      await this.clearPending();
      await this.#closeBrowser();
      this.#publish({ error: 'state_invalid' });
      return;
    }
    if (typeof callback.error === 'string') {
      await this.clearPending();
      await this.#closeBrowser();
      this.#publish({ error: sanitizeError(callback.error) });
      return;
    }

    try {
      const response = await this.#transport.request('/api/auth/oidc/mobile/exchange', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Scrumboy': '1' },
        body: JSON.stringify({ code: callback.code, state: callback.state, verifier: pending.verifier }),
      });
      if (callbackGeneration !== this.#pendingGeneration) return;
      if (!response.ok) throw new Error('exchange rejected');
      const body = await response.json() as { returnTo?: unknown };
      if (callbackGeneration !== this.#pendingGeneration) return;
      await this.#removePending();
      this.#publish({ returnTo: sanitizeReturnTo(body.returnTo ?? pending.returnTo) });
    } catch {
      if (callbackGeneration !== this.#pendingGeneration) return;
      await this.#removePending();
      this.#publish({ error: 'token' });
    } finally {
      await this.#closeBrowser();
    }
  }

  async #closeBrowser(): Promise<void> {
    try {
      await this.#deps.browser.close();
    } catch {
      // The browser may already have closed during an Android task handoff.
    }
  }

  #publish(result: ProductResult): void {
    if (this.#productReady) this.#deps.dispatchResult(result);
    else this.#queuedProductResult = result;
  }
}

export const nativeOIDC = new MobileOIDCCoordinator();
