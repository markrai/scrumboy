import { afterEach, expect, it, vi } from 'vitest';
import { createVoiceFlowTrace, summarizeVoiceCommand } from './trace.js';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('ends once, suppresses late events, and gives subsequent tasks distinct IDs', () => {
  vi.stubGlobal('localStorage', { getItem: () => '1' });
  const debug = vi.spyOn(console, 'debug').mockImplementation(() => undefined);
  const first = createVoiceFlowTrace();
  first.end('cancelled_by_user');
  first.end('success');
  first.emit('execute');
  createVoiceFlowTrace().end('success');
  expect(debug).toHaveBeenCalledTimes(2);
  expect(debug.mock.calls[0][1].op).not.toBe(debug.mock.calls[1][1].op);
});

it('omits merged notes and only describes the dictated length', () => {
  expect(summarizeVoiceCommand({
    ir: { intent: 'todos.append_notes', projectId: 1, projectSlug: 'alpha', entities: { localId: 355, body: 'unrelated private notes', notes: 'new text' } },
    danger: false, requiresConfirmation: true, summary: '', confirmLabel: '',
  })).toEqual({ commandIntent: 'todos.append_notes', projectId: 1, localId: 355, notesLength: 8 });
});
