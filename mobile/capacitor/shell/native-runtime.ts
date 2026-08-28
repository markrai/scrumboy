import type { AppRuntime } from '../../../internal/httpapi/web/modules/platform/runtime.js';
import type { ServerTransport } from '../../../internal/httpapi/web/modules/platform/server-transport.js';

type RuntimeModule = { installAppRuntime(runtime: AppRuntime): void };
type ModuleImporter = (path: string) => Promise<unknown>;

const DEVICE_GLOBAL_KEYS = new Set([
  'scrumboy.locale',
  'scrumboy_theme',
  'scrumboy.keybindings',
  'scrumboy_assignment_notify_muted',
]);

export function clearScrumboyWebState(): void {
  try {
    const remove: string[] = [];
    for (let i = 0; i < localStorage.length; i += 1) {
      const key = localStorage.key(i);
      if (key && !DEVICE_GLOBAL_KEYS.has(key)) remove.push(key);
    }
    for (const key of remove) localStorage.removeItem(key);
  } catch {
    // Native session cleanup remains authoritative if WebView storage is unavailable.
  }
  try {
    sessionStorage.clear();
  } catch {
    // Ignore unavailable WebView storage.
  }
}

export function createCapacitorRuntime(origin: string, transport: ServerTransport): AppRuntime {
  return {
    kind: 'capacitor',
    assetOrigin: () => globalThis.location?.origin || '',
    serverOrigin: () => origin,
    publicLinkOrigin: () => origin,
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    transport: () => transport,
  };
}

export async function installRuntimeAndStartProduct(
  origin: string,
  transport: ServerTransport,
  importer: ModuleImporter = (path) => import(path),
): Promise<void> {
  const runtimeModule = await importer('/dist/platform/runtime.js') as RuntimeModule;
  runtimeModule.installAppRuntime(createCapacitorRuntime(origin, transport));
  await importer('/app.js');
}
