// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CHANGE_SERVER_EVENT,
  SELECTED_SERVER_KEY,
  startMobileBootstrap,
  type BootstrapDependencies,
} from '../../../../mobile/capacitor/shell/bootstrap-core.js';
import type { ScrumboyTransportPlugin } from '../../../../mobile/capacitor/shell/native-plugin.js';

function pluginFake(overrides: Partial<ScrumboyTransportPlugin> = {}): ScrumboyTransportPlugin {
  return {
    probeServer: vi.fn(async ({ origin }) => ({
      normalizedOrigin: origin.replace(/\/$/, ''),
      version: 'test',
      authStatus: { mode: 'full', user: null, bootstrapAvailable: false },
    })),
    configure: vi.fn(async () => undefined),
    request: vi.fn(),
    cancelRequest: vi.fn(async () => undefined),
    openEventStream: vi.fn(async () => undefined),
    closeEventStream: vi.fn(async () => undefined),
    acquireResource: vi.fn(),
    releaseResource: vi.fn(async () => undefined),
    logout: vi.fn(async () => undefined),
    resetForServerChange: vi.fn(async () => undefined),
    addListener: vi.fn(async () => ({ remove: vi.fn(async () => undefined) })),
    ...overrides,
  };
}

function dependencies(options: {
  saved?: string | null;
  plugin?: ScrumboyTransportPlugin;
  order?: string[];
} = {}): BootstrapDependencies & {
  preferences: BootstrapDependencies['preferences'] & {
    get: ReturnType<typeof vi.fn>;
    set: ReturnType<typeof vi.fn>;
    remove: ReturnType<typeof vi.fn>;
  };
  reload: ReturnType<typeof vi.fn>;
} {
  const order = options.order || [];
  const preferences = {
    get: vi.fn(async () => ({ value: options.saved ?? null })),
    set: vi.fn(async ({ value }: { key: string; value: string }) => { order.push(`persist:${value}`); }),
    remove: vi.fn(async () => { order.push('preference-removed'); }),
  };
  const plugin = options.plugin || pluginFake();
  const importer = vi.fn(async (path: string) => {
    order.push(`import:${path}`);
    if (path === '/dist/platform/runtime.js') {
      return { installAppRuntime: vi.fn(() => order.push('runtime-installed')) };
    }
    return {};
  });
  return {
    preferences,
    plugin,
    importer,
    reload: vi.fn(() => order.push('reload')),
    confirmChange: vi.fn(() => true),
  };
}

