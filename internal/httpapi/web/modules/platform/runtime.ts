import { BrowserServerTransport } from './browser-server-transport.js';
import type {
  AcquiredServerResource,
  ServerEventStream,
  ServerRequestOptions,
  ServerResourceOptions,
  ServerResponse,
  ServerTransport,
} from './server-transport.js';

export type RuntimeKind = 'browser' | 'capacitor';

export interface AppRuntime {
  readonly kind: RuntimeKind;
  assetOrigin(): string;
  serverOrigin(): string;
  publicLinkOrigin(): string;
  supportsPWA(): boolean;
  supportsWebPush(): boolean;
  transport(): ServerTransport;
}

const RUNTIME_META_NAME = 'scrumboy-runtime';

function currentOrigin(): string {
  return globalThis.location?.origin || '';
}

const browserTransport = new BrowserServerTransport();

const browserRuntime: AppRuntime = {
  kind: 'browser',
  assetOrigin: currentOrigin,
  serverOrigin: currentOrigin,
  publicLinkOrigin: currentOrigin,
  supportsPWA: () => true,
  supportsWebPush: () => true,
  transport: () => browserTransport,
};

function unconfiguredMobileError(): Error {
  return new Error('The Capacitor ServerTransport has not been installed');
}

const unconfiguredMobileTransport: ServerTransport = {
  request(_path: string, _options?: ServerRequestOptions): Promise<ServerResponse> {
    return Promise.reject(unconfiguredMobileError());
  },
  openEventStream(_path: string): ServerEventStream {
    throw unconfiguredMobileError();
  },
  acquireResource(_path: string, _options?: ServerResourceOptions): Promise<AcquiredServerResource> {
    return Promise.reject(unconfiguredMobileError());
  },
  logout(): Promise<void> {
    return Promise.reject(unconfiguredMobileError());
  },
};

const unconfiguredMobileRuntime: AppRuntime = {
  kind: 'capacitor',
  assetOrigin: currentOrigin,
  serverOrigin: () => {
    throw unconfiguredMobileError();
  },
  publicLinkOrigin: () => {
    throw unconfiguredMobileError();
  },
  supportsPWA: () => false,
  supportsWebPush: () => false,
  transport: () => unconfiguredMobileTransport,
};

function runtimeKindFromDocument(): RuntimeKind {
  const marker = globalThis.document?.querySelector?.(`meta[name="${RUNTIME_META_NAME}"]`);
  return marker?.getAttribute('content') === 'capacitor' ? 'capacitor' : 'browser';
}

let installedRuntime: AppRuntime | null = null;

export function getAppRuntime(): AppRuntime {
  if (installedRuntime) return installedRuntime;
  return runtimeKindFromDocument() === 'capacitor' ? unconfiguredMobileRuntime : browserRuntime;
}

/** Future native bootstrap installs its runtime before product networking starts. */
export function installAppRuntime(runtime: AppRuntime): void {
  installedRuntime = runtime;
}

/** Test-only reset; production code must not switch runtimes after startup. */
export function resetAppRuntimeForTests(): void {
  installedRuntime = null;
}

export function getBrowserRuntimeForTests(): AppRuntime {
  return browserRuntime;
}
