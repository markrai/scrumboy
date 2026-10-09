// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from '../platform/runtime.js';
import type { ServerEventStream, ServerTransport } from '../platform/server-transport.js';
import { SSE_STALE_AFTER_MS, SseConnectionManager } from './sse-client.js';
import { PUBLIC_STREAM_POLICY } from '../views/public-board.js';

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

function installStreams(): FakeStream[] {
  const streams: FakeStream[] = [];
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream: vi.fn(() => {
      const stream = new FakeStream();
      streams.push(stream);
      return stream;
    }),
    acquireResource: vi.fn(),
    logout: vi.fn(),
  };
  installAppRuntime({
    kind: 'browser',
    capability: () => null,
    assetOrigin: () => 'https://example.test',
    serverOrigin: () => 'https://example.test',
    publicLinkOrigin: () => 'https://example.test',
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: vi.fn(),
    transport: () => transport,
  } as unknown as AppRuntime);
  return streams;
}

describe('SseConnectionManager policies', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resetAppRuntimeForTests();
  });
  afterEach(() => {
    resetAppRuntimeForTests();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('keeps the authenticated default: data silence triggers the stale watchdog', () => {
    const streams = installStreams();
    const manager = new SseConnectionManager('/api/me/realtime', { onMessage: vi.fn() });
    manager.open();
    streams[0].open();
    vi.advanceTimersByTime(SSE_STALE_AFTER_MS + 1000);
    expect(streams).toHaveLength(2);
    manager.stop();
  });

  it('public policy: comment-only heartbeats (no onmessage) never cause a stale reconnect', () => {
    const streams = installStreams();
    const onMessage = vi.fn();
    const manager = new SseConnectionManager('/api/public/board/ignite/events', { onMessage }, PUBLIC_STREAM_POLICY);
    manager.open();
    streams[0].open();
    vi.advanceTimersByTime(30 * 60 * 1000);
    expect(streams).toHaveLength(1);
    expect(streams[0].close).not.toHaveBeenCalled();
    expect(onMessage).not.toHaveBeenCalled();
    manager.stop();
  });

  it('public policy: open-then-close loops keep backing off and stay under 20 attempts per minute', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const streams = installStreams();
    const manager = new SseConnectionManager('/api/public/board/ignite/events', { onMessage: vi.fn() }, PUBLIC_STREAM_POLICY);
    manager.open();
    for (let elapsed = 0; elapsed < 60_000; elapsed += 100) {
      const latest = streams[streams.length - 1];
      if (!latest.close.mock.calls.length) {
        latest.open();
        latest.error();
      }
      vi.advanceTimersByTime(100);
    }
    // 5s, 10s, 20s ... backoff: well below the server budget of 20/min.
    expect(streams.length).toBeLessThanOrEqual(5);
    manager.stop();
  });

  it('public policy: backoff resets only after a stable connection', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const streams = installStreams();
    const manager = new SseConnectionManager('/api/public/board/ignite/events', { onMessage: vi.fn() }, PUBLIC_STREAM_POLICY);
    manager.open();
    streams[0].error();
    vi.advanceTimersByTime(5_000);
    expect(streams).toHaveLength(2);
    streams[1].open();
    vi.advanceTimersByTime(31_000);
    streams[1].error();
    vi.advanceTimersByTime(4_999);
    expect(streams).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(streams).toHaveLength(3);
    manager.stop();
  });

  it('adds jitter so clients do not retry in lockstep', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.99);
    const streams = installStreams();
    const manager = new SseConnectionManager('/api/public/board/ignite/events', { onMessage: vi.fn() }, PUBLIC_STREAM_POLICY);
    manager.open();
    streams[0].error();
    vi.advanceTimersByTime(5_000);
    expect(streams).toHaveLength(1);
    vi.advanceTimersByTime(1_500);
    expect(streams).toHaveLength(2);
    manager.stop();
  });

  it('a transport-error handler that stops the manager prevents any reconnect', () => {
    const streams = installStreams();
    let manager: SseConnectionManager | null = null;
    manager = new SseConnectionManager('/api/public/board/ignite/events', {
      onMessage: vi.fn(),
      onTransportError: () => manager?.stop(),
    }, PUBLIC_STREAM_POLICY);
    manager.open();
    streams[0].error();
    vi.advanceTimersByTime(5 * 60 * 1000);
    expect(streams).toHaveLength(1);
  });
});
