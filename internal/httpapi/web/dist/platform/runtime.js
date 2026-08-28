import { BrowserServerTransport } from './browser-server-transport.js';
const RUNTIME_META_NAME = 'scrumboy-runtime';
function currentOrigin() {
    return globalThis.location?.origin || '';
}
const browserTransport = new BrowserServerTransport();
const browserRuntime = {
    kind: 'browser',
    assetOrigin: currentOrigin,
    serverOrigin: currentOrigin,
    publicLinkOrigin: currentOrigin,
    supportsPWA: () => true,
    supportsWebPush: () => true,
    supportsInteractiveOIDC: () => true,
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
/** Future native bootstrap installs its runtime before product networking starts. */
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
