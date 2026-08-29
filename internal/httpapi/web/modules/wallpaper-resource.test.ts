// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  applyWallpaperForAuthContext,
  applyWallpaperState,
  type WallpaperState,
} from './wallpaper.js';
import {
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from './platform/runtime.js';
import type {
  AcquiredServerResource,
  ServerResourceOptions,
  ServerTransport,
} from './platform/server-transport.js';

type Deferred<T> = {
  promise: Promise<T>;
  resolve(value: T): void;
  reject(reason?: unknown): void;
};

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function resource(url: string) {
  return {
    value: { url, release: vi.fn() } satisfies AcquiredServerResource,
  };
}

function installFakeMobile(
  acquireResource: (path: string, options?: ServerResourceOptions) => Promise<AcquiredServerResource>,
): ServerTransport {
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream: vi.fn(),
    acquireResource: vi.fn(acquireResource),
    logout: vi.fn().mockResolvedValue(undefined),
  };
  const runtime: AppRuntime = {
    kind: 'capacitor',
    assetOrigin: () => 'https://localhost',
    serverOrigin: () => 'https://server.example',
    publicLinkOrigin: () => 'https://server.example',
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: vi.fn(async () => undefined),
    transport: () => transport,
  };
  installAppRuntime(runtime);
  return transport;
}

function imageState(rev: number): WallpaperState {
  return { v: 1, mode: 'image', rev };
}

function wallpaperImageElement(): HTMLElement {
  const element = document.querySelector('.wallpaper-shell__image');
  if (!(element instanceof HTMLElement)) throw new Error('wallpaper image element was not created');
  return element;
}

