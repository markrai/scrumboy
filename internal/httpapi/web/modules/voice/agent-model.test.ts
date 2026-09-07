import { describe, expect, it, vi } from 'vitest';
import { createVoiceAgentModel } from './agent-model.js';
describe('local agent provider boundary', () => {
  it('uses the local capability with v9 domain instructions and bounded output', async () => {
    const generate = vi.fn(async request => ({ requestId: request.requestId, text: '{"kind":"finish"}' }));
    const model = createVoiceAgentModel({ generate, status: vi.fn(), prepare: vi.fn() }, 'en');
    const signal = new AbortController().signal;
    expect(await model('{"goal":"Open Settings"}', signal)).toBe('{"kind":"finish"}');
    const request = generate.mock.calls[0][0];
    expect(request.requestId).toMatch(/^voice-agent-v9-/); expect(request.maximumOutputTokens).toBe(256); expect(request.signal).toBe(signal);
    expect(request.instructions).toContain("Bird's Eye View"); expect(request.instructions.length).toBeLessThan(8192);
  });
  it('rejects late/mismatched provider request ownership', async () => {
    const model = createVoiceAgentModel({ generate: vi.fn(async () => ({ requestId: 'old', text: '{"kind":"finish"}' })), status: vi.fn(), prepare: vi.fn() }, 'en');
    await expect(model('goal', new AbortController().signal)).rejects.toThrow('Stale model response');
  });
});
