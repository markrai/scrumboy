// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enCatalog from '../i18n/locales/en.json';
import { initI18n, resetI18nForTests } from '../i18n/index.js';

const apiFetchMock = vi.fn();
const showConfirmDialogMock = vi.fn();
const showToastMock = vi.fn();

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../utils.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../utils.js')>();
  return {
    ...actual,
    showConfirmDialog: showConfirmDialogMock,
    showToast: showToastMock,
  };
});

const { bindApiTokensInteractions, invalidateApiTokensCache, renderApiTokensSectionHTML } = await import('./settings-api-tokens.js');

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value(this: HTMLDialogElement) {
      if (!this.hasAttribute('open')) return;
      this.removeAttribute('open');
      this.dispatchEvent(new Event('close'));
    },
  });
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

describe('settings-api-tokens', () => {
  beforeEach(async () => {
    document.body.innerHTML = '';
    installDialogPolyfill();
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    apiFetchMock.mockReset();
    showConfirmDialogMock.mockReset();
    showToastMock.mockClear();
    invalidateApiTokensCache();
    resetI18nForTests();
    await initI18n({
      locale: 'en',
      loadLocale: async () => enCatalog as Record<string, string>,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it('renders tokens with active/service/revoked badges and a Revoke action only on live tokens', async () => {
    apiFetchMock.mockResolvedValue({
      items: [
        { id: 1, name: 'CI pipeline', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: '2026-02-01T00:00:00Z', isService: true },
        { id: 2, name: null, createdAt: '2026-01-02T00:00:00Z', isService: false },
        { id: 3, name: 'Old token', createdAt: '2026-01-03T00:00:00Z', revokedAt: '2026-01-04T00:00:00Z', isService: false },
      ],
    });

    const html = await renderApiTokensSectionHTML();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/me/tokens');
    document.body.innerHTML = html;

    expect(document.body.textContent).toContain('CI pipeline');
    expect(document.body.textContent).toContain('(unnamed)');
    expect(document.body.textContent).toContain('Old token');
    expect(document.querySelectorAll('.status-pill--service').length).toBe(1);
    expect(document.querySelectorAll('.status-pill--revoked').length).toBe(1);
    expect(document.querySelectorAll('.status-pill--active').length).toBe(2);

    const revokeButtons = document.querySelectorAll('[data-action="revoke-api-token"]');
    expect(revokeButtons.length).toBe(2);
    expect(document.querySelector('[data-action="revoke-api-token"][data-token-id="1"]')).not.toBeNull();
    expect(document.querySelector('[data-action="revoke-api-token"][data-token-id="2"]')).not.toBeNull();
    expect(document.querySelector('[data-action="revoke-api-token"][data-token-id="3"]')).toBeNull();

    apiFetchMock.mockClear();
    const cachedHtml = await renderApiTokensSectionHTML();
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(cachedHtml).toBe(html);

    invalidateApiTokensCache();
    await renderApiTokensSectionHTML();
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
  });

  it('shows the empty state when there are no tokens', async () => {
    apiFetchMock.mockResolvedValue({ items: [] });

    const html = await renderApiTokensSectionHTML();
    expect(html).toContain('No API tokens yet.');
    expect(html).not.toContain('api-tokens-table');
  });

  it('shows a load error message and does not cache a failed fetch', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('boom'));

    const html = await renderApiTokensSectionHTML();
    expect(html).toContain('boom');

    apiFetchMock.mockResolvedValueOnce({ items: [] });
    const retryHtml = await renderApiTokensSectionHTML();
    expect(apiFetchMock).toHaveBeenCalledTimes(2);
    expect(retryHtml).toContain('No API tokens yet.');
  });

  it('creates a token, shows the secret once in a dialog, and rerenders the list', async () => {
    apiFetchMock.mockResolvedValueOnce({
      id: 9,
      name: 'new one',
      createdAt: '2026-01-01T00:00:00Z',
      isService: true,
      token: 'sb_abcdef123456',
    });
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <form id="createApiTokenForm">
        <input type="text" id="createApiTokenName" value="new one" />
        <input type="checkbox" id="createApiTokenService" checked />
        <button type="submit" id="createApiTokenSubmit">Create token</button>
      </form>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const form = document.getElementById('createApiTokenForm');
    if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/me/tokens', {
      method: 'POST',
      body: JSON.stringify({ name: 'new one', isService: true }),
    });
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(showToastMock).toHaveBeenCalledWith('Token created.');

    const secretInput = document.getElementById('apiTokenCreatedDisplay');
    if (!(secretInput instanceof HTMLInputElement)) throw new Error('missing created-token secret display');
    expect(secretInput.value).toBe('sb_abcdef123456');
    expect(document.querySelector('.dialog__title')?.textContent).toBe('Token created');

    const copyBtn = document.getElementById('apiTokenCreatedCopy');
    if (!(copyBtn instanceof HTMLElement)) throw new Error('missing copy button');
    copyBtn.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('sb_abcdef123456');
    expect(showToastMock).toHaveBeenCalledWith('Token copied to clipboard');

    const doneBtn = document.getElementById('apiTokenCreatedDone');
    if (!(doneBtn instanceof HTMLElement)) throw new Error('missing done button');
    doneBtn.dispatchEvent(new Event('click', { bubbles: true }));
    expect(document.getElementById('apiTokenCreatedDisplay')).toBeNull();
  });

  it('omits an empty name from the create request instead of sending an empty string', async () => {
    apiFetchMock.mockResolvedValueOnce({ id: 10, createdAt: '2026-01-01T00:00:00Z', isService: false, token: 'sb_x' });
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <form id="createApiTokenForm">
        <input type="text" id="createApiTokenName" value="   " />
        <input type="checkbox" id="createApiTokenService" />
        <button type="submit" id="createApiTokenSubmit">Create token</button>
      </form>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const form = document.getElementById('createApiTokenForm');
    if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/me/tokens', {
      method: 'POST',
      body: JSON.stringify({ isService: false }),
    });
  });

  it('shows the checkbox copy that reflects the token being deleted, not scoped down, on account deletion', async () => {
    apiFetchMock.mockResolvedValue({ items: [] });
    const html = await renderApiTokensSectionHTML();
    expect(html).toContain('if this account is deleted, this token is deleted too');
    expect(html).not.toContain('its record survives');
  });

  it('still shows the created secret even when the post-create rerender rejects, and does not report it as a failure', async () => {
    apiFetchMock.mockResolvedValueOnce({
      id: 9,
      name: 'new one',
      createdAt: '2026-01-01T00:00:00Z',
      isService: true,
      token: 'sb_abcdef123456',
    });
    const rerender = vi.fn().mockRejectedValue(new Error('rerender exploded'));

    document.body.innerHTML = `
      <form id="createApiTokenForm">
        <input type="text" id="createApiTokenName" value="new one" />
        <input type="checkbox" id="createApiTokenService" checked />
        <button type="submit" id="createApiTokenSubmit">Create token</button>
      </form>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const form = document.getElementById('createApiTokenForm');
    if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();

    const secretInput = document.getElementById('apiTokenCreatedDisplay');
    if (!(secretInput instanceof HTMLInputElement)) throw new Error('missing created-token secret display');
    expect(secretInput.value).toBe('sb_abcdef123456');
    expect(showToastMock).toHaveBeenCalledWith('Token created.');
    expect(showToastMock).not.toHaveBeenCalledWith('Failed to create token.');

    const submitBtn = document.getElementById('createApiTokenSubmit');
    if (!(submitBtn instanceof HTMLButtonElement)) throw new Error('missing submit button');
    expect(submitBtn.disabled).toBe(false);
  });

  it.each([
    ['null response', null, null],
    ['missing token with an id', { id: 11, name: 'ghost', createdAt: '2026-01-01T00:00:00Z', isService: false }, 11],
    ['empty token', { id: 12, name: 'ghost', createdAt: '2026-01-01T00:00:00Z', isService: false, token: '' }, 12],
    ['non-string token', { id: 13, name: 'ghost', createdAt: '2026-01-01T00:00:00Z', isService: false, token: 123 }, 13],
  ] as const)('never shows ordinary success for a malformed 2xx create response: %s', async (_case, response, expectedRevokeId) => {
    apiFetchMock.mockResolvedValueOnce(response);
    if (expectedRevokeId !== null) apiFetchMock.mockResolvedValueOnce(undefined); // best-effort DELETE
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <form id="createApiTokenForm">
        <input type="text" id="createApiTokenName" value="ghost" />
        <input type="checkbox" id="createApiTokenService" />
        <button type="submit" id="createApiTokenSubmit">Create token</button>
      </form>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const form = document.getElementById('createApiTokenForm');
    if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(document.getElementById('apiTokenCreatedDisplay')).toBeNull();
    expect(showToastMock).toHaveBeenCalledWith('Token creation returned an unexpected response. A token may have been created — check your list and revoke it if unused.');
    expect(showToastMock).not.toHaveBeenCalledWith('Token created.');
    if (expectedRevokeId === null) {
      expect(apiFetchMock).toHaveBeenCalledTimes(1);
    } else {
      expect(apiFetchMock).toHaveBeenCalledWith(`/api/me/tokens/${expectedRevokeId}`, { method: 'DELETE' });
    }
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('re-enables the submit button and shows an error toast when creation fails', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('nope'));
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <form id="createApiTokenForm">
        <input type="text" id="createApiTokenName" value="broken" />
        <input type="checkbox" id="createApiTokenService" />
        <button type="submit" id="createApiTokenSubmit">Create token</button>
      </form>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const form = document.getElementById('createApiTokenForm');
    const submitBtn = document.getElementById('createApiTokenSubmit');
    if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
    if (!(submitBtn instanceof HTMLButtonElement)) throw new Error('missing submit button');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(rerender).not.toHaveBeenCalled();
    expect(submitBtn.disabled).toBe(false);
    expect(submitBtn.textContent).toBe('Create token');
  });

  it('revokes a token after confirmation and rerenders', async () => {
    showConfirmDialogMock.mockResolvedValueOnce(true);
    apiFetchMock.mockResolvedValueOnce(undefined);
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    document.querySelector('[data-action="revoke-api-token"]')?.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      "This immediately invalidates CI pipeline. Anything still using it will stop working. This can't be undone.",
      'Revoke API token',
      'Revoke',
    );
    expect(apiFetchMock).toHaveBeenCalledWith('/api/me/tokens/42', { method: 'DELETE' });
    expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('does not revoke when the confirmation is declined', async () => {
    showConfirmDialogMock.mockResolvedValueOnce(false);
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    document.querySelector('[data-action="revoke-api-token"]')?.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('re-enables the revoke button after a non-404 failure so the user can retry', async () => {
    showConfirmDialogMock.mockResolvedValue(true);
    apiFetchMock.mockRejectedValueOnce(new Error('server exploded'));
    apiFetchMock.mockResolvedValueOnce(undefined);
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const btn = document.querySelector('[data-action="revoke-api-token"]');
    if (!(btn instanceof HTMLButtonElement)) throw new Error('missing revoke button');
    btn.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(rerender).not.toHaveBeenCalled();
    expect(showToastMock).toHaveBeenCalledWith('server exploded');
    expect(btn.disabled).toBe(false);

    btn.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledTimes(2);
    expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['a successful DELETE', null],
    ['an already-revoked 404', Object.assign(new Error('not found'), { status: 404 })],
  ])('keeps %s final when the best-effort rerender rejects', async (_case, deleteError) => {
    showConfirmDialogMock.mockResolvedValueOnce(true);
    if (deleteError) apiFetchMock.mockRejectedValueOnce(deleteError);
    else apiFetchMock.mockResolvedValueOnce(undefined);
    const rerender = vi.fn().mockRejectedValue(new Error('rerender exploded'));

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const btn = document.querySelector('[data-action="revoke-api-token"]');
    if (!(btn instanceof HTMLButtonElement)) throw new Error('missing revoke button');
    btn.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
    expect(showToastMock).not.toHaveBeenCalledWith('rerender exploded');
    expect(showToastMock).not.toHaveBeenCalledWith('Failed to revoke token.');
    expect(btn.disabled).toBe(true);
  });

  it('does not issue a second DELETE for the same token while the first confirmed revoke is still pending', async () => {
    showConfirmDialogMock.mockResolvedValue(true);
    let resolveDelete!: () => void;
    apiFetchMock.mockImplementationOnce(
      () => new Promise((resolve) => { resolveDelete = () => resolve(undefined); }),
    );
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    const btn = document.querySelector('[data-action="revoke-api-token"]');
    btn?.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();
    // First DELETE is now pending. A second confirmed click for the same token must not fire another.
    btn?.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledTimes(1);

    resolveDelete();
    await flushPromises();
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('treats a 404 on revoke as the token already being gone, not a failure', async () => {
    showConfirmDialogMock.mockResolvedValueOnce(true);
    const notFound = Object.assign(new Error('not found'), { status: 404 });
    apiFetchMock.mockRejectedValueOnce(notFound);
    const rerender = vi.fn().mockResolvedValue(undefined);

    document.body.innerHTML = `
      <button data-action="revoke-api-token" data-token-id="42" data-token-name="CI pipeline">Revoke</button>
    `;
    bindApiTokensInteractions({ signal: new AbortController().signal, rerender });

    document.querySelector('[data-action="revoke-api-token"]')?.dispatchEvent(new Event('click', { bubbles: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
    expect(showToastMock).not.toHaveBeenCalledWith('Failed to revoke token.');
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('discards a stale in-flight GET so it cannot overwrite a newer cache written after invalidation', async () => {
    let resolveStale!: (value: { items: { id: number }[] }) => void;
    apiFetchMock.mockImplementationOnce(
      () => new Promise((resolve) => { resolveStale = resolve; }),
    );

    const stalePromise = renderApiTokensSectionHTML(); // GET A starts (old generation)

    invalidateApiTokensCache(); // a create/revoke invalidates the cache mid-flight

    apiFetchMock.mockResolvedValueOnce({ items: [{ id: 99 }] });
    const freshHtml = await renderApiTokensSectionHTML(); // GET B starts and resolves first (new generation)
    expect(freshHtml).toContain('data-token-id="99"');

    resolveStale({ items: [{ id: 1 }] }); // GET A resolves afterward, with stale data
    await stalePromise;

    apiFetchMock.mockClear();
    const cachedHtml = await renderApiTokensSectionHTML();
    expect(apiFetchMock).not.toHaveBeenCalled(); // still cached — B's result, not A's
    expect(cachedHtml).toContain('data-token-id="99"');
    expect(cachedHtml).not.toContain('data-token-id="1"');
  });

  it('discards a stale in-flight GET rejection after a newer generation succeeds', async () => {
    let rejectStale!: (reason: Error) => void;
    apiFetchMock.mockImplementationOnce(
      () => new Promise((_resolve, reject) => { rejectStale = reject; }),
    );

    const stalePromise = renderApiTokensSectionHTML(); // GET A starts (old generation)

    invalidateApiTokensCache();

    apiFetchMock.mockResolvedValueOnce({ items: [{ id: 99 }] });
    const freshHtml = await renderApiTokensSectionHTML(); // GET B succeeds (new generation)
    expect(freshHtml).toContain('data-token-id="99"');

    rejectStale(new Error('stale request failed')); // GET A rejects afterward
    const recoveredStaleHtml = await stalePromise;

    expect(recoveredStaleHtml).toContain('data-token-id="99"');
    expect(recoveredStaleHtml).not.toContain('stale request failed');

    apiFetchMock.mockClear();
    const cachedHtml = await renderApiTokensSectionHTML();
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(cachedHtml).toContain('data-token-id="99"');
  });
});
