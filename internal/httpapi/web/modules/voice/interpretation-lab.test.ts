// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { LocalTextGenerationCapability } from '../platform/local-text-generation.js';
import {
  INTERPRETATION_LAB_SEED_CORPUS,
  createInterpretationLabCopyPayload,
  mountInterpretationLab,
} from './interpretation-lab.js';
import {
  INTERPRETATION_LAB_CANDIDATE_PROFILE,
  INTERPRETATION_LAB_CURRENT_PROFILE,
  INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
} from './interpretation-lab-prompts.js';
import type {
  RunInterpretationLabBatchOptions,
  VoiceInterpretationDiagnostic,
} from './interpretation-diagnostics.js';

function capability(): LocalTextGenerationCapability & {
  status: ReturnType<typeof vi.fn>;
} {
  return {
    status: vi.fn().mockResolvedValue({ state: 'ready', maximumOutputTokens: 256 }),
    prepare: vi.fn().mockResolvedValue(undefined),
    generate: vi.fn(),
  };
}

function diagnostic(overrides: Partial<VoiceInterpretationDiagnostic> = {}): VoiceInterpretationDiagnostic {
  return {
    input: 'Create a to-do about cleaning the garage today',
    profile: INTERPRETATION_LAB_CANDIDATE_PROFILE,
    rawOutput: '{"command":"create todo Clean the garage","unrepresented":["today"]}',
    provider: 'ok',
    envelope: {
      state: 'refused',
      command: 'create todo Clean the garage',
      unrepresented: ['today'],
    },
    canonicalParse: { state: 'ok', command: 'create todo Clean the garage' },
    resolution: { state: 'ok', summary: 'Create todo "Clean the garage"' },
    finalClassification: 'partial',
    ...overrides,
  };
}

function dialog(): HTMLDialogElement {
  const root = document.createElement('dialog');
  root.innerHTML = '<form><div class="voice-command__review"></div></form>';
  document.body.appendChild(root);
  return root;
}

async function flushAsync(): Promise<void> {
  for (let index = 0; index < 20; index += 1) await Promise.resolve();
}

