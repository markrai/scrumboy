// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';
import { initI18n, resetI18nForTests } from '../i18n/index.js';
import { executeCommandIR } from './execute.js';
import { callMcpTool } from './mcp-client.js';
import { startOneShotRecognition } from './speech.js';

const de = deCatalog as Record<string, string>;
const en = enCatalog as Record<string, string>;

async function initGermanLocale() {
  resetI18nForTests();
  await initI18n({
    locale: 'de',
    loadLocale: vi.fn(async (locale: 'en' | 'de') => (locale === 'de' ? de : en)),
  });
}

describe('VoiceFlow runtime i18n', () => {
  beforeEach(async () => {
    await initGermanLocale();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    resetI18nForTests();
  });

  it('localizes speech recognition support errors', async () => {
    delete window.SpeechRecognition;
    delete window.webkitSpeechRecognition;

    await expect(startOneShotRecognition()).rejects.toThrow(de['voice.errors.speechUnavailable']);
  });

  it('localizes open todo execution configuration errors', async () => {
    await expect(executeCommandIR({
      intent: 'open_todo',
      projectId: 1,
      projectSlug: 'alpha',
      entities: { localId: 56 },
    })).rejects.toThrow(de['voice.errors.openTodoUnavailable']);
  });

  it('localizes generic MCP fallback errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('not json', { status: 200 })));
    await expect(callMcpTool('todos_get', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow(de['voice.errors.mcpInvalidResponse']);

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: false }), { status: 503 })));
    await expect(callMcpTool('todos_get', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow(
      de['voice.errors.mcpHttpFailure'].replace('{status}', '503'),
    );
  });
});