function submitOrigin(origin: string): void {
  const input = document.getElementById('scrumboy-mobile-server-origin') as HTMLInputElement;
  input.value = origin;
  document.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

beforeEach(() => {
  document.head.innerHTML = '<meta name="scrumboy-runtime" content="capacitor">';
  document.body.innerHTML = '<div id="app"></div>';
  localStorage.clear();
  sessionStorage.clear();
});

describe('C2 server selection bootstrap', () => {
  it('shows the selector when no server is saved', async () => {
    const deps = dependencies();

    await startMobileBootstrap(deps);

    expect(document.getElementById('scrumboy-mobile-server-origin')).toBeInstanceOf(HTMLInputElement);
    expect(document.body.textContent).toContain('Scrumboy');
    expect(deps.plugin.configure).not.toHaveBeenCalled();
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('configures and probes a saved server before runtime installation and app import', async () => {
    const order: string[] = [];
    const plugin = pluginFake({
      configure: vi.fn(async () => { order.push('configure'); }),
      probeServer: vi.fn(async ({ origin }) => {
        order.push('probe');
        return { normalizedOrigin: origin, version: 'test', authStatus: { mode: 'full' } };
      }),
    });
    const deps = dependencies({ saved: 'https://saved.example', plugin, order });

    await startMobileBootstrap(deps);

    expect(order).toEqual([
      'configure',
      'probe',
      'import:/dist/platform/runtime.js',
      'runtime-installed',
      'import:/app.js',
    ]);
    expect(plugin.configure).toHaveBeenCalledWith({ origin: 'https://saved.example', resetSession: false });
  });

  it('shows Retry and Change server without importing app when a saved server fails', async () => {
    const plugin = pluginFake({ configure: vi.fn(async () => { throw new Error('offline'); }) });
    const deps = dependencies({ saved: 'https://offline.example', plugin });

    await startMobileBootstrap(deps);

    expect(document.body.textContent).toContain('https://offline.example');
    expect([...document.querySelectorAll('button')].map((button) => button.textContent)).toEqual(['Retry', 'Change server']);
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('persists a normalized candidate before configuring, then installs runtime and imports app', async () => {
    const order: string[] = [];
    const plugin = pluginFake({
      probeServer: vi.fn(async () => {
        order.push('probe');
        return {
          normalizedOrigin: 'https://selected.example',
          version: 'test',
          authStatus: { mode: 'full', user: null },
        };
      }),
      configure: vi.fn(async () => { order.push('configure'); }),
    });
    const deps = dependencies({ plugin, order });
    await startMobileBootstrap(deps);

    submitOrigin('https://SELECTED.example/');
    await vi.waitFor(() => expect(deps.importer).toHaveBeenCalledWith('/app.js'));

    expect(order).toEqual([
      'probe',
      'persist:https://selected.example',
      'configure',
      'import:/dist/platform/runtime.js',
      'runtime-installed',
      'import:/app.js',
    ]);
    expect(deps.preferences.set).toHaveBeenCalledWith({
      key: SELECTED_SERVER_KEY,
      value: 'https://selected.example',
    });
  });

  it('does not persist or import after a candidate probe failure', async () => {
    const plugin = pluginFake({ probeServer: vi.fn(async () => { throw { code: 'tls_failure' }; }) });
    const deps = dependencies({ plugin });
    await startMobileBootstrap(deps);

    submitOrigin('https://bad.example');
    await vi.waitFor(() => expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('certificate'));

    expect(deps.preferences.set).not.toHaveBeenCalled();
    expect(plugin.configure).not.toHaveBeenCalled();
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('rolls Preferences back if native configure fails after persistence', async () => {
    const plugin = pluginFake({
      configure: vi.fn()
        .mockRejectedValueOnce(new Error('saved server offline'))
        .mockRejectedValueOnce(new Error('native configure failed')),
    });
    const deps = dependencies({ saved: 'https://old.example', plugin });
    await startMobileBootstrap(deps);
    [...document.querySelectorAll('button')].find((button) => button.textContent === 'Change server')?.click();

    submitOrigin('https://new.example');
    await vi.waitFor(() => expect(deps.preferences.set).toHaveBeenLastCalledWith({
      key: SELECTED_SERVER_KEY,
      value: 'https://old.example',
    }));
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('changes server by clearing native session and scoped web state before removing selection and reload', async () => {
    const order: string[] = [];
    const plugin = pluginFake({
      resetForServerChange: vi.fn(async () => { order.push('native-reset'); }),
    });
    const deps = dependencies({ saved: 'https://saved.example', plugin, order });
    localStorage.setItem('scrumboy.locale', 'de');
    localStorage.setItem('private-user-state', 'secret');
    await startMobileBootstrap(deps);

    window.dispatchEvent(new CustomEvent(CHANGE_SERVER_EVENT));
    await vi.waitFor(() => expect(deps.reload).toHaveBeenCalledOnce());

    expect(order.slice(-3)).toEqual(['native-reset', 'preference-removed', 'reload']);
    expect(deps.preferences.remove).toHaveBeenCalledWith({ key: SELECTED_SERVER_KEY });
    expect(localStorage.getItem('private-user-state')).toBeNull();
    expect(localStorage.getItem('scrumboy.locale')).toBe('de');
  });

  it('fails closed outside the packaged Capacitor runtime', async () => {
    document.head.innerHTML = '';
    await expect(startMobileBootstrap(dependencies())).rejects.toThrow(
      'Refusing to start the mobile bootstrap outside the packaged Capacitor runtime',
    );
  });
});
