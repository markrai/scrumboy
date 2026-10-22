// @vitest-environment jsdom
// Note images on Capacitor: every native resource handle acquired by the Markdown preview is
// released exactly once, including handles that resolve after the preview was released.
import createDOMPurify from 'dompurify';
import MarkdownIt from 'markdown-it';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ScrumboyTransportPlugin } from '../../../../mobile/capacitor/shell/native-plugin.js';
import { NativeServerTransport } from '../../../../mobile/capacitor/shell/native-server-transport.js';
import { createCapacitorRuntime } from '../../../../mobile/capacitor/shell/native-runtime.js';
import { installAppRuntime, resetAppRuntimeForTests } from '../modules/platform/runtime.js';
import { releaseMarkdownPreviewResources, renderMarkdownPreviewInto } from '../modules/markdown-preview.js';

const A = '/api/assets/11111111-1111-4111-8111-111111111111';
const B = '/api/assets/22222222-2222-4222-9222-222222222222';

describe('capacitor note images', () => {
  let resolvers: Array<() => void>;
  let plugin: ScrumboyTransportPlugin;

  beforeEach(() => {
    (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
    (window as any).DOMPurify = createDOMPurify(window);
    resolvers = [];
    let next = 0;
    plugin = {
      probeServer: vi.fn(),
      configure: vi.fn(),
      request: vi.fn(),
      cancelRequest: vi.fn(async () => undefined),
      openEventStream: vi.fn(async () => undefined),
      closeEventStream: vi.fn(async () => undefined),
      // Each acquisition resolves only when the test says so (late resolution after abort included).
      acquireResource: vi.fn(() => new Promise((resolve) => {
        const handle = `h${next++}`;
        resolvers.push(() => resolve({ handle, fileUri: `file:///cache/${handle}` }));
      })),
      releaseResource: vi.fn(async () => undefined),
      logout: vi.fn(async () => undefined),
      resetForServerChange: vi.fn(async () => undefined),
      addListener: vi.fn(async () => ({ remove: vi.fn(async () => undefined) })),
    } as unknown as ScrumboyTransportPlugin;
    const transport = new NativeServerTransport({ plugin, convertFileSrc: (uri) => uri.replace('file://', 'https://localhost/_capacitor_file_') });
    installAppRuntime(createCapacitorRuntime('https://server.example', transport));
  });

  afterEach(() => resetAppRuntimeForTests());

  it('releases every native handle after re-render and close, including late resolutions', async () => {
    const container = document.createElement('div');
    const first = renderMarkdownPreviewInto(container, `![a](${A}) ![b](${B})`, { inlineImagesEnabled: true });
    await vi.waitFor(() => expect(resolvers).toHaveLength(2));
    resolvers[0]();
    await vi.waitFor(() => expect(container.querySelector('img')?.getAttribute('src')).toContain('_capacitor_file_'));

    // Re-render while the second download is still pending, then resolve it late.
    const second = renderMarkdownPreviewInto(container, `![a](${A})`, { inlineImagesEnabled: true });
    resolvers[1]();
    await vi.waitFor(() => expect(resolvers).toHaveLength(3));
    resolvers[2]();
    await Promise.all([first, second]);

    releaseMarkdownPreviewResources(container);
    releaseMarkdownPreviewResources(container);

    await vi.waitFor(() => expect(plugin.releaseResource).toHaveBeenCalledTimes(3));
    const released = (plugin.releaseResource as any).mock.calls.map((c: any[]) => c[0].handle).sort();
    expect(released).toEqual(['h0', 'h1', 'h2']);
  });
});
