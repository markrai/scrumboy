import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { NativeSpeechInputPlugin } from '../../../../../mobile/capacitor/shell/native-speech-input-plugin.js';
import { createSpeechInputComposition } from '../../../../../mobile/capacitor/shell/speech-input-capability.js';
import { SPEECH_INPUT_CAPABILITY } from './speech-input.js';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function plugin(): NativeSpeechInputPlugin {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready' }),
    listen: vi.fn(),
    cancel: vi.fn().mockResolvedValue(undefined),
    invalidate: vi.fn().mockResolvedValue(undefined),
    addListener: vi.fn().mockResolvedValue({ remove: vi.fn().mockResolvedValue(undefined) }),
  };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('Capacitor speech-input composition', () => {
  it('reports genuine recognizer readiness once for the owned operation', async () => {
    const native = plugin();
    let listeningListener!: (event: { operationId: string }) => void;
    vi.mocked(native.addListener).mockImplementation(async (_name, listener) => {
      listeningListener = listener;
      return { remove: vi.fn().mockResolvedValue(undefined) };
    });
    vi.mocked(native.listen).mockImplementation(async () => {
      listeningListener({ operationId: 'other' });
      listeningListener({ operationId: 'speech-1' });
      listeningListener({ operationId: 'speech-1' });
      return { transcript: 'Open story 355' };
    });
    const composition = createSpeechInputComposition({ plugin: native, operationIdFactory: () => 'speech-1' });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;
    const onListening = vi.fn();

    await capability.listen({ maxDurationMs: 10_000, onListening });

    expect(onListening).toHaveBeenCalledOnce();
  });

  it('reports the owned final transcript only through opt-in qualification diagnostics', async () => {
    const native = plugin();
    let listeningListener!: (event: { operationId: string }) => void;
    vi.mocked(native.addListener).mockImplementation(async (_name, listener) => {
      listeningListener = listener;
      return { remove: vi.fn().mockResolvedValue(undefined) };
    });
    vi.mocked(native.listen).mockImplementation(async () => {
      listeningListener({ operationId: 'speech-1' });
      return { transcript: 'Move Agenda lane finalization to backlog' };
    });
    vi.stubGlobal('localStorage', { getItem: vi.fn().mockReturnValue('1') });
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => undefined);
    const composition = createSpeechInputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-1',
    });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;

    await capability.listen({ maxDurationMs: 10_000 });

    expect(debug).toHaveBeenCalledWith('VoiceFlow ASR start', { operationId: 'speech-1' });
    expect(debug).toHaveBeenCalledWith('VoiceFlow ASR ready', { operationId: 'speech-1' });
    expect(debug).toHaveBeenCalledWith('VoiceFlow ASR result', {
      operationId: 'speech-1',
      transcript: 'Move Agenda lane finalization to backlog',
    });
  });

  it('owns the 10-second deadline, cancels native work, and ignores a late result', async () => {
    const native = plugin();
    const first = deferred<{ transcript: string }>();
    vi.mocked(native.listen).mockReturnValueOnce(first.promise);
    const composition = createSpeechInputComposition({
      plugin: native,
      operationIdFactory: vi.fn().mockReturnValueOnce('speech-1').mockReturnValueOnce('speech-2'),
    });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;

    const timedOut = capability.listen({ maxDurationMs: 10_000 });
    const timeoutResult = timedOut.then(() => null, (error) => error);
    await vi.waitFor(() => expect(native.listen).toHaveBeenCalledWith({
      operationId: 'speech-1',
      maxDurationMs: 10_000,
    }));
    await vi.advanceTimersByTimeAsync(10_000);
    await expect(timeoutResult).resolves.toMatchObject({ code: 'timeout' });
    expect(native.cancel).toHaveBeenCalledWith({ operationId: 'speech-1' });

    first.resolve({ transcript: 'late transcript' });
    await Promise.resolve();

    vi.mocked(native.listen).mockResolvedValueOnce({ transcript: 'fresh transcript' });
    await expect(capability.listen({ maxDurationMs: 10_000 }))
      .resolves.toEqual({ transcript: 'fresh transcript' });
    expect(native.listen).toHaveBeenCalledTimes(2);
  });

  it('stops early on AbortSignal and never starts two recognizers', async () => {
    const native = plugin();
    const pending = deferred<{ transcript: string }>();
    vi.mocked(native.listen).mockReturnValue(pending.promise);
    const composition = createSpeechInputComposition({ plugin: native, operationIdFactory: () => 'speech-1' });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;
    const abort = new AbortController();

    const first = capability.listen({ maxDurationMs: 10_000, signal: abort.signal });
    await expect(capability.listen({ maxDurationMs: 10_000 })).rejects.toMatchObject({ code: 'busy' });
    abort.abort();
    await expect(first).rejects.toMatchObject({ code: 'cancelled' });
    expect(native.cancel).toHaveBeenCalledOnce();
    expect(native.listen).toHaveBeenCalledOnce();
  });

  it('invalidates active listening and suppresses late delivery', async () => {
    const native = plugin();
    const pending = deferred<{ transcript: string }>();
    vi.mocked(native.listen).mockReturnValue(pending.promise);
    const composition = createSpeechInputComposition({ plugin: native, operationIdFactory: () => 'speech-1' });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;
    const listening = capability.listen({ maxDurationMs: 10_000 });

    await composition.invalidate();
    await expect(listening).rejects.toMatchObject({ code: 'cancelled' });
    expect(native.invalidate).toHaveBeenCalledOnce();
    pending.resolve({ transcript: 'late transcript' });
    await Promise.resolve();
  });

  it('preserves normalized native failures with bounded provider diagnostics', async () => {
    const native = plugin();
    vi.mocked(native.listen).mockRejectedValue({
      code: 'recognition_failed',
      data: {
        recoverable: true,
        providerCode: 3,
        providerReason: 'audio',
      },
    });
    const composition = createSpeechInputComposition({
      plugin: native,
      operationIdFactory: () => 'speech-1',
    });
    const capability = composition.registry.get(SPEECH_INPUT_CAPABILITY)!;

    await expect(capability.listen({ maxDurationMs: 10_000 })).rejects.toMatchObject({
      code: 'recognition_failed',
      recoverable: true,
      providerCode: 3,
      providerReason: 'audio',
    });
  });
});
