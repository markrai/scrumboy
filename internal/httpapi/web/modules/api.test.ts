import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('apiFetch', () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.resetModules();
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('passes string bodies through to fetch unchanged', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ok: true }),
    });

    const { apiFetch } = await import('./api.js');
    const raw = '{"id":"board-raw","name":"Raw Trello"}';
    await apiFetch('/api/import/trello/preview', {
      method: 'POST',
      body: raw,
    });

    expect(fetchMock).toHaveBeenCalledWith('/api/import/trello/preview', expect.objectContaining({
      method: 'POST',
      body: raw,
    }));
    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(headers.get('X-Scrumboy')).toBe('1');
  });

  it.each([
    ['plain object', { 'X-Trace': 'object' }],
    ['Headers', new Headers({ 'X-Trace': 'headers' })],
    ['tuple array', [['X-Trace', 'tuples']] as [string, string][]],
  ])('merges defaults with %s custom headers', async (_name, customHeaders) => {
    fetchMock.mockResolvedValue({ ok: true, status: 200, json: async () => ({ ok: true }) });
    const { apiFetch } = await import('./api.js');

    await apiFetch('/api/header-shapes', { headers: customHeaders });

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get('X-Scrumboy')).toBe('1');
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(headers.get('X-Trace')).toBe(new Headers(customHeaders).get('X-Trace'));
  });

  it('preserves intentional caller overrides of default headers', async () => {
    fetchMock.mockResolvedValue({ ok: true, status: 200, json: async () => ({ ok: true }) });
    const { apiFetch } = await import('./api.js');

    await apiFetch('/api/header-overrides', {
      headers: new Headers({ 'Content-Type': 'text/plain', 'X-Scrumboy': 'caller-value' }),
    });

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get('Content-Type')).toBe('text/plain');
    expect(headers.get('X-Scrumboy')).toBe('caller-value');
  });

  it('preserves status/data errors and 204 responses', async () => {
    fetchMock
      .mockResolvedValueOnce({ ok: true, status: 204, json: vi.fn() })
      .mockResolvedValueOnce({
        ok: false,
        status: 409,
        json: async () => ({ error: { message: 'conflict' }, detail: 'kept' }),
      });
    const { apiFetch } = await import('./api.js');

    await expect(apiFetch('/api/empty')).resolves.toBeNull();
    await expect(apiFetch('/api/conflict')).rejects.toMatchObject({
      message: 'conflict',
      status: 409,
      data: { error: { message: 'conflict' }, detail: 'kept' },
    });
  });

  it('preserves multipart form data and lets fetch create the boundary', async () => {
    fetchMock.mockResolvedValue({ ok: true, status: 200, json: async () => ({ rev: 2 }) });
    const { apiFetchForm } = await import('./api.js');
    const form = new FormData();
    form.append('file', new Blob(['image']), 'wallpaper.jpg');

    await expect(apiFetchForm('/api/user/wallpaper/image', form)).resolves.toEqual({ rev: 2 });
    expect(fetchMock).toHaveBeenCalledWith('/api/user/wallpaper/image', {
      method: 'POST',
      headers: { 'X-Scrumboy': '1' },
      body: form,
    });
    expect(fetchMock.mock.calls[0][1].headers).not.toHaveProperty('Content-Type');
  });

  it('does not add a JSON content type when apiFetch receives FormData', async () => {
    fetchMock.mockResolvedValue({ ok: true, status: 200, json: async () => ({ ok: true }) });
    const { apiFetch } = await import('./api.js');
    const form = new FormData();
    form.append('file', new Blob(['image']), 'wallpaper.jpg');

    await apiFetch('/api/form-through-generic-helper', {
      method: 'POST',
      body: form,
      headers: [['X-Trace', 'multipart']],
    });

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get('Content-Type')).toBeNull();
    expect(headers.get('X-Scrumboy')).toBe('1');
    expect(headers.get('X-Trace')).toBe('multipart');
  });

  it('uses the cursor archive endpoint and atomic batch request shapes', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ todos: [], hasMore: false }),
    });
    const { archiveTodos, listArchivedTodos, restoreTodos } = await import('./api.js');

    await listArchivedTodos('alpha board', { limit: 50, afterCursor: '123:9' });
    await archiveTodos('alpha', [12, 14]);
    await restoreTodos('alpha', [19]);

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/board/alpha%20board/archive?limit=50&afterCursor=123%3A9', expect.any(Object));
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/board/alpha/todos/archive', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ localIds: [12, 14] }),
    }));
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/board/alpha/todos/restore', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ localIds: [19] }),
    }));
    for (const call of fetchMock.mock.calls) {
      const headers = new Headers(call[1].headers);
      expect(headers.get('Content-Type')).toBe('application/json');
      expect(headers.get('X-Scrumboy')).toBe('1');
    }
  });
});
