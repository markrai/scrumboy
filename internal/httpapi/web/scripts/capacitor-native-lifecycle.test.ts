// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AppState } from '@capacitor/app';
import {
  installNativeLifecycle,
  NATIVE_FOREGROUND_EVENT,
  removeNativeLifecycle,
  type NativeAppLifecycle,
} from '../../../../mobile/capacitor/shell/native-lifecycle.js';

function lifecycleFake() {
  const registrations: Array<{
    listener: (state: AppState) => void;
    removed: boolean;
    remove: ReturnType<typeof vi.fn>;
  }> = [];
  const removeAllListeners = vi.fn();
  const app: NativeAppLifecycle & { removeAllListeners: typeof removeAllListeners } = {
    addListener: vi.fn(async (_eventName, listener) => {
      const registration = {
        listener,
        removed: false,
        remove: vi.fn(async () => {
          registration.removed = true;
        }),
      };
      registrations.push(registration);
      return { remove: registration.remove };
    }),
    removeAllListeners,
  };
  const emit = (state: AppState) => {
    for (const registration of registrations) {
      if (!registration.removed) registration.listener(state);
    }
  };
  return { app, registrations, emit, removeAllListeners };
}

beforeEach(async () => {
  await removeNativeLifecycle();
});

afterEach(async () => {
  await removeNativeLifecycle();
  vi.restoreAllMocks();
});

describe('C3.0 native lifecycle bridge', () => {
  it('dispatches native foreground only for an active app state', async () => {
    const fake = lifecycleFake();
    const foreground = vi.fn();
    window.addEventListener(NATIVE_FOREGROUND_EVENT, foreground);

    try {
      await installNativeLifecycle(fake.app);
      fake.emit({ isActive: false });
      expect(foreground).not.toHaveBeenCalled();

      fake.emit({ isActive: true });
      expect(foreground).toHaveBeenCalledOnce();
    } finally {
      window.removeEventListener(NATIVE_FOREGROUND_EVENT, foreground);
    }
  });

  it('registers exactly one App listener across repeated installation', async () => {
    const fake = lifecycleFake();

    await installNativeLifecycle(fake.app);
    await installNativeLifecycle(fake.app);

    expect(fake.app.addListener).toHaveBeenCalledOnce();
    expect(fake.app.addListener).toHaveBeenCalledWith('appStateChange', expect.any(Function));
  });

  it('removes only its owned listener', async () => {
    const fake = lifecycleFake();

    await installNativeLifecycle(fake.app);
    await removeNativeLifecycle();

    expect(fake.registrations[0].remove).toHaveBeenCalledOnce();
    expect(fake.removeAllListeners).not.toHaveBeenCalled();
  });

  it('can register one fresh listener after owned cleanup', async () => {
    const fake = lifecycleFake();
    const foreground = vi.fn();
    window.addEventListener(NATIVE_FOREGROUND_EVENT, foreground);

    try {
      await installNativeLifecycle(fake.app);
      await removeNativeLifecycle();
      await installNativeLifecycle(fake.app);
      fake.emit({ isActive: true });

      expect(fake.app.addListener).toHaveBeenCalledTimes(2);
      expect(foreground).toHaveBeenCalledOnce();
    } finally {
      window.removeEventListener(NATIVE_FOREGROUND_EVENT, foreground);
    }
  });
});
