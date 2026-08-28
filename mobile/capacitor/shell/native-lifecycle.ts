import { App, type AppState } from '@capacitor/app';
import type { PluginListenerHandle } from '@capacitor/core';

export const NATIVE_FOREGROUND_EVENT = 'scrumboy:native-foreground';

export interface NativeAppLifecycle {
  addListener(
    eventName: 'appStateChange',
    listener: (state: AppState) => void,
  ): Promise<PluginListenerHandle>;
}

let listenerHandle: Promise<PluginListenerHandle> | null = null;
let listenerRemoval: Promise<void> = Promise.resolve();

/** Install the one shell-owned native foreground listener for this WebView lifecycle. */
export async function installNativeLifecycle(app: NativeAppLifecycle = App): Promise<void> {
  await listenerRemoval;
  if (listenerHandle) {
    await listenerHandle;
    return;
  }

  const pending = app.addListener('appStateChange', ({ isActive }) => {
    if (isActive) {
      window.dispatchEvent(new Event(NATIVE_FOREGROUND_EVENT));
    }
  });
  listenerHandle = pending;

  try {
    await pending;
  } catch (error) {
    if (listenerHandle === pending) {
      listenerHandle = null;
    }
    throw error;
  }
}

/** Remove only the listener installed by this module; safe to install again afterward. */
export async function removeNativeLifecycle(): Promise<void> {
  const ownedHandle = listenerHandle;
  if (!ownedHandle) {
    await listenerRemoval;
    return;
  }

  listenerHandle = null;
  listenerRemoval = ownedHandle.then((handle) => handle.remove());
  try {
    await listenerRemoval;
  } finally {
    listenerRemoval = Promise.resolve();
  }
}
