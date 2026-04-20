import { afterEach, describe, expect, it, vi } from 'vitest';
import { callMcpTool } from './mcp-client.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(response: Response): ReturnType<typeof vi.fn> {
  const fetchMock = vi.fn().mockResolvedValue(response);
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('voice MCP client', () => {
  it('returns data from valid legacy MCP envelopes', async () => {
    stubFetch(new Response(JSON.stringify({ ok: true, data: { value: 1 }, meta: {} }), { status: 200 }));

    await expect(callMcpTool('todos.get', { projectSlug: 'alpha', localId: 1 })).resolves.toEqual({ value: 1 });
  });

  it('throws server MCP errors with the server message', async () => {
    stubFetch(new Response(JSON.stringify({ ok: false, error: { message: 'forbidden' } }), { status: 403 }));

    await expect(callMcpTool('todos.delete', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow('forbidden');
  });

  it('rejects invalid JSON and malformed envelopes', async () => {
    stubFetch(new Response('not json', { status: 200 }));
    await expect(callMcpTool('todos.get', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow('Invalid MCP response');

    stubFetch(new Response(JSON.stringify({ data: {} }), { status: 200 }));
    await expect(callMcpTool('todos.get', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow('Invalid MCP response');

    stubFetch(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    await expect(callMcpTool('todos.get', { projectSlug: 'alpha', localId: 1 })).rejects.toThrow('Invalid MCP response');
  });

  it('passes abort signals to fetch', async () => {
    const response = new Response(JSON.stringify({ ok: true, data: {} }), { status: 200 });
    const fetchMock = stubFetch(response);
    const controller = new AbortController();

    await callMcpTool('todos.get', { projectSlug: 'alpha', localId: 1 }, { signal: controller.signal });

    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);
  });
});
