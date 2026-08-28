import { Preferences } from '@capacitor/preferences';
import { NativeServerTransport } from './native-server-transport.js';
import { clearScrumboyWebState, installRuntimeAndStartProduct } from './native-runtime.js';
import { ScrumboyTransport, type ScrumboyTransportPlugin } from './native-plugin.js';
import { renderServerSelector } from './server-selection.js';

export const SELECTED_SERVER_KEY = 'scrumboy.server.origin.v1';
export const CHANGE_SERVER_EVENT = 'scrumboy:mobile-change-server';

type PreferenceStore = Pick<typeof Preferences, 'get' | 'set' | 'remove'>;
type Importer = (path: string) => Promise<unknown>;

export interface BootstrapDependencies {
  preferences: PreferenceStore;
  plugin: ScrumboyTransportPlugin;
  importer: Importer;
  reload(): void;
  confirmChange(): boolean;
}

const defaults: BootstrapDependencies = {
  preferences: Preferences,
  plugin: ScrumboyTransport,
  importer: (path) => import(path),
  reload: () => globalThis.location?.reload(),
  confirmChange: () => globalThis.confirm('Change Scrumboy server? The current mobile session will be cleared.'),
};

let removeServerChangeHandler: (() => void) | null = null;

function assertPackagedRuntime(): void {
  const runtimeMarker = document.head.querySelector('meta[name="scrumboy-runtime"]');
  if (runtimeMarker?.getAttribute('content') !== 'capacitor') {
    throw new Error('Refusing to start the mobile bootstrap outside the packaged Capacitor runtime');
  }
}

async function startProduct(origin: string, deps: BootstrapDependencies): Promise<void> {
  const transport = new NativeServerTransport({ plugin: deps.plugin });
  await installRuntimeAndStartProduct(origin, transport, deps.importer);
  let serverChangeStarted = false;
  const handleServerChange = () => {
    if (serverChangeStarted || !deps.confirmChange()) return;
    serverChangeStarted = true;
    void (async () => {
      try {
        await deps.plugin.resetForServerChange();
        clearScrumboyWebState();
        await deps.preferences.remove({ key: SELECTED_SERVER_KEY });
        deps.reload();
      } catch {
        serverChangeStarted = false;
      }
    })();
  };
  removeServerChangeHandler?.();
  window.addEventListener(CHANGE_SERVER_EVENT, handleServerChange);
  removeServerChangeHandler = () => window.removeEventListener(CHANGE_SERVER_EVENT, handleServerChange);
}

async function connectCandidate(
  candidate: string,
  previousOrigin: string | null,
  deps: BootstrapDependencies,
): Promise<void> {
  const probe = await deps.plugin.probeServer({ origin: candidate });
  const changed = !!previousOrigin && previousOrigin !== probe.normalizedOrigin;
  try {
    await deps.preferences.set({ key: SELECTED_SERVER_KEY, value: probe.normalizedOrigin });
    await deps.plugin.configure({ origin: probe.normalizedOrigin, resetSession: changed });
  } catch (error) {
    if (previousOrigin) {
      await deps.preferences.set({ key: SELECTED_SERVER_KEY, value: previousOrigin }).catch(() => undefined);
    } else {
      await deps.preferences.remove({ key: SELECTED_SERVER_KEY }).catch(() => undefined);
    }
    throw error;
  }
  await startProduct(probe.normalizedOrigin, deps);
}

function showEntry(previousOrigin: string | null, deps: BootstrapDependencies): void {
  renderServerSelector(
    { kind: 'entry', initialOrigin: previousOrigin || undefined },
    { connect: (candidate) => connectCandidate(candidate, previousOrigin, deps) },
  );
}

function showSavedUnavailable(origin: string, deps: BootstrapDependencies): void {
  renderServerSelector(
    { kind: 'saved-unreachable', origin, message: 'Could not connect.' },
    {
      retry: async () => {
        try {
          await deps.plugin.configure({ origin, resetSession: false });
          const probe = await deps.plugin.probeServer({ origin });
          await startProduct(probe.normalizedOrigin, deps);
        } catch {
          showSavedUnavailable(origin, deps);
        }
      },
      change: () => showEntry(origin, deps),
      connect: async () => undefined,
    },
  );
}

export async function startMobileBootstrap(overrides: Partial<BootstrapDependencies> = {}): Promise<void> {
  assertPackagedRuntime();
  const deps = { ...defaults, ...overrides };
  const saved = (await deps.preferences.get({ key: SELECTED_SERVER_KEY })).value?.trim() || null;
  if (!saved) {
    showEntry(null, deps);
    return;
  }
  try {
    await deps.plugin.configure({ origin: saved, resetSession: false });
    const probe = await deps.plugin.probeServer({ origin: saved });
    await startProduct(probe.normalizedOrigin, deps);
  } catch {
    showSavedUnavailable(saved, deps);
  }
}
