import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import {
  installVoiceAgentEvaluationBridge,
  NATIVE_VOICE_AGENT_EVAL_EVENT,
  VOICE_AGENT_EVAL_CORPUS,
} from '../../../../mobile/capacitor/shell/voice-agent-evaluation-bridge.js';

describe('Capacitor VoiceFlow agent evaluation bridge', () => {
  it('installs from the mobile bootstrap after the create dry-run bridge', () => {
    const source = readFileSync(new URL('../../../../mobile/capacitor/shell/bootstrap.ts', import.meta.url), 'utf8');
    expect(source).toContain("import { installVoiceAgentEvaluationBridge } from './voice-agent-evaluation-bridge.js'");
    expect(source).toMatch(/installVoiceCreateDryRunBridge\(\)\)\.then\(\(\) => installVoiceAgentEvaluationBridge\(\)\)/);
  });
  it('keeps the device evaluator transport-minimal', () => {
    const source = readFileSync(new URL('../modules/voice/agent-device-evaluation.ts', import.meta.url), 'utf8');
    expect(source).not.toContain('callMcpTool');
    expect(source).not.toContain("from './mcp-client.js'");
    expect(source).toMatch(/LOCAL_TEXT_GENERATION_CAPABILITY/);
    expect(source).toMatch(/evaluateVoiceAgentCorpusOnCurrentBoard/);
  });

  it('serializes device requests and returns only non-mutating structured results', async () => {
    let listener: ((event: { requestId: string; transcript: string; timeoutMs?: number }) => void) | null = null;
    const completions: Array<{ requestId: string; resultJson: string }> = [];
    const plugin = {
      addListener: vi.fn(async (eventName, next) => {
        expect(eventName).toBe(NATIVE_VOICE_AGENT_EVAL_EVENT);
        listener = next;
        return { remove: async () => undefined };
      }),
      completeAgentEval: vi.fn(async value => { completions.push(value); }),
    };
    let active = 0;
    let maximumActive = 0;
    const importer = vi.fn(async () => ({
      evaluateVoiceAgentOnCurrentBoard: async (input: string) => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        await new Promise(resolve => setTimeout(resolve, 5));
        active -= 1;
        return {
          version: 1,
          transcript: input,
          deterministicCall: null,
          rawEnvelope: null,
          guardedEnvelope: null,
          slots: { reference: null, numeric: false, lane: null },
          rawMatches: null,
          applicationMatches: null,
          mismatches: [],
          mutationExecuted: false,
          providerUsed: true,
        };
      },
      evaluateVoiceAgentCorpusOnCurrentBoard: async () => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        await new Promise(resolve => setTimeout(resolve, 5));
        active -= 1;
        return {
          version: 1,
          mutationExecuted: false,
          providerUsed: true,
          caseCount: 13,
          rawMatched: 4,
          applicationMatched: 11,
          rawAccuracy: 4 / 13,
          applicationAccuracy: 11 / 13,
          results: [],
        };
      },
    }));
    await installVoiceAgentEvaluationBridge(importer, plugin as never);
    listener!({ requestId: 'first', transcript: 'Move #239 to done.' });
    listener!({ requestId: 'corpus_1', transcript: VOICE_AGENT_EVAL_CORPUS });
    await vi.waitFor(() => expect(completions).toHaveLength(2));
    expect(maximumActive).toBe(1);
    expect(completions.map(value => value.requestId)).toEqual(['first', 'corpus_1']);
    expect(JSON.parse(completions[0].resultJson)).toMatchObject({ mutationExecuted: false, transcript: 'Move #239 to done.' });
    expect(JSON.parse(completions[1].resultJson)).toMatchObject({
      mutationExecuted: false,
      caseCount: 13,
      rawMatched: 4,
      applicationMatched: 11,
    });
  });

  it('replaces malformed application output with a safe bridge failure', async () => {
    let listener: ((event: { requestId: string; transcript: string }) => void) | null = null;
    const completeAgentEval = vi.fn(async () => undefined);
    await installVoiceAgentEvaluationBridge(
      async () => ({ evaluateVoiceAgentOnCurrentBoard: async () => ({ mutationExecuted: true }) }),
      {
        addListener: async (_name, next) => { listener = next; return { remove: async () => undefined }; },
        completeAgentEval,
      } as never,
    );
    listener!({ requestId: 'malformed', transcript: 'Move #239 to done.' });
    await vi.waitFor(() => expect(completeAgentEval).toHaveBeenCalledOnce());
    expect(JSON.parse(completeAgentEval.mock.calls[0][0].resultJson)).toMatchObject({
      mutationExecuted: false,
      providerUsed: false,
      caseCount: 0,
      rawMatched: 0,
      applicationMatched: 0,
    });
  });
});