async function flushAcquisition(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

afterEach(() => {
  applyWallpaperState({ v: 1, mode: 'off' });
  resetAppRuntimeForTests();
  document.body.innerHTML = '';
  document.documentElement.removeAttribute('data-wallpaper-active');
  document.documentElement.removeAttribute('data-wallpaper-source');
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('authenticated wallpaper resource ownership', () => {
  it('waits for delayed mobile acquisition before displaying its local resource URL', async () => {
    const acquisition = deferred<AcquiredServerResource>();
    const acquired = resource('blob:fake-resource-1');
    const transport = installFakeMobile(() => acquisition.promise);

    applyWallpaperState(imageState(1));

    expect(transport.acquireResource).toHaveBeenCalledWith(
      '/api/user/wallpaper/image?rev=1',
      { signal: expect.any(AbortSignal) },
    );
    expect(wallpaperImageElement().style.backgroundImage).toBe('');

    acquisition.resolve(acquired.value);
    await flushAcquisition();

    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:fake-resource-1');
    expect(acquired.value.release).not.toHaveBeenCalled();
  });

  it('installs a replacement before releasing the previously displayed resource', async () => {
    const acquisitionA = deferred<AcquiredServerResource>();
    const acquisitionB = deferred<AcquiredServerResource>();
    const acquiredA = resource('blob:resource-a');
    const acquiredB = resource('blob:resource-b');
    installFakeMobile(vi.fn()
      .mockReturnValueOnce(acquisitionA.promise)
      .mockReturnValueOnce(acquisitionB.promise));

    applyWallpaperState(imageState(1));
    acquisitionA.resolve(acquiredA.value);
    await flushAcquisition();
    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-a');

    acquiredA.value.release.mockImplementation(() => {
      expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-b');
    });
    applyWallpaperState(imageState(2));
    acquisitionB.resolve(acquiredB.value);
    await flushAcquisition();

    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-b');
    expect(acquiredA.value.release).toHaveBeenCalledOnce();
    expect(acquiredB.value.release).not.toHaveBeenCalled();
  });

  it.each([
    ['off', { v: 1, mode: 'off' } satisfies WallpaperState],
    ['color', { v: 1, mode: 'color', hex: '#123456' } satisfies WallpaperState],
    ['builtin', { v: 1, mode: 'builtin' } satisfies WallpaperState],
  ])('releases a displayed resource once when changing to %s mode', async (_name, nextState) => {
    const acquired = resource('blob:resource-a');
    installFakeMobile(async () => acquired.value);
    applyWallpaperState(imageState(1));
    await flushAcquisition();

    applyWallpaperState(nextState);

    expect(wallpaperImageElement().style.backgroundImage).not.toContain('blob:resource-a');
    expect(acquired.value.release).toHaveBeenCalledOnce();
  });

  it('releases a displayed resource when the runtime enters anonymous mode', async () => {
    const acquired = resource('blob:resource-a');
    installFakeMobile(async () => acquired.value);
    applyWallpaperState(imageState(1));
    await flushAcquisition();

    applyWallpaperForAuthContext(false);

    expect(wallpaperImageElement().style.backgroundImage).toBe('');
    expect(acquired.value.release).toHaveBeenCalledOnce();
  });

  it('keeps B displayed and releases late stale A when B resolves first', async () => {
    const acquisitionA = deferred<AcquiredServerResource>();
    const acquisitionB = deferred<AcquiredServerResource>();
    const acquiredA = resource('blob:resource-a');
    const acquiredB = resource('blob:resource-b');
    installFakeMobile(vi.fn()
      .mockReturnValueOnce(acquisitionA.promise)
      .mockReturnValueOnce(acquisitionB.promise));

    applyWallpaperState(imageState(1));
    applyWallpaperState(imageState(2));
    acquisitionB.resolve(acquiredB.value);
    await flushAcquisition();
    acquisitionA.resolve(acquiredA.value);
    await flushAcquisition();

    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-b');
    expect(wallpaperImageElement().style.backgroundImage).not.toContain('blob:resource-a');
    expect(acquiredA.value.release).toHaveBeenCalledOnce();
    expect(acquiredB.value.release).not.toHaveBeenCalled();
  });

  it('releases stale A when it resolves before the current B acquisition', async () => {
    const acquisitionA = deferred<AcquiredServerResource>();
    const acquisitionB = deferred<AcquiredServerResource>();
    const acquiredA = resource('blob:resource-a');
    const acquiredB = resource('blob:resource-b');
    installFakeMobile(vi.fn()
      .mockReturnValueOnce(acquisitionA.promise)
      .mockReturnValueOnce(acquisitionB.promise));

    applyWallpaperState(imageState(1));
    applyWallpaperState(imageState(2));
    acquisitionA.resolve(acquiredA.value);
    await flushAcquisition();
    expect(wallpaperImageElement().style.backgroundImage).not.toContain('blob:resource-a');
    expect(acquiredA.value.release).toHaveBeenCalledOnce();

    acquisitionB.resolve(acquiredB.value);
    await flushAcquisition();
    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-b');
    expect(acquiredB.value.release).not.toHaveBeenCalled();
  });

  it('keeps the displayed resource when its replacement acquisition fails', async () => {
    const acquisitionB = deferred<AcquiredServerResource>();
    const acquiredA = resource('blob:resource-a');
    installFakeMobile(vi.fn()
      .mockResolvedValueOnce(acquiredA.value)
      .mockReturnValueOnce(acquisitionB.promise));
    applyWallpaperState(imageState(1));
    await flushAcquisition();

    applyWallpaperState(imageState(2));
    acquisitionB.reject(new Error('native acquisition failed'));
    await flushAcquisition();

    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-a');
    expect(acquiredA.value.release).not.toHaveBeenCalled();
  });

  it('aborts a superseded acquisition and releases its resource if it resolves late', async () => {
    const acquisitionA = deferred<AcquiredServerResource>();
    const acquisitionB = deferred<AcquiredServerResource>();
    const acquiredA = resource('blob:resource-a');
    const acquiredB = resource('blob:resource-b');
    const signals: AbortSignal[] = [];
    installFakeMobile((_path, options) => {
      if (options?.signal) signals.push(options.signal);
      return signals.length === 1 ? acquisitionA.promise : acquisitionB.promise;
    });

    applyWallpaperState(imageState(1));
    applyWallpaperState(imageState(2));

    expect(signals).toHaveLength(2);
    expect(signals[0].aborted).toBe(true);
    expect(signals[1].aborted).toBe(false);

    acquisitionA.resolve(acquiredA.value);
    acquisitionB.resolve(acquiredB.value);
    await flushAcquisition();

    expect(wallpaperImageElement().style.backgroundImage).toContain('blob:resource-b');
    expect(acquiredA.value.release).toHaveBeenCalledOnce();
    expect(acquiredB.value.release).not.toHaveBeenCalled();
  });
});
