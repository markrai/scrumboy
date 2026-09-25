import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import {
  installVoiceCreateDryRunBridge,
  NATIVE_VOICE_CREATE_DRY_RUN_EVENT,
} from '../../../../mobile/capacitor/shell/voice-create-dry-run-bridge.js';

describe('Capacitor Voice Create dry-run bridge', () => {
  it('keeps the semantic evaluator capability-minimal', () => {
    const source = readFileSync(new URL('../modules/voice/voice-create-evaluation.ts', import.meta.url), 'utf8');
    expect(source).not.toContain('callMcpTool');
    expect(source).not.toContain("from './mcp-client.js'");
    expect(source).toMatch(/readMembers: VoiceCreateMembersReader;/);
    expect(source).not.toMatch(/readMembers\?: VoiceCreateMembersReader;/);
  });

  it('serializes device requests and returns only non-mutating structured results', async () => {
    let listener: ((event: { requestId: string; transcript: string; timeoutMs?: number }) => void) | null = null;
    const completions: Array<{ requestId: string; resultJson: string }> = [];
    const plugin = {
      addListener: vi.fn(async (eventName, next) => {
        expect(eventName).toBe(NATIVE_VOICE_CREATE_DRY_RUN_EVENT);
        listener = next;
        return { remove: async () => undefined };
      }),
      completeDryRun: vi.fn(async value => { completions.push(value); }),
    };
    let active = 0;
    let maximumActive = 0;
    const importer = vi.fn(async () => ({
      evaluateVoiceCreateDryRunOnCurrentBoard: async (input: string) => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        await new Promise(resolve => setTimeout(resolve, 5));
        active -= 1;
        return {
          version: 1,
          input,
          outcome: 'ready',
          planner: { status: 'ok', plan: { version: 1, kind: 'create', title: input } },
          preparation: { status: 'ready', title: input, lane: { key: 'todo', name: 'Todo', defaulted: true }, assignee: null, tags: [], notesPresent: false, summary: input },
          confirmationReady: true,
          plannerCallCount: 1,
          mutationExecuted: false,
        };
      },
    }));
    await installVoiceCreateDryRunBridge(importer, plugin as never);
    listener!({ requestId: 'first', transcript: 'First' });
    listener!({ requestId: 'second', transcript: 'Second' });
    await vi.waitFor(() => expect(completions).toHaveLength(2));
    expect(maximumActive).toBe(1);
    expect(completions.map(value => value.requestId)).toEqual(['first', 'second']);
    for (const completion of completions) {
      expect(JSON.parse(completion.resultJson)).toMatchObject({ mutationExecuted: false, plannerCallCount: 1 });
    }
  });

  it('replaces malformed application output with a safe bridge failure', async () => {
    let listener: ((event: { requestId: string; transcript: string }) => void) | null = null;
    const completeDryRun = vi.fn(async () => undefined);
    await installVoiceCreateDryRunBridge(
      async () => ({ evaluateVoiceCreateDryRunOnCurrentBoard: async () => ({ mutationExecuted: true }) }),
      {
        addListener: async (_name, next) => { listener = next; return { remove: async () => undefined }; },
        completeDryRun,
      } as never,
    );
    listener!({ requestId: 'malformed', transcript: 'Create Fred' });
    await vi.waitFor(() => expect(completeDryRun).toHaveBeenCalledOnce());
    expect(JSON.parse(completeDryRun.mock.calls[0][0].resultJson)).toMatchObject({
      outcome: 'unexpected_failure',
      planner: { code: 'debug_bridge_unavailable' },
      mutationExecuted: false,
    });
  });
});
