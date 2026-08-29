// @vitest-environment happy-dom

import { describe, expect, it, vi } from 'vitest';
import {
  createLocalTextGenerationComposition,
} from '../../../../mobile/capacitor/shell/local-text-generation-capability.js';
import type { NativeLocalTextGenerationPlugin } from '../../../../mobile/capacitor/shell/native-local-text-generation-plugin.js';
import {
  LOCAL_TEXT_GENERATION_CAPABILITY,
  type LocalTextGenerationCapability,
  type LocalTextGenerationErrorCode,
} from '../modules/platform/local-text-generation.js';

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function pluginFake(overrides: Partial<NativeLocalTextGenerationPlugin> = {}): NativeLocalTextGenerationPlugin {
  return {
    status: vi.fn(async () => ({ state: 'temporarily-unavailable', reason: 'provider' })),
    prepare: vi.fn(async () => undefined),
    generate: vi.fn(async ({ requestId }) => ({ requestId, text: 'generated' })),
    cancel: vi.fn(async () => undefined),
    invalidate: vi.fn(async () => undefined),
    ...overrides,
  };
}

function createHarness(plugin: NativeLocalTextGenerationPlugin) {
  let sequence = 0;
  const composition = createLocalTextGenerationComposition({
    plugin,
    operationIdFactory: () => `operation-${++sequence}`,
    invalidationTimeoutMs: 25,
  });
  const capability = composition.registry.get(LOCAL_TEXT_GENERATION_CAPABILITY);
  if (!capability) throw new Error('test composition did not register local text generation');
  return { ...composition, capability };
}

function request(requestId = 'request-1') {
  return {
    requestId,
    input: 'Write one sentence.',
    instructions: 'Be concise.',
    maximumOutputTokens: 32,
  };
}

