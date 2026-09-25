import { emptyClientCapabilityRegistry } from './client-capabilities.js';
import { BrowserServerTransport } from './browser-server-transport.js';
const RUNTIME_META_NAME = 'scrumboy-runtime';
function currentOrigin() {
    return globalThis.location?.origin || '';
}
const browserTransport = new BrowserServerTransport();
function emptyCapability(name) {
    return emptyClientCapabilityRegistry.get(name);
}
const browserRuntime = {
    kind: 'browser',
    capability: emptyCapability,
    assetOrigin: currentOrigin,
    serverOrigin: currentOrigin,
    publicLinkOrigin: currentOrigin,
    supportsPWA: () => true,
    supportsWebPush: () => true,
    supportsInteractiveOIDC: () => true,
    startInteractiveOIDC: async (returnTo) => {
        globalThis.location?.assign(`/api/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`);
    },
    transport: () => browserTransport,
};
function unconfiguredMobileError() {
    return new Error('The Capacitor ServerTransport has not been installed');
}
const unconfiguredMobileTransport = {
    request(_path, _options) {
        return Promise.reject(unconfiguredMobileError());
    },
    openEventStream(_path) {
        throw unconfiguredMobileError();
    },
    acquireResource(_path, _options) {
        return Promise.reject(unconfiguredMobileError());
    },
    logout() {
        return Promise.reject(unconfiguredMobileError());
    },
};
const unconfiguredMobileRuntime = {
    kind: 'capacitor',
    capability: emptyCapability,
    assetOrigin: currentOrigin,
    serverOrigin: () => {
        throw unconfiguredMobileError();
    },
    publicLinkOrigin: () => {
        throw unconfiguredMobileError();
    },
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: () => Promise.reject(unconfiguredMobileError()),
    transport: () => unconfiguredMobileTransport,
};
function runtimeKindFromDocument() {
    const marker = globalThis.document?.querySelector?.(`meta[name="${RUNTIME_META_NAME}"]`);
    return marker?.getAttribute('content') === 'capacitor' ? 'capacitor' : 'browser';
}
let installedRuntime = null;
export function getAppRuntime() {
    if (installedRuntime)
        return installedRuntime;
    return runtimeKindFromDocument() === 'capacitor' ? unconfiguredMobileRuntime : browserRuntime;
}
/** Native bootstrap installs its runtime before product networking starts. */
export function installAppRuntime(runtime) {
    installedRuntime = runtime;
}
/** Test-only reset; production code must not switch runtimes after startup. */
export function resetAppRuntimeForTests() {
    installedRuntime = null;
}
export function getBrowserRuntimeForTests() {
    return browserRuntime;
}
