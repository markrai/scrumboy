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
