// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { off, on } from '../events.js';
import {
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from '../platform/runtime.js';
import type { ServerEventStream, ServerTransport } from '../platform/server-transport.js';
import { setAuthStatusAvailable, setProjectId, setUser } from '../state/mutations.js';
import { startGlobalRealtime, stopGlobalRealtime } from './realtime.js';

class SyntheticStream implements ServerEventStream {
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn();

  emit(data: string): void {
    this.onmessage?.(new MessageEvent('message', { data }));
  }
}

afterEach(() => {
  stopGlobalRealtime();
  setUser(null);
  setProjectId(null);
  setAuthStatusAvailable(false);
  resetAppRuntimeForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('product realtime over a fake mobile stream', () => {
  it('processes a synthetic event without browser EventSource or remote URL construction', () => {
    const stream = new SyntheticStream();
    const openEventStream = vi.fn(() => stream);
    const transport: ServerTransport = {
      request: vi.fn(),
      openEventStream,
      acquireResource: vi.fn(async (path: string) => ({ url: path, release: vi.fn() })),
      logout: vi.fn().mockResolvedValue(undefined),
    };
    const runtime: AppRuntime = {
      kind: 'capacitor',
      capability: () => null,
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
    const EventSourceMock = vi.fn();
    vi.stubGlobal('EventSource', EventSourceMock);
    setAuthStatusAvailable(true);
    setUser({ id: 11, email: 'u@example.test', name: 'User', systemRole: 'user' } as any);
    const received = vi.fn();
    on('realtime:event', received);

    try {
      startGlobalRealtime();
      stream.emit(JSON.stringify({ id: 'fake-1', type: 'refresh_needed', projectId: 4 }));

      expect(openEventStream).toHaveBeenCalledWith('/api/me/realtime');
      expect(EventSourceMock).not.toHaveBeenCalled();
      expect(received).toHaveBeenCalledWith(expect.objectContaining({ id: 'fake-1', type: 'refresh_needed' }));
    } finally {
      off('realtime:event', received);
    }
  });
});
