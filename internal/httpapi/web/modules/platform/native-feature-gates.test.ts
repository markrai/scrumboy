// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getDesktopNotificationStatusKind,
  requestDesktopNotificationPermission,
  showAssignmentDesktopNotification,
} from '../core/assignmentNotify.js';
import {
  isPushSubscribed,
  maybeAutoSubscribePushAfterLogin,
  subscribeToPush,
} from '../core/push.js';
import { registerPwaGlobals } from '../pwaUpdate.js';
import {
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from './runtime.js';
import type { ServerTransport } from './server-transport.js';

afterEach(() => {
  resetAppRuntimeForTests();
  delete (window as any).reloadForUpdate;
  delete (window as any).dismissUpdateNotification;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('future native runtime browser-feature gates', () => {
  it('does not register PWA, use browser Web Push, or use Notification', async () => {
    const request = vi.fn();
    const transport: ServerTransport = {
      request,
      openEventStream: vi.fn(),
      acquireResource: vi.fn(async (path: string) => ({ url: path, release: vi.fn() })),
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
    const register = vi.fn();
    Object.defineProperty(navigator, 'serviceWorker', {
      configurable: true,
      value: { register, getRegistration: vi.fn() },
    });
    vi.stubGlobal('PushManager', class PushManager {});
    const NotificationMock = vi.fn();
    Object.defineProperty(NotificationMock, 'permission', { configurable: true, value: 'granted' });
    Object.defineProperty(NotificationMock, 'requestPermission', { configurable: true, value: vi.fn() });
    vi.stubGlobal('Notification', NotificationMock);

    registerPwaGlobals();
    window.dispatchEvent(new Event('load'));
    await maybeAutoSubscribePushAfterLogin(1);

    await expect(isPushSubscribed()).resolves.toBe(false);
    await expect(subscribeToPush()).resolves.toBe(false);
    expect(getDesktopNotificationStatusKind()).toBe('unsupported');
    await expect(requestDesktopNotificationPermission()).resolves.toBe('denied');
    showAssignmentDesktopNotification('native-gated');

    expect(register).not.toHaveBeenCalled();
    expect(request).not.toHaveBeenCalled();
    expect(NotificationMock).not.toHaveBeenCalled();
    expect((NotificationMock as any).requestPermission).not.toHaveBeenCalled();
    expect((window as any).reloadForUpdate).toBeUndefined();
    expect((window as any).dismissUpdateNotification).toBeUndefined();
  });
});
