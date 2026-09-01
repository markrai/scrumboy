import { describe, expect, it, vi } from 'vitest';
import type { NativeSpeechOutputPlugin } from '../../../../../mobile/capacitor/shell/native-speech-output-plugin.js';
import { createSpeechOutputComposition } from '../../../../../mobile/capacitor/shell/speech-output-capability.js';
import { SPEECH_OUTPUT_CAPABILITY } from './speech-output.js';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function plugin(): NativeSpeechOutputPlugin {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready' }),
    speak: vi.fn(),
    stop: vi.fn().mockResolvedValue(undefined),
    invalidate: vi.fn().mockResolvedValue(undefined),
  };
}

describe('Capacitor speech-output composition', () => {
  it('maps native readiness through the provider-neutral capability', async () => {
    const native = plugin();
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-output-status',
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;

    await expect(capability.status()).resolves.toEqual({ state: 'ready' });
    expect(native.status).toHaveBeenCalledWith({ operationId: 'speech-output-status' });
  });

  it('resolves only a response carrying the exact owned operation ID', async () => {
    const native = plugin();
    vi.mocked(native.speak).mockResolvedValue({ operationId: 'speech-output-1' });
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-output-1',
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;

    await expect(capability.speak({ text: 'Done.', language: 'en-US' }))
      .resolves.toEqual({ completed: true });
    expect(native.speak).toHaveBeenCalledWith({
      operationId: 'speech-output-1',
      text: 'Done.',
      language: 'en-US',
    });

    vi.mocked(native.speak).mockResolvedValueOnce({ operationId: 'wrong-operation' });
    await expect(capability.speak({ text: 'Again.' }))
      .rejects.toMatchObject({ code: 'synthesis_failed' });
  });

  it('uses a named stop on abort, rejects cancelled, and ignores late completion', async () => {
    const native = plugin();
    const pending = deferred<{ operationId: string }>();
    const nativeStop = deferred<void>();
    vi.mocked(native.speak).mockReturnValueOnce(pending.promise);
    vi.mocked(native.stop).mockReturnValueOnce(nativeStop.promise);
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: vi.fn()
        .mockReturnValueOnce('speech-output-1')
        .mockReturnValueOnce('speech-output-2'),
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;
    const abort = new AbortController();
    const first = capability.speak({ text: 'First.', signal: abort.signal });

    abort.abort();
    await expect(first).rejects.toMatchObject({ code: 'cancelled' });
    expect(native.stop).toHaveBeenCalledWith({ operationId: 'speech-output-1' });
    const stopping = capability.stop();
    let stopSettled = false;
    void stopping.then(() => { stopSettled = true; });
    await Promise.resolve();
    expect(stopSettled).toBe(false);

    pending.resolve({ operationId: 'speech-output-1' });
    await Promise.resolve();
    expect(stopSettled).toBe(false);

    nativeStop.resolve();
    await stopping;
    expect(stopSettled).toBe(true);
    expect(native.stop).toHaveBeenCalledOnce();

    vi.mocked(native.speak).mockResolvedValueOnce({ operationId: 'speech-output-2' });
    await expect(capability.speak({ text: 'Second.' })).resolves.toEqual({ completed: true });
  });

  it('rejects concurrent speech as busy', async () => {
    const native = plugin();
    const pending = deferred<{ operationId: string }>();
    vi.mocked(native.speak).mockReturnValueOnce(pending.promise);
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-output-1',
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;
    const first = capability.speak({ text: 'First.' });

    await expect(capability.speak({ text: 'Second.' })).rejects.toMatchObject({ code: 'busy' });
    pending.resolve({ operationId: 'speech-output-1' });
    await expect(first).resolves.toEqual({ completed: true });
  });

  it('invalidates active speech without allowing a late native result to settle it again', async () => {
    const native = plugin();
    const pending = deferred<{ operationId: string }>();
    const nativeStop = deferred<void>();
    vi.mocked(native.speak).mockReturnValueOnce(pending.promise);
    vi.mocked(native.stop).mockReturnValueOnce(nativeStop.promise);
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-output-1',
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;
    const speaking = capability.speak({ text: 'First.' });

    const invalidating = composition.invalidate();
    await expect(speaking).rejects.toMatchObject({ code: 'cancelled' });
    expect(native.stop).toHaveBeenCalledWith({ operationId: 'speech-output-1' });
    expect(native.invalidate).not.toHaveBeenCalled();

    nativeStop.resolve();
    await invalidating;
    expect(native.invalidate).toHaveBeenCalledOnce();
    pending.resolve({ operationId: 'speech-output-1' });
    await Promise.resolve();
  });

  it('does not send an anonymous stop or let an old AbortSignal stop newer speech', async () => {
    const native = plugin();
    const ids = ['speech-output-1', 'speech-output-2'];
    vi.mocked(native.speak).mockImplementation(async ({ operationId }) => ({ operationId }));
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => ids.shift()!,
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;
    const oldAbort = new AbortController();

    await capability.speak({ text: 'First.', signal: oldAbort.signal });
    const second = capability.speak({ text: 'Second.' });
    oldAbort.abort();
    await expect(second).resolves.toEqual({ completed: true });
    await capability.stop();

    expect(native.stop).not.toHaveBeenCalled();
  });

  it('rejects new speech while a prior named native stop is in flight', async () => {
    const native = plugin();
    const firstNative = deferred<{ operationId: string }>();
    const stopNative = deferred<void>();
    vi.mocked(native.speak)
      .mockReturnValueOnce(firstNative.promise)
      .mockResolvedValueOnce({ operationId: 'speech-output-2' });
    vi.mocked(native.stop).mockReturnValueOnce(stopNative.promise);
    const ids = ['speech-output-1', 'speech-output-2'];
    const composition = createSpeechOutputComposition({
      plugin: native,
      operationIdFactory: () => ids.shift()!,
    });
    const capability = composition.registry.get(SPEECH_OUTPUT_CAPABILITY)!;
    const abort = new AbortController();
    const first = capability.speak({ text: 'First.', signal: abort.signal });

    abort.abort();
    await expect(first).rejects.toMatchObject({ code: 'cancelled' });
    expect(native.stop).toHaveBeenCalledWith({ operationId: 'speech-output-1' });
    await expect(capability.speak({ text: 'Second.' })).rejects.toMatchObject({ code: 'busy' });
    expect(native.speak).toHaveBeenCalledOnce();

    stopNative.resolve();
    await capability.stop();
    firstNative.resolve({ operationId: 'speech-output-1' });
    await Promise.resolve();

    await expect(capability.speak({ text: 'Second.' })).resolves.toEqual({ completed: true });
    expect(native.speak).toHaveBeenCalledTimes(2);
  });
});
