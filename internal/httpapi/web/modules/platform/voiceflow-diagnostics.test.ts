import { afterEach, describe, expect, it, vi } from 'vitest';
import { voiceFlowDiagnostic } from './voiceflow-diagnostics.js';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('VoiceFlow qualification diagnostics', () => {
  it('is opt-in and emits only the supplied ephemeral event details', () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => undefined);
    const getItem = vi.fn().mockReturnValue(null);
    const setItem = vi.fn();
    vi.stubGlobal('localStorage', { getItem, setItem });

    voiceFlowDiagnostic('ASR result', {
      operationId: 'speech-1',
      transcript: 'Move Agenda lane finalization to backlog',
    });
    expect(debug).not.toHaveBeenCalled();

    getItem.mockReturnValue('1');
    voiceFlowDiagnostic('ASR result', {
      operationId: 'speech-1',
      transcript: 'Move Agenda lane finalization to backlog',
    });

    expect(debug).toHaveBeenCalledWith('VoiceFlow ASR result', {
      operationId: 'speech-1',
      transcript: 'Move Agenda lane finalization to backlog',
    });
    expect(setItem).not.toHaveBeenCalled();
  });
});

it('forwards only opted-in traces as serialized shell events and isolates sink errors', () => {
  vi.stubGlobal('localStorage', { getItem: () => '1' });
  const dispatch = vi.fn();
  vi.stubGlobal('dispatchEvent', dispatch);
  vi.stubGlobal('CustomEvent', class { constructor(public type: string, public options: { detail: string }) {} });
  vi.spyOn(console, 'debug').mockImplementation(() => undefined);
  voiceFlowDiagnostic('trace', { op: '42', stage: 'asr_final', transcript: 'quoted "text"' });
  expect(dispatch.mock.calls[0][0]).toMatchObject({ type: 'scrumboy:voiceflow-trace', options: { detail: 'VF {"op":"42","stage":"asr_final","transcript":"quoted \\"text\\""}' } });
  dispatch.mockClear();
  voiceFlowDiagnostic('ASR ready');
  expect(dispatch).not.toHaveBeenCalled();
  vi.mocked(console.debug).mockImplementation(() => { throw new Error('sink unavailable'); });
  expect(() => voiceFlowDiagnostic('trace', { op: '42' })).not.toThrow();
});