describe('Capacitor local text generation adapter', () => {
  it('constructs without status, preparation, generation, or warmup work', () => {
    const plugin = pluginFake();
    const { capability } = createHarness(plugin);

    expect(capability).toBeDefined();
    expect(plugin.status).not.toHaveBeenCalled();
    expect(plugin.prepare).not.toHaveBeenCalled();
    expect(plugin.generate).not.toHaveBeenCalled();
  });

  it.each([
    [{ state: 'unsupported', reason: 'device' }],
    [{ state: 'action-required', action: 'download' }],
    [{ state: 'preparing', downloadedBytes: 10, totalBytes: 100 }],
    [{ state: 'ready', maximumOutputTokens: 256, contextTokenLimit: 4_096, providerModel: 'nano-v4' }],
    [{ state: 'temporarily-unavailable', reason: 'provider' }],
  ] as const)('validates and returns native status %o', async (status) => {
    const plugin = pluginFake({ status: vi.fn(async () => status) });
    const { capability } = createHarness(plugin);

    await expect(capability.status()).resolves.toEqual(status);
    expect(plugin.status).toHaveBeenCalledWith({ operationId: 'operation-1' });
  });

  it('rejects invalid native status data as an internal error', async () => {
    const plugin = pluginFake({
      status: vi.fn(async () => ({ state: 'ready', maximumOutputTokens: 4_096 } as never)),
    });

    await expect(createHarness(plugin).capability.status()).rejects.toMatchObject({ code: 'internal' });
  });

  it('requires literal user initiation and otherwise delegates preparation once', async () => {
    const plugin = pluginFake();
    const { capability } = createHarness(plugin);

    await expect(capability.prepare({ userInitiated: false } as never))
      .rejects.toMatchObject({ code: 'invalid_request' });
    expect(plugin.prepare).not.toHaveBeenCalled();

    await expect(capability.prepare({ userInitiated: true })).resolves.toBeUndefined();
    expect(plugin.prepare).toHaveBeenCalledWith({ operationId: 'operation-1', userInitiated: true });
  });

  it('contains malformed cancellation inputs before native invocation', async () => {
    const plugin = pluginFake();
    const { capability } = createHarness(plugin);

    await expect(capability.status(null as never)).rejects.toMatchObject({ code: 'invalid_request' });
    await expect(capability.prepare({ userInitiated: true, signal: {} as AbortSignal }))
      .rejects.toMatchObject({ code: 'invalid_request' });
    await expect(capability.generate({ ...request(), signal: {} as AbortSignal }))
      .rejects.toMatchObject({ code: 'invalid_request' });
    expect(plugin.status).not.toHaveBeenCalled();
    expect(plugin.prepare).not.toHaveBeenCalled();
    expect(plugin.generate).not.toHaveBeenCalled();
  });

  it('maps generation input and returns only a matching bounded result', async () => {
    const plugin = pluginFake();
    const { capability } = createHarness(plugin);

    await expect(capability.generate(request())).resolves.toEqual({ requestId: 'request-1', text: 'generated' });
    expect(plugin.generate).toHaveBeenCalledWith({ operationId: 'operation-1', ...request() });
  });

  it('rejects cross-delivered or oversized native output', async () => {
    const mismatched = pluginFake({
      generate: vi.fn(async () => ({ requestId: 'other', text: 'late' })),
    });
    await expect(createHarness(mismatched).capability.generate(request()))
      .rejects.toMatchObject({ code: 'output_rejected' });

    const oversized = pluginFake({
      generate: vi.fn(async ({ requestId }) => ({ requestId, text: 'x'.repeat(65_537) })),
    });
    await expect(createHarness(oversized).capability.generate(request()))
      .rejects.toMatchObject({ code: 'output_rejected' });
  });

  it('enforces one active generation and rejects recent request ID reuse', async () => {
    const first = deferred<{ requestId: string; text: string }>();
    const plugin = pluginFake({ generate: vi.fn(() => first.promise) });
    const { capability } = createHarness(plugin);
    const pending = capability.generate(request());

    await expect(capability.generate(request('request-2'))).rejects.toMatchObject({ code: 'busy' });
    first.resolve({ requestId: 'request-1', text: 'done' });
    await expect(pending).resolves.toMatchObject({ text: 'done' });
    await expect(capability.generate(request())).rejects.toMatchObject({ code: 'invalid_request' });
    expect(plugin.generate).toHaveBeenCalledTimes(1);
  });

  it('does not invoke native code for an already-aborted signal', async () => {
    const plugin = pluginFake();
    const controller = new AbortController();
    controller.abort();

    await expect(createHarness(plugin).capability.generate({ ...request(), signal: controller.signal }))
      .rejects.toMatchObject({ code: 'cancelled' });
    expect(plugin.generate).not.toHaveBeenCalled();
  });

  it('holds the generation slot until native cancellation acknowledgement and ignores late output', async () => {
    const nativeResult = deferred<{ requestId: string; text: string }>();
    const cancellation = deferred<void>();
    const plugin = pluginFake({
      generate: vi.fn()
        .mockImplementationOnce(() => nativeResult.promise)
        .mockImplementation(async ({ requestId }) => ({ requestId, text: 'next result' })),
      cancel: vi.fn(() => cancellation.promise),
    });
    const { capability } = createHarness(plugin);
    const controller = new AbortController();
    const pending = capability.generate({ ...request(), signal: controller.signal });
    controller.abort();

    await expect(capability.generate(request('request-2'))).rejects.toMatchObject({ code: 'busy' });
    expect(plugin.cancel).toHaveBeenCalledWith({ operationId: 'operation-1' });
    cancellation.resolve();
    await expect(pending).rejects.toMatchObject({ code: 'cancelled' });

    nativeResult.resolve({ requestId: 'request-1', text: 'must be ignored' });
    await expect(capability.generate(request('request-2'))).resolves.toMatchObject({ requestId: 'request-2' });
  });

  it('invalidates all local delivery before awaiting native acknowledgement', async () => {
    const nativeResult = deferred<{ requestId: string; text: string }>();
    const invalidation = deferred<void>();
    const plugin = pluginFake({
      generate: vi.fn(() => nativeResult.promise),
      invalidate: vi.fn(() => invalidation.promise),
    });
    const harness = createHarness(plugin);
    const pending = harness.capability.generate(request());
    const invalidating = harness.invalidate();

    nativeResult.resolve({ requestId: 'request-1', text: 'must be ignored' });
    invalidation.resolve();
    await invalidating;
    await expect(pending).rejects.toMatchObject({ code: 'cancelled' });
  });

  it('shares one bounded native invalidation across concurrent callers', async () => {
    const nativeInvalidation = deferred<void>();
    const plugin = pluginFake({ invalidate: vi.fn(() => nativeInvalidation.promise) });
    const harness = createHarness(plugin);

    const first = harness.invalidate();
    const second = harness.invalidate();
    expect(plugin.invalidate).toHaveBeenCalledTimes(1);

    nativeInvalidation.resolve();
    await Promise.all([first, second]);
  });

  it.each([
    'unsupported',
    'disabled',
    'not_ready',
    'download_failed',
    'foreground_required',
    'busy',
    'quota_exceeded',
    'insufficient_storage',
    'input_too_large',
    'invalid_request',
    'output_rejected',
    'cancelled',
    'internal',
  ] satisfies LocalTextGenerationErrorCode[])('maps native %s without exposing native messages', async (code) => {
    const plugin = pluginFake({
      status: vi.fn(async () => Promise.reject({
        code,
        message: 'sensitive provider diagnostic',
        data: { recoverable: true, retryAfterMs: 500 },
      })),
    });

    await expect(createHarness(plugin).capability.status()).rejects.toMatchObject({
      code,
      recoverable: true,
      retryAfterMs: 500,
    });
    await expect(createHarness(plugin).capability.status()).rejects.not.toMatchObject({
      message: 'sensitive provider diagnostic',
    });
  });

  it('maps unknown native failures to internal and removes settled abort listeners', async () => {
    const plugin = pluginFake({ status: vi.fn(async () => Promise.reject(new Error('raw'))) });
    await expect(createHarness(plugin).capability.status()).rejects.toMatchObject({ code: 'internal' });

    const successfulPlugin = pluginFake();
    const controller = new AbortController();
    const capability: LocalTextGenerationCapability = createHarness(successfulPlugin).capability;
    await capability.generate({ ...request(), signal: controller.signal });
    controller.abort();
    expect(successfulPlugin.cancel).not.toHaveBeenCalled();
  });

  it('drops unbounded retry metadata and contains operation ID factory failures', async () => {
    const unbounded = pluginFake({
      status: vi.fn(async () => Promise.reject({
        code: 'busy',
        data: { recoverable: true, retryAfterMs: 86_400_001 },
      })),
    });
    const error = await createHarness(unbounded).capability.status().catch((failure: unknown) => failure);
    expect(error).toMatchObject({ code: 'busy' });
    expect(error).not.toHaveProperty('retryAfterMs');

    const brokenComposition = createLocalTextGenerationComposition({
      plugin: pluginFake(),
      operationIdFactory: () => { throw new Error('must stay contained'); },
    });
    const capability = brokenComposition.registry.get(LOCAL_TEXT_GENERATION_CAPABILITY)!;
    await expect(capability.status()).rejects.toMatchObject({ code: 'internal' });
  });
});
