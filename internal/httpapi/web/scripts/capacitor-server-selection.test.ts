// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CHANGE_SERVER_EVENT,
  SELECTED_SERVER_KEY,
  startMobileBootstrap,
  type BootstrapDependencies,
} from '../../../../mobile/capacitor/shell/bootstrap-core.js';
import type { ScrumboyTransportPlugin } from '../../../../mobile/capacitor/shell/native-plugin.js';
import {
  createClientCapabilityRegistry,
  type AppCapabilityMap,
} from '../modules/platform/client-capabilities.js';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  type LocalTextGenerationCapability,
} from '../modules/platform/local-text-generation.js';
import type { AppRuntime } from '../modules/platform/runtime.js';
import { renderServerSelector } from '../../../../mobile/capacitor/shell/server-selection.js';

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
  capabilities?: BootstrapDependencies['capabilities'];
  invalidateCapabilities?: BootstrapDependencies['invalidateCapabilities'];
  order?: string[];
  onAppImport?: () => void;
  onRuntime?: (runtime: AppRuntime) => void;
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
  const oidc = {
    installURLCapture: vi.fn(async () => undefined),
    configure: vi.fn(async () => undefined),
    start: vi.fn(async () => undefined),
    clearPending: vi.fn(async () => { order.push('oidc-clear'); }),
    markProductReady: vi.fn(),
  };
  const importer = vi.fn(async (path: string) => {
    order.push(`import:${path}`);
    if (path === '/dist/platform/runtime.js') {
      return { installAppRuntime: vi.fn((runtime) => {
        order.push('runtime-installed');
        options.onRuntime?.(runtime);
      }) };
    }
    if (path === '/app.js') options.onAppImport?.();
    return {};
  });
  return {
    capabilities: options.capabilities || { get: () => null },
    invalidateCapabilities: options.invalidateCapabilities || vi.fn(async () => {
      order.push('capabilities-invalidated');
    }),
    preferences,
    plugin,
    oidc,
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
  document.documentElement.removeAttribute('data-theme');
  document.body.innerHTML = [
    '<div id="app"></div>',
    '<dialog id="todoDialog"></dialog>',
    '<dialog id="settingsDialog"></dialog>',
    '<div id="toast" class="toast" role="status"></div>',
  ].join('');
  localStorage.clear();
  sessionStorage.clear();
  vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })));
});

