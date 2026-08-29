// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getBrowserRuntimeForTests,
  installAppRuntime,
  resetAppRuntimeForTests,
  type AppRuntime,
} from '../platform/runtime.js';
import type { ServerEventStream, ServerTransport } from '../platform/server-transport.js';
import { SSE_STALE_AFTER_MS, SseConnectionManager } from './sse-client.js';

class FakeStream implements ServerEventStream {
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn();

  open(): void {
    this.onopen?.(new Event('open'));
  }

  message(data: string): void {
    this.onmessage?.(new MessageEvent('message', { data }));
  }

  error(): void {
    this.onerror?.(new Event('error'));
  }
}

function installFakeMobileTransport(openEventStream: ReturnType<typeof vi.fn>): void {
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream,
    acquireResource: vi.fn(async (path: string) => ({
      url: `blob:fake/${encodeURIComponent(path)}`,
      release: vi.fn(),
    })),
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
}

describe('SseConnectionManager stream boundary', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resetAppRuntimeForTests();
  });

  afterEach(() => {
    resetAppRuntimeForTests();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uses browser EventSource through the browser transport', () => {
    const source = new FakeStream();
    const EventSourceMock = vi.fn(() => source);
    vi.stubGlobal('EventSource', EventSourceMock);
    installAppRuntime(getBrowserRuntimeForTests());
    const onMessage = vi.fn();
    const manager = new SseConnectionManager('/api/me/realtime', { onMessage });

    manager.open();
    source.open();
    source.message('{"type":"refresh_needed"}');

    expect(EventSourceMock).toHaveBeenCalledWith(new URL('/api/me/realtime', window.location.origin).toString());
    expect(onMessage).toHaveBeenCalledOnce();
  });

  it('accepts a fake mobile stream without constructing EventSource or a remote URL', () => {
    const source = new FakeStream();
    const openEventStream = vi.fn(() => source);
    const EventSourceMock = vi.fn();
    vi.stubGlobal('EventSource', EventSourceMock);
    installFakeMobileTransport(openEventStream);
    const onMessage = vi.fn();
    const manager = new SseConnectionManager('/api/board/alpha/events', { onMessage });

    manager.open();
    source.open();
    source.message('{"type":"refresh_needed"}');

    expect(openEventStream).toHaveBeenCalledWith('/api/board/alpha/events');
    expect(EventSourceMock).not.toHaveBeenCalled();
    expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ data: '{"type":"refresh_needed"}' }));
  });

  it('keeps ping filtering, stale restart, explicit restart, error backoff, and close above the stream', () => {
    const streams: FakeStream[] = [];
    const openEventStream = vi.fn(() => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    });
    installFakeMobileTransport(openEventStream);
    const onMessage = vi.fn();
    const manager = new SseConnectionManager('/api/me/realtime', { onMessage });

    manager.open();
    streams[0].open();
    streams[0].message('{"type":"ping"}');
    expect(onMessage).not.toHaveBeenCalled();

    vi.advanceTimersByTime(SSE_STALE_AFTER_MS);
    vi.advanceTimersByTime(400);
    expect(streams[0].close).toHaveBeenCalledOnce();
    expect(openEventStream).toHaveBeenCalledTimes(2);

    streams[1].error();
    vi.advanceTimersByTime(999);
    expect(openEventStream).toHaveBeenCalledTimes(2);
    vi.advanceTimersByTime(1);
    expect(openEventStream).toHaveBeenCalledTimes(3);

    manager.restartRequested('test');
    vi.advanceTimersByTime(400);
    expect(openEventStream).toHaveBeenCalledTimes(4);

    manager.stop();
    expect(streams[3].close).toHaveBeenCalledOnce();
  });

  it('coalesces adjacent native and browser restart requests into one stream recycle', () => {
    const streams: FakeStream[] = [];
    const openEventStream = vi.fn(() => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    });
    installFakeMobileTransport(openEventStream);
    const manager = new SseConnectionManager('/api/me/realtime', {});

    manager.open();
    manager.restartRequested('native-foreground');
    manager.restartRequested('visibility');
    manager.restartRequested('pageshow-bfcache');
    vi.advanceTimersByTime(399);
    expect(openEventStream).toHaveBeenCalledOnce();

    vi.advanceTimersByTime(1);
    expect(streams[0].close).toHaveBeenCalledOnce();
    expect(openEventStream).toHaveBeenCalledTimes(2);
  });
});
