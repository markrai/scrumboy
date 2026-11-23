// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from '../api.js';
import { maybeAutoSubscribePushAfterLogin, subscribeToPush } from './push.js';
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from '../platform/runtime.js';

vi.mock('../api.js', () => ({ apiFetch: vi.fn() }));

const persist = vi.mocked(apiFetch);

function setupPushBrowser(existing: boolean) {
  const subscription = {
    endpoint: 'https://push.example.com/private',
    toJSON: () => ({
      endpoint: 'https://push.example.com/private',
      keys: { p256dh: 'public-key', auth: 'auth-secret' },
    }),
    unsubscribe: vi.fn(async () => true),
  };
  const getSubscription = vi.fn(async () => existing ? subscription : null);
  const subscribe = vi.fn(async () => subscription);
  const request = vi.fn(async () => ({
    ok: true,
    json: async () => ({ publicKey: 'AQID' }),
  }));
  installAppRuntime({
    kind: 'browser',
    supportsWebPush: () => true,
    transport: () => ({ request }),
  } as unknown as AppRuntime);
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { getRegistration: vi.fn(async () => ({ pushManager: { getSubscription, subscribe } })) },
  });
  vi.stubGlobal('PushManager', class PushManager {});
  vi.stubGlobal('Notification', { permission: 'granted', requestPermission: vi.fn() });
  return { subscription, getSubscription, subscribe, request };
}

beforeEach(() => {
  localStorage.clear();
  persist.mockReset();
});

afterEach(() => {
  localStorage.clear();
  resetAppRuntimeForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Web Push subscription persistence', () => {
  it('reposts an existing subscription even with a completed autosubscribe state', async () => {
    const browser = setupPushBrowser(true);
    persist.mockResolvedValue(null);
    localStorage.setItem('scrumboy_push_autosub_v1_u42', 'done');

    await maybeAutoSubscribePushAfterLogin(42);

    expect(persist).toHaveBeenCalledTimes(1);
    expect(browser.subscribe).not.toHaveBeenCalled();
    expect(browser.subscription.unsubscribe).not.toHaveBeenCalled();
  });

  it('synchronizes an existing subscription even after a previous denied outcome', async () => {
    const browser = setupPushBrowser(true);
    localStorage.setItem('scrumboy_push_autosub_v1_u42', 'denied');
    persist.mockResolvedValue(null);

    await maybeAutoSubscribePushAfterLogin(42);

    expect(persist).toHaveBeenCalledTimes(1);
    expect(browser.subscribe).not.toHaveBeenCalled();
    expect(localStorage.getItem('scrumboy_push_autosub_v1_u42')).toBe('done');
  });

  it('does not prompt while browser notification permission remains denied', async () => {
    const browser = setupPushBrowser(false);
    localStorage.setItem('scrumboy_push_autosub_v1_u42', 'denied');
    vi.stubGlobal('Notification', { permission: 'denied', requestPermission: vi.fn() });

    await maybeAutoSubscribePushAfterLogin(42);

    expect(browser.subscribe).not.toHaveBeenCalled();
    expect(persist).not.toHaveBeenCalled();
    expect(Notification.requestPermission).not.toHaveBeenCalled();
  });

  it('can create a subscription without prompting when permission is restored', async () => {
    const browser = setupPushBrowser(false);
    localStorage.setItem('scrumboy_push_autosub_v1_u42', 'denied');
    persist.mockResolvedValue(null);

    await maybeAutoSubscribePushAfterLogin(42);

    expect(browser.subscribe).toHaveBeenCalledTimes(1);
    expect(persist).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem('scrumboy_push_autosub_v1_u42')).toBe('done');
  });

  it('unsubscribes a newly created browser subscription when registration fails', async () => {
    const browser = setupPushBrowser(false);
    persist.mockRejectedValue(new Error('registration unavailable'));

    await expect(subscribeToPush()).rejects.toThrow('registration unavailable');

    expect(browser.subscribe).toHaveBeenCalledTimes(1);
    expect(browser.subscription.unsubscribe).toHaveBeenCalledTimes(1);
  });

  it('preserves a pre-existing browser subscription on transient registration failure', async () => {
    const browser = setupPushBrowser(true);
    persist.mockRejectedValue(new Error('registration unavailable'));

    await expect(subscribeToPush()).rejects.toThrow('registration unavailable');

    expect(browser.subscribe).not.toHaveBeenCalled();
    expect(browser.subscription.unsubscribe).not.toHaveBeenCalled();
  });
});