describe('C2 server selection bootstrap', () => {
  it.each([
    ['entry', { kind: 'entry' } as const],
    ['saved-unreachable', {
      kind: 'saved-unreachable',
      origin: 'https://offline.example',
      failure: { code: 'connect_failure' },
    } as const],
  ])('renders the %s selector only inside the authoritative product mount', (_name, mode) => {
    const body = document.body;
    const toast = document.getElementById('toast');
    const todoDialog = document.getElementById('todoDialog');
    const replaceBodyChildren = vi.spyOn(body, 'replaceChildren');

    renderServerSelector(mode, { connect: vi.fn(async () => undefined) });

    expect(document.body).toBe(body);
    expect(replaceBodyChildren).not.toHaveBeenCalled();
    expect(document.querySelector('#app > #scrumboy-mobile-bootstrap')).toBeInstanceOf(HTMLElement);
    expect(document.getElementById('toast')).toBe(toast);
    expect(document.getElementById('todoDialog')).toBe(todoDialog);
    expect(toast?.isConnected).toBe(true);
    expect(todoDialog?.isConnected).toBe(true);
  });

  it('renders the polished entry presentation and mobile URL safeguards', () => {
    renderServerSelector({ kind: 'entry' }, { connect: vi.fn(async () => undefined) });

    const shell = document.getElementById('scrumboy-mobile-bootstrap');
    const wordmark = shell?.querySelector<HTMLImageElement>('img.brand-text');
    const title = document.getElementById('scrumboy-mobile-bootstrap-title');
    const input = document.getElementById('scrumboy-mobile-server-origin') as HTMLInputElement;
    const label = shell?.querySelector<HTMLLabelElement>('label[for="scrumboy-mobile-server-origin"]');
    const connect = shell?.querySelector<HTMLButtonElement>('button[type="submit"]');
    const warning = document.getElementById('scrumboy-mobile-insecure-http') as HTMLParagraphElement;

    expect(shell?.classList.contains('page--mobile-bootstrap')).toBe(true);
    expect(shell?.querySelector('.container .brand')).toBeInstanceOf(HTMLElement);
    expect(shell?.querySelector('.panel .panel__header')).toBeInstanceOf(HTMLElement);
    expect(wordmark?.getAttribute('src')).toBe('/scrumboytext.png');
    expect(wordmark?.alt).toBe('Scrumboy');
    expect(title?.textContent).toBe('Connect to your server');
    expect(shell?.textContent).toContain('Enter the address of your Scrumboy instance. You’ll sign in next.');
    expect(label?.textContent).toBe('Server address');
    expect(label?.classList.contains('field__label')).toBe(true);
    expect(input.type).toBe('url');
    expect(input.inputMode).toBe('url');
    expect(input.autocomplete).toBe('url');
    expect(input.required).toBe(true);
    expect(input.placeholder).toBe('https://scrumboy.example.com');
    expect(input.getAttribute('autocapitalize')).toBe('none');
    expect(input.getAttribute('autocorrect')).toBe('off');
    expect(input.getAttribute('spellcheck')).toBe('false');
    expect(input.getAttribute('enterkeyhint')).toBe('go');
    expect(connect?.classList.contains('btn')).toBe(true);
    expect(connect?.classList.contains('mobile-bootstrap__action')).toBe(true);
    expect(shell?.textContent).toContain('This address is stored only on this device.');
    expect(warning.hidden).toBe(true);

    input.value = 'http://debug.example';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(warning.hidden).toBe(false);
    expect(warning.textContent).toContain('HTTP is insecure');

    input.value = 'https://secure.example';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    expect(warning.hidden).toBe(true);
    expect(warning.textContent).toBe('');
  });

  it('renders the polished saved-unreachable presentation', () => {
    renderServerSelector(
      {
        kind: 'saved-unreachable',
        origin: 'https://offline.example',
        failure: { code: 'timeout' },
      },
      { connect: vi.fn(async () => undefined) },
    );

    const shell = document.getElementById('scrumboy-mobile-bootstrap');
    const buttons = [...shell!.querySelectorAll('button')];
    expect(shell?.querySelector<HTMLImageElement>('img.brand-text')?.alt).toBe('Scrumboy');
    expect(document.getElementById('scrumboy-mobile-bootstrap-title')?.textContent).toBe('Can’t reach your server');
    expect(shell?.querySelector('.mobile-bootstrap__origin')?.textContent).toBe('https://offline.example');
    expect(buttons.map((button) => button.textContent)).toEqual(['Try again', 'Change server']);
    expect([...buttons[0].classList]).toEqual(expect.arrayContaining(['btn', 'mobile-bootstrap__action']));
    expect([...buttons[1].classList]).toEqual(expect.arrayContaining(['btn', 'btn--ghost', 'mobile-bootstrap__action']));
    expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('did not respond in time');
  });

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

  it('injects the capability dependency before importing app.js', async () => {
    const localTextGeneration = {} as LocalTextGenerationCapability;
    const capabilities = createClientCapabilityRegistry<AppCapabilityMap>({
      [LOCAL_TEXT_GENERATION_CAPABILITY]: localTextGeneration,
    });
    let installedRuntime: AppRuntime | null = null;
    let capabilityAtAppImport: unknown = 'app-not-imported';
    const deps = dependencies({
      saved: 'https://saved.example',
      capabilities,
      onRuntime: (runtime) => { installedRuntime = runtime; },
      onAppImport: () => {
        capabilityAtAppImport = installedRuntime!.capability(LOCAL_TEXT_GENERATION_CAPABILITY);
      },
    });

    await startMobileBootstrap(deps);

    expect(capabilityAtAppImport).toBe(localTextGeneration);
  });

  it('shows Try again and Change server without importing app when a saved server fails', async () => {
    const plugin = pluginFake({ configure: vi.fn(async () => { throw new Error('offline'); }) });
    const deps = dependencies({ saved: 'https://offline.example', plugin });

    await startMobileBootstrap(deps);

    expect(document.body.textContent).toContain('https://offline.example');
    expect([...document.querySelectorAll('button')].map((button) => button.textContent)).toEqual(['Try again', 'Change server']);
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('shows a mapped safe reason for a coded saved-server startup failure', async () => {
    const plugin = pluginFake({ configure: vi.fn(async () => { throw { code: 'dns_failure' }; }) });
    const deps = dependencies({ saved: 'https://missing.example', plugin });

    await startMobileBootstrap(deps);

    expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toBe(
      'The server name could not be resolved.',
    );
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('updates the saved-server reason when retry observes a different coded failure', async () => {
    const plugin = pluginFake({
      configure: vi.fn()
        .mockRejectedValueOnce({ code: 'timeout' })
        .mockResolvedValueOnce(undefined),
      probeServer: vi.fn(async () => { throw { code: 'tls_failure' }; }),
    });
    const deps = dependencies({ saved: 'https://offline.example', plugin });

    await startMobileBootstrap(deps);
    expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('did not respond in time');
    [...document.querySelectorAll('button')].find((button) => button.textContent === 'Try again')?.click();

    await vi.waitFor(() => expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('certificate'));
    expect(deps.importer).not.toHaveBeenCalled();
  });

  it('prefills the saved origin when changing from saved-unreachable', async () => {
    const plugin = pluginFake({ configure: vi.fn(async () => { throw { code: 'connect_failure' }; }) });
    const deps = dependencies({ saved: 'https://offline.example:9443', plugin });

    await startMobileBootstrap(deps);
    [...document.querySelectorAll('button')].find((button) => button.textContent === 'Change server')?.click();

    expect((document.getElementById('scrumboy-mobile-server-origin') as HTMLInputElement).value).toBe(
      'https://offline.example:9443',
    );
    expect(deps.preferences.remove).not.toHaveBeenCalled();
  });

  it.each([
    ['light', 'light'],
    ['dark', null],
  ])('applies a stored %s theme before reading Preferences', async (stored, expected) => {
    localStorage.setItem('scrumboy_theme', stored);
    const deps = dependencies();
    deps.preferences.get = vi.fn(async () => {
      expect(document.documentElement.getAttribute('data-theme')).toBe(expected);
      return { value: null };
    });

    await startMobileBootstrap(deps);

    expect(document.documentElement.getAttribute('data-theme')).toBe(expected);
  });

  it.each([
    [false, 'light'],
    [true, null],
  ])('resolves a stored system theme when dark preference is %s', async (systemIsDark, expected) => {
    localStorage.setItem('scrumboy_theme', 'system');
    vi.mocked(globalThis.matchMedia).mockImplementation((query: string) => ({
      matches: systemIsDark,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));

    await startMobileBootstrap(dependencies());

    expect(document.documentElement.getAttribute('data-theme')).toBe(expected);
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

  it('preserves packaged DOM until app.js takes over the product mount on first connect', async () => {
    const order: string[] = [];
    const plugin = pluginFake({
      probeServer: vi.fn(async () => ({
        normalizedOrigin: 'https://vega.example:9446',
        version: '3.34.0',
        authStatus: { mode: 'full', user: null },
      })),
      configure: vi.fn(async () => { order.push('configure'); }),
    });
    const deps = dependencies({
      plugin,
      order,
      onAppImport: () => {
        expect(document.getElementById('toast')).toBeInstanceOf(HTMLElement);
        expect(document.getElementById('todoDialog')).toBeInstanceOf(HTMLDialogElement);
        const app = document.getElementById('app');
        expect(app).toBeInstanceOf(HTMLElement);
        const product = document.createElement('section');
        product.id = 'packaged-auth-ui';
        app?.replaceChildren(product);
      },
    });

    await startMobileBootstrap(deps);
    submitOrigin('https://vega.example:9446');

    await vi.waitFor(() => expect(document.getElementById('packaged-auth-ui')).toBeInstanceOf(HTMLElement));
    expect(document.getElementById('scrumboy-mobile-bootstrap')).toBeNull();
    expect(deps.preferences.set).toHaveBeenCalledWith({
      key: SELECTED_SERVER_KEY,
      value: 'https://vega.example:9446',
    });
    expect(plugin.configure).toHaveBeenCalledWith({
      origin: 'https://vega.example:9446',
      resetSession: false,
    });
    expect(order).toEqual([
      'persist:https://vega.example:9446',
      'configure',
      'import:/dist/platform/runtime.js',
      'runtime-installed',
      'import:/app.js',
    ]);
  });

  it('keeps the packaged DOM viable after a failed candidate and then starts in the same launch', async () => {
    const plugin = pluginFake({
      probeServer: vi.fn()
        .mockRejectedValueOnce({ code: 'connect_failure' })
        .mockResolvedValueOnce({
          normalizedOrigin: 'https://vega.example:9446',
          version: '3.34.0',
          authStatus: { mode: 'full', user: null },
        }),
    });
    const deps = dependencies({
      plugin,
      onAppImport: () => {
        expect(document.getElementById('toast')).toBeInstanceOf(HTMLElement);
        const product = document.createElement('section');
        product.id = 'packaged-auth-ui';
        document.getElementById('app')?.replaceChildren(product);
      },
    });

    await startMobileBootstrap(deps);
    submitOrigin('https://offline.example');
    await vi.waitFor(() => expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('Could not connect'));

    submitOrigin('https://vega.example:9446');
    await vi.waitFor(() => expect(document.getElementById('packaged-auth-ui')).toBeInstanceOf(HTMLElement));

    expect(document.getElementById('scrumboy-mobile-bootstrap')).toBeNull();
    expect(document.getElementById('todoDialog')).toBeInstanceOf(HTMLDialogElement);
    expect(deps.importer).toHaveBeenCalledWith('/app.js');
  });

  it('preserves packaged DOM while a saved-unreachable retry starts the product in place', async () => {
    const plugin = pluginFake({
      configure: vi.fn()
        .mockRejectedValueOnce(new Error('offline'))
        .mockResolvedValueOnce(undefined),
    });
    const deps = dependencies({
      saved: 'https://vega.example:9446',
      plugin,
      onAppImport: () => {
        expect(document.getElementById('toast')).toBeInstanceOf(HTMLElement);
        expect(document.getElementById('settingsDialog')).toBeInstanceOf(HTMLDialogElement);
        const product = document.createElement('section');
        product.id = 'packaged-auth-ui';
        document.getElementById('app')?.replaceChildren(product);
      },
    });

    await startMobileBootstrap(deps);
    [...document.querySelectorAll('button')].find((button) => button.textContent === 'Try again')?.click();

    await vi.waitFor(() => expect(document.getElementById('packaged-auth-ui')).toBeInstanceOf(HTMLElement));
    expect(document.getElementById('scrumboy-mobile-bootstrap')).toBeNull();
    expect(document.getElementById('toast')).toBeInstanceOf(HTMLElement);
    expect(deps.importer).toHaveBeenCalledWith('/app.js');
  });

  it('does not persist or import after a candidate probe failure', async () => {
    const plugin = pluginFake({ probeServer: vi.fn(async () => { throw { code: 'tls_failure' }; }) });
    const deps = dependencies({ plugin });
    await startMobileBootstrap(deps);

    submitOrigin('https://bad.example');
    await vi.waitFor(() => expect(document.getElementById('scrumboy-mobile-server-status')?.textContent).toContain('certificate'));

    expect((document.getElementById('scrumboy-mobile-server-origin') as HTMLInputElement).value).toBe('https://bad.example');
    expect(document.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(false);
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

    expect(order.slice(-5)).toEqual([
      'capabilities-invalidated',
      'oidc-clear',
      'native-reset',
      'preference-removed',
      'reload',
    ]);
    expect(deps.preferences.remove).toHaveBeenCalledWith({ key: SELECTED_SERVER_KEY });
    expect(localStorage.getItem('private-user-state')).toBeNull();
    expect(localStorage.getItem('scrumboy.locale')).toBe('de');
  });

  it('clears pending OIDC proof before completing native logout cleanup', async () => {
    let runtime!: { transport(): { logout(): Promise<void> } };
    const deps = dependencies({
      saved: 'https://saved.example',
      onRuntime: (installed) => { runtime = installed; },
    });
    await startMobileBootstrap(deps);

    await runtime.transport().logout();

    expect(deps.plugin.logout).toHaveBeenCalledOnce();
    expect(deps.invalidateCapabilities).toHaveBeenCalledOnce();
    expect(deps.oidc.clearPending).toHaveBeenCalledOnce();
  });

  it('continues logout and server change when capability invalidation fails', async () => {
    let runtime!: { transport(): { logout(): Promise<void> } };
    const deps = dependencies({
      saved: 'https://saved.example',
      invalidateCapabilities: vi.fn(async () => { throw new Error('native cancellation failed'); }),
      onRuntime: (installed) => { runtime = installed; },
    });
    await startMobileBootstrap(deps);

    await expect(runtime.transport().logout()).resolves.toBeUndefined();
    expect(deps.oidc.clearPending).toHaveBeenCalledOnce();

    window.dispatchEvent(new CustomEvent(CHANGE_SERVER_EVENT));
    await vi.waitFor(() => expect(deps.reload).toHaveBeenCalledOnce());
    expect(deps.plugin.resetForServerChange).toHaveBeenCalledOnce();
  });

  it('fails closed outside the packaged Capacitor runtime', async () => {
    document.head.innerHTML = '';
    await expect(startMobileBootstrap(dependencies())).rejects.toThrow(
      'Refusing to start the mobile bootstrap outside the packaged Capacitor runtime',
    );
  });
});