describe('temporary VoiceFlow Interpretation Lab UI', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
    localStorage.clear();
  });

  it('mounts behind an explicit temporary control with the owner corpus and no persistent preference', () => {
    const root = dialog();
    mountInterpretationLab(root, {
      runtime: { capability },
      getResolveContext: () => null,
    });

    const toggle = root.querySelector<HTMLButtonElement>('#voiceInterpretationLabToggle');
    const panel = root.querySelector<HTMLElement>('#voiceInterpretationLabPanel');
    const corpus = root.querySelector<HTMLTextAreaElement>('#voiceInterpretationLabCorpus');
    expect(toggle?.textContent).toBe('Interpretation Lab');
    expect(panel?.hidden).toBe(true);
    toggle?.click();
    expect(panel?.hidden).toBe(false);
    expect(panel?.textContent).toContain('Experimental / temporary');
    expect(corpus?.value).toBe(INTERPRETATION_LAB_SEED_CORPUS);
    expect(corpus?.value).toContain('Create a to-do called fix the flux capacitor in the bathroom');
    expect(corpus?.value).toContain('Open and delete Bogus');
    expect(corpus?.value).toContain('Move Bogus to Done and assign it to Ada');
    expect(corpus?.value).toContain('Assign Bogus to Ada and open it');
    expect(corpus?.value).toContain('Create a todo to call the dentist and delete Bogus');
    expect(corpus?.value).toContain('Create a todo to buy milk and eggs');
    expect(corpus?.value).toContain('Create a todo to call Alice and Bob');
    expect(root.querySelector('#voiceInterpretationLabRunCandidate')?.textContent).toBe('Run candidate');
    expect(root.querySelector('#voiceInterpretationLabRunAll')?.textContent).toBe('Run all');
    expect(root.querySelector('#voiceInterpretationLabRunCurrent')?.textContent).toBe('Run legacy');
    expect(root.querySelector('#voiceInterpretationLabRunBoth')).toBeNull();
    expect(localStorage.length).toBe(0);
  });

  it('runs all three profiles through the batch engine and renders raw diagnostic detail', async () => {
    const root = dialog();
    const local = capability();
    const result = diagnostic();
    const runBatch = vi.fn(async (options: RunInterpretationLabBatchOptions) => {
      options.onProgress?.({
        completed: 0,
        total: 3,
        input: result.input,
        profile: INTERPRETATION_LAB_CURRENT_PROFILE,
      });
      options.onProgress?.({
        completed: 3,
        total: 3,
        input: result.input,
        profile: INTERPRETATION_LAB_CANDIDATE_PROFILE,
        result,
      });
      return [result];
    });
    mountInterpretationLab(root, {
      runtime: { capability: () => local },
      getResolveContext: () => null,
      runBatch,
    });

    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabRunAll')?.click();
    await flushAsync();

    expect(runBatch).toHaveBeenCalledWith(expect.objectContaining({
      profiles: [
        INTERPRETATION_LAB_CURRENT_PROFILE,
        INTERPRETATION_LAB_EXPERIMENTAL_PROFILE,
        INTERPRETATION_LAB_CANDIDATE_PROFILE,
      ],
      capability: local,
      getResolveContext: expect.any(Function),
      signal: expect.any(AbortSignal),
    }));
    expect(root.querySelector('#voiceInterpretationLabStatus')?.textContent).toContain('Complete');
    expect(root.querySelector('.voice-command__lab-table')?.textContent).toContain('partial');
    expect(root.querySelector('.voice-command__lab-table')?.textContent).toContain('today');
    expect(root.querySelector('.voice-command__lab-detail-row pre')?.textContent).toBe(result.rawOutput);
    expect(localStorage.length).toBe(0);
  });

  it('runs the candidate profile independently', async () => {
    const root = dialog();
    const local = capability();
    const runBatch = vi.fn().mockResolvedValue([]);
    mountInterpretationLab(root, {
      runtime: { capability: () => local },
      getResolveContext: () => null,
      runBatch,
    });

    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabRunCandidate')?.click();
    await flushAsync();

    expect(runBatch).toHaveBeenCalledWith(expect.objectContaining({
      profiles: [INTERPRETATION_LAB_CANDIDATE_PROFILE],
    }));
  });

  it('does not start a batch or prepare automatically when local AI is not ready', async () => {
    const root = dialog();
    const local = capability();
    local.status.mockResolvedValue({ state: 'action-required', action: 'download' });
    const runBatch = vi.fn();
    mountInterpretationLab(root, {
      runtime: { capability: () => local },
      getResolveContext: () => null,
      runBatch,
    });

    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabRunCurrent')?.click();
    await flushAsync();

    expect(runBatch).not.toHaveBeenCalled();
    expect(local.prepare).not.toHaveBeenCalled();
    expect(root.querySelector('#voiceInterpretationLabStatus')?.textContent)
      .toContain('Prepare it through normal VoiceFlow first');
  });

  it('Stop aborts the active batch signal', async () => {
    const root = dialog();
    const local = capability();
    let activeSignal: AbortSignal | null = null;
    const runBatch = vi.fn((options: RunInterpretationLabBatchOptions) => {
      activeSignal = options.signal ?? null;
      return new Promise<VoiceInterpretationDiagnostic[]>((resolve) => {
        options.signal?.addEventListener('abort', () => resolve([
          diagnostic({
            provider: 'cancelled',
            envelope: { state: 'not-run' },
            canonicalParse: { state: 'not-run' },
            resolution: { state: 'not-run' },
            finalClassification: 'cancelled',
          }),
        ]), { once: true });
      });
    });
    mountInterpretationLab(root, {
      runtime: { capability: () => local },
      getResolveContext: () => null,
      runBatch,
    });

    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabRunCurrent')?.click();
    await vi.waitFor(() => expect(runBatch).toHaveBeenCalledTimes(1));
    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabStop')?.click();
    await flushAsync();

    expect(activeSignal?.aborted).toBe(true);
    expect(root.querySelector('#voiceInterpretationLabStatus')?.textContent).toContain('Stopped');
  });

  it('copies a self-contained JSON payload with diagnostics but no account server or auth state', async () => {
    const root = dialog();
    const local = capability();
    const result = diagnostic();
    const writeClipboard = vi.fn().mockResolvedValue(undefined);
    mountInterpretationLab(root, {
      runtime: { capability: () => local },
      getResolveContext: () => null,
      runBatch: vi.fn().mockResolvedValue([result]),
      writeClipboard,
      now: () => new Date('2026-08-30T20:00:00.000Z'),
    });

    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabRunCandidate')?.click();
    await flushAsync();
    root.querySelector<HTMLButtonElement>('#voiceInterpretationLabCopy')?.click();
    await flushAsync();

    expect(writeClipboard).toHaveBeenCalledTimes(1);
    const copied = writeClipboard.mock.calls[0][0] as string;
    const payload = JSON.parse(copied);
    expect(payload).toEqual(createInterpretationLabCopyPayload([result], '2026-08-30T20:00:00.000Z'));
    expect(payload).toMatchObject({
      labVersion: 1,
      results: [{
        input: result.input,
        profile: result.profile,
        rawOutput: result.rawOutput,
        classification: 'partial',
        command: 'create todo Clean the garage',
        unrepresented: ['today'],
        canonicalParse: { state: 'ok' },
        resolution: { state: 'ok' },
      }],
    });
    expect(copied).not.toMatch(/userId|projectId|serverUrl|authorization|cookie|deviceSerial/i);
    expect(localStorage.length).toBe(0);
  });
});
