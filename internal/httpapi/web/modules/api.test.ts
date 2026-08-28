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

    expect(fetchMock).toHaveBeenCalledWith('/api/import/trello/preview', {
      method: 'POST',
      body: raw,
      headers: {
        'Content-Type': 'application/json',
        'X-Scrumboy': '1',
      },
    });
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
});
