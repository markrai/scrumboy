// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';
import { initI18n, resetI18nForTests, setLocale } from '../i18n/index.js';

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

const {
  bindServiceAccountsInteractions,
  fetchAllPages,
  invalidateServiceAccountsLifecycle,
  MAX_LIST_PAGES,
  renderServiceAccountsSectionHTML,
} = await import('./settings-service-accounts.js');

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

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

const SAMPLE_ACCOUNT = {
  userId: 7,
  stableUserId: 'sa-7',
  name: 'plans-bot',
  createdAt: '2026-01-15T12:00:00Z',
};

async function initTestI18n(): Promise<void> {
  resetI18nForTests();
  await initI18n({
    locale: 'en',
    loadLocale: async (locale) => {
      if (locale === 'de') return deCatalog as Record<string, string>;
      return enCatalog as Record<string, string>;
    },
  });
}

function mockAccountsList(
  items: typeof SAMPLE_ACCOUNT[] = [SAMPLE_ACCOUNT],
  nextCursor: string | null = null,
): void {
  apiFetchMock.mockResolvedValueOnce({ items, nextCursor });
}

async function mountOwnerSection(
  items: typeof SAMPLE_ACCOUNT[] = [SAMPLE_ACCOUNT],
  isOwner = true,
): Promise<ReturnType<typeof vi.fn>> {
  mockAccountsList(items);
  const rerender = vi.fn().mockResolvedValue(undefined);
  const html = await renderServiceAccountsSectionHTML(isOwner);
  document.body.innerHTML = html;
  bindServiceAccountsInteractions({
    signal: new AbortController().signal,
    rerender,
    isOwner,
  });
  return rerender;
}

type SampleToken = {
  tokenId: number;
  userId: number;
  name?: string | null;
  createdAt: string;
  lastUsedAt?: string | null;
  revokedAt?: string | null;
};

async function openTokensDialog(
  isOwner = true,
  tokens: SampleToken[] = [],
): Promise<void> {
  await mountOwnerSection([SAMPLE_ACCOUNT], isOwner);
  apiFetchMock.mockResolvedValueOnce({ items: tokens, nextCursor: null });
  const tokensBtn = document.querySelector('[data-action="sa-tokens"]');
  if (!(tokensBtn instanceof HTMLElement)) throw new Error('missing tokens button');
  tokensBtn.dispatchEvent(new Event('click', { bubbles: true }));
  await flushPromises();
}

describe('settings-service-accounts', () => {
  beforeEach(async () => {
    document.body.innerHTML = '';
    installDialogPolyfill();
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    Object.defineProperty(HTMLInputElement.prototype, 'select', {
      configurable: true,
      value: vi.fn(),
    });
    apiFetchMock.mockReset();
    showConfirmDialogMock.mockReset();
    showToastMock.mockClear();
    invalidateServiceAccountsLifecycle();
    await initTestI18n();
  });

  afterEach(() => {
    invalidateServiceAccountsLifecycle();
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  describe('renderServiceAccountsSectionHTML', () => {
    it('shows Delete and Tokens for owner; Tokens only for admin', async () => {
      mockAccountsList([SAMPLE_ACCOUNT]);
      const ownerHtml = await renderServiceAccountsSectionHTML(true);
      expect(ownerHtml).toContain('data-action="sa-delete"');
      expect(ownerHtml).toContain('data-action="sa-tokens"');

      apiFetchMock.mockReset();
      mockAccountsList([SAMPLE_ACCOUNT]);
      const adminHtml = await renderServiceAccountsSectionHTML(false);
      expect(adminHtml).toContain('data-action="sa-tokens"');
      expect(adminHtml).not.toContain('data-action="sa-delete"');
    });

    it('shows the empty state when there are no service accounts', async () => {
      mockAccountsList([]);
      const html = await renderServiceAccountsSectionHTML(true);
      expect(html).toContain('No service accounts yet.');
      expect(html).not.toContain('users-table');
    });

    it('shows a load error message when listing fails', async () => {
      apiFetchMock.mockRejectedValueOnce(new Error('list exploded'));
      const html = await renderServiceAccountsSectionHTML(true);
      expect(html).toContain('list exploded');
    });

    it('escapes hostile account names in the table and action button attributes', async () => {
      const hostile = '<img src=x onerror=alert(1)>';
      mockAccountsList([{ ...SAMPLE_ACCOUNT, name: hostile }]);
      const html = await renderServiceAccountsSectionHTML(true);
      expect(html).not.toContain('<img src=x');
      expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;');
      expect(html).toContain(
        `data-sa-name="&lt;img src=x onerror=alert(1)&gt;"`,
      );
    });
  });

  describe('fetchAllPages', () => {
    it('follows nextCursor until it is null and merges all items', async () => {
      apiFetchMock
        .mockResolvedValueOnce({
          items: [{ id: 1 }],
          nextCursor: 'page2',
        })
        .mockResolvedValueOnce({
          items: [{ id: 2 }],
          nextCursor: null,
        });

      const items = await fetchAllPages<{ id: number }>('/api/admin/service-accounts');
      expect(items).toEqual([{ id: 1 }, { id: 2 }]);
      expect(apiFetchMock).toHaveBeenNthCalledWith(
        1,
        '/api/admin/service-accounts?limit=100',
      );
      expect(apiFetchMock).toHaveBeenNthCalledWith(
        2,
        '/api/admin/service-accounts?limit=100&cursor=page2',
      );
    });

    it('uses & for cursor when the path already has a query string', async () => {
      apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });
      await fetchAllPages('/api/admin/service-accounts?foo=bar');
      expect(apiFetchMock).toHaveBeenCalledWith(
        '/api/admin/service-accounts?foo=bar&limit=100',
      );
    });

    it('throws when the API returns a repeated cursor', async () => {
      apiFetchMock
        .mockResolvedValueOnce({ items: [{ id: 1 }], nextCursor: 'stuck' })
        .mockResolvedValueOnce({ items: [{ id: 2 }], nextCursor: 'stuck' });

      await expect(fetchAllPages('/api/admin/service-accounts')).rejects.toThrow(
        'service-account list returned a repeated cursor',
      );
    });

    it('stops after a single page when nextCursor is absent', async () => {
      apiFetchMock.mockResolvedValueOnce({
        items: [{ userId: 1 }],
        nextCursor: undefined,
      });
      const items = await fetchAllPages('/api/admin/service-accounts');
      expect(items).toEqual([{ userId: 1 }]);
      expect(apiFetchMock).toHaveBeenCalledTimes(1);
    });

    it('returns a complete list when the final page is exactly at MAX_LIST_PAGES', async () => {
      for (let i = 0; i < MAX_LIST_PAGES; i++) {
        const nextCursor = i < MAX_LIST_PAGES - 1 ? `c${i + 1}` : null;
        apiFetchMock.mockResolvedValueOnce({
          items: [{ id: i }],
          nextCursor,
        });
      }
      const items = await fetchAllPages<{ id: number }>('/api/admin/service-accounts');
      expect(items).toHaveLength(MAX_LIST_PAGES);
      expect(apiFetchMock).toHaveBeenCalledTimes(MAX_LIST_PAGES);
    });

    it('throws an incomplete-list failure when more results remain after MAX_LIST_PAGES', async () => {
      for (let i = 0; i < MAX_LIST_PAGES; i++) {
        apiFetchMock.mockResolvedValueOnce({
          items: [{ id: i }],
          nextCursor: `c${i + 1}`,
        });
      }
      await expect(fetchAllPages('/api/admin/service-accounts')).rejects.toThrow(
        'service-account list incomplete: page limit reached while more results remain',
      );
      expect(apiFetchMock).toHaveBeenCalledTimes(MAX_LIST_PAGES);
    });
  });

  describe('create service account', () => {
    it('creates a service account, toasts success, and rerenders', async () => {
      const rerender = await mountOwnerSection([]);
      apiFetchMock.mockResolvedValueOnce(undefined);

      const form = document.getElementById('createServiceAccountForm');
      const nameInput = document.getElementById('createServiceAccountName');
      if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
      if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing name input');
      nameInput.value = 'new-bot';

      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      expect(apiFetchMock).toHaveBeenCalledWith('/api/admin/service-accounts', {
        method: 'POST',
        body: JSON.stringify({ name: 'new-bot' }),
      });
      expect(showToastMock).toHaveBeenCalledWith('Service account created.');
      expect(rerender).toHaveBeenCalledTimes(1);
    });

    it('re-enables submit and shows an error toast when creation fails', async () => {
      const rerender = await mountOwnerSection([]);
      apiFetchMock.mockRejectedValueOnce(new Error('create failed'));

      const form = document.getElementById('createServiceAccountForm');
      const submit = document.getElementById('createServiceAccountSubmit');
      const nameInput = document.getElementById('createServiceAccountName');
      if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
      if (!(submit instanceof HTMLButtonElement)) throw new Error('missing submit');
      if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing name input');
      nameInput.value = 'broken';

      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      expect(rerender).not.toHaveBeenCalled();
      expect(showToastMock).toHaveBeenCalledWith('create failed');
      expect(submit.disabled).toBe(false);
    });

    it('does not issue a second POST while the first create is still in flight', async () => {
      await mountOwnerSection([]);
      apiFetchMock.mockClear();
      let resolveCreate!: () => void;
      apiFetchMock.mockImplementationOnce(
        () => new Promise((resolve) => {
          resolveCreate = () => resolve(undefined);
        }),
      );

      const form = document.getElementById('createServiceAccountForm');
      const nameInput = document.getElementById('createServiceAccountName');
      const submit = document.getElementById('createServiceAccountSubmit');
      if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
      if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing name input');
      if (!(submit instanceof HTMLButtonElement)) throw new Error('missing submit');
      nameInput.value = 'dup-test';

      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      expect(submit.disabled).toBe(true);
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      const createPosts = apiFetchMock.mock.calls.filter(
        (call) => call[1]?.method === 'POST',
      );
      expect(createPosts.length).toBe(1);
      resolveCreate();
      await flushPromises();
    });

    it('does not rerender Settings when create completes after lifecycle teardown', async () => {
      mockAccountsList([]);
      const rerender = vi.fn().mockResolvedValue(undefined);
      const html = await renderServiceAccountsSectionHTML(true);
      document.body.innerHTML = html;
      const controller = new AbortController();
      bindServiceAccountsInteractions({
        signal: controller.signal,
        rerender,
        isOwner: true,
      });

      let resolveCreate!: () => void;
      apiFetchMock.mockImplementationOnce(
        () => new Promise((resolve) => {
          resolveCreate = () => resolve(undefined);
        }),
      );

      const form = document.getElementById('createServiceAccountForm');
      const nameInput = document.getElementById('createServiceAccountName');
      if (!(form instanceof HTMLFormElement)) throw new Error('missing create form');
      if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing name input');
      nameInput.value = 'late-bot';
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      controller.abort();
      invalidateServiceAccountsLifecycle();
      resolveCreate();
      await flushPromises();

      expect(rerender).not.toHaveBeenCalled();
      expect(showToastMock).not.toHaveBeenCalledWith('Service account created.');
    });
  });

  describe('delete service account', () => {
    it('deletes after confirmation and rerenders', async () => {
      showConfirmDialogMock.mockResolvedValueOnce(true);
      const rerender = await mountOwnerSection();
      apiFetchMock.mockResolvedValueOnce(undefined);

      const deleteBtn = document.querySelector('[data-action="sa-delete"]');
      if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing delete button');
      deleteBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      expect(showConfirmDialogMock).toHaveBeenCalledWith(
        "This deletes plans-bot, revokes all of its tokens and removes its project memberships. This can't be undone.",
        'Delete service account',
        'Delete',
      );
      expect(apiFetchMock).toHaveBeenCalledWith('/api/admin/service-accounts/7', {
        method: 'DELETE',
      });
      expect(showToastMock).toHaveBeenCalledWith('Service account deleted.');
      expect(rerender).toHaveBeenCalledTimes(1);
    });

    it('does not delete when confirmation is declined', async () => {
      showConfirmDialogMock.mockResolvedValueOnce(false);
      const rerender = await mountOwnerSection();

      document.querySelector('[data-action="sa-delete"]')?.dispatchEvent(
        new Event('click', { bubbles: true }),
      );
      await flushPromises();

      expect(apiFetchMock).toHaveBeenCalledTimes(1);
      expect(rerender).not.toHaveBeenCalled();
    });

    it('re-enables the delete button after a failed DELETE', async () => {
      showConfirmDialogMock.mockResolvedValueOnce(true);
      await mountOwnerSection();
      apiFetchMock.mockRejectedValueOnce(new Error('delete nope'));

      const deleteBtn = document.querySelector('[data-action="sa-delete"]');
      if (!(deleteBtn instanceof HTMLButtonElement)) throw new Error('missing delete button');
      deleteBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      expect(showToastMock).toHaveBeenCalledWith('delete nope');
      expect(deleteBtn.disabled).toBe(false);
    });
  });

  describe('service account tokens dialog', () => {
    it('shows the mint form for owner and owner-only message for admin', async () => {
      await openTokensDialog(true);
      expect(document.getElementById('saTokenMintForm')).not.toBeNull();
      expect(document.body.textContent).not.toContain(
        'Only the system owner can create service tokens.',
      );

      document.body.innerHTML = '';
      apiFetchMock.mockReset();
      await openTokensDialog(false);
      expect(document.getElementById('saTokenMintForm')).toBeNull();
      expect(document.body.textContent).toContain(
        'Only the system owner can create service tokens.',
      );
    });

    it('sets the dialog title from t() with the account name', async () => {
      await openTokensDialog(true);
      const title = document.getElementById('saTokensTitle');
      expect(title?.textContent).toBe('Tokens for plans-bot');
    });

    it('updates the dialog title when the locale changes via bindDialogLocale sync', async () => {
      await openTokensDialog(true);
      await setLocale('de');
      const title = document.getElementById('saTokensTitle');
      expect(title?.textContent).toBe('Token für plans-bot');
    });

    it('hydrates dynamically inserted token rows after the tokens dialog opens', async () => {
      await openTokensDialog(true, [
        {
          tokenId: 1,
          userId: 7,
          name: 'prod',
          createdAt: '2026-01-01T00:00:00Z',
          revokedAt: null,
        },
        {
          tokenId: 2,
          userId: 7,
          name: 'old',
          createdAt: '2026-01-02T00:00:00Z',
          revokedAt: '2026-01-03T00:00:00Z',
        },
      ]);

      const activeBadge = document.querySelector('.status-pill--active');
      const revokedBadge = document.querySelector('.status-pill--revoked');
      expect(activeBadge?.textContent).toBe('Active');
      expect(revokedBadge?.textContent).toBe('Revoked');
    });

    it('mints a token, shows the one-time secret dialog, and cleans up on close', async () => {
      await openTokensDialog(true);
      apiFetchMock.mockResolvedValueOnce({
        tokenId: 10,
        token: 'sa_plaintext_secret',
      });

      const mintForm = document.getElementById('saTokenMintForm');
      if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
      mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      expect(showToastMock).toHaveBeenCalledWith('Token created.');
      const secretInput = document.getElementById('saTokenCreatedDisplay');
      if (!(secretInput instanceof HTMLInputElement)) throw new Error('missing secret dialog input');
      expect(secretInput.value).toBe('sa_plaintext_secret');

      const doneBtn = document.getElementById('saTokenCreatedDone');
      if (!(doneBtn instanceof HTMLElement)) throw new Error('missing done button');
      doneBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();
      expect(document.getElementById('saTokenCreatedDisplay')).toBeNull();
    });

    it.each([
      ['null response', null, null],
      ['missing token with an id', { tokenId: 11, name: 'ghost' }, 11],
      ['empty token', { tokenId: 12, name: 'ghost', token: '' }, 12],
      ['non-string token', { tokenId: 13, name: 'ghost', token: 123 }, 13],
    ] as const)(
      'handles malformed mint 2xx (%s) with best-effort revoke and no secret dialog',
      async (_case, response, expectedRevokeId) => {
        await openTokensDialog(true);
        apiFetchMock.mockResolvedValueOnce(response);
        if (expectedRevokeId !== null) {
          apiFetchMock.mockResolvedValueOnce(undefined);
        }
        apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });

        const mintForm = document.getElementById('saTokenMintForm');
        if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
        mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
        await flushPromises();

        expect(document.getElementById('saTokenCreatedDisplay')).toBeNull();
        expect(showToastMock).toHaveBeenCalledWith(
          'Token creation returned an unexpected response. A token may have been created — check your list and revoke it if unused.',
        );
        expect(showToastMock).not.toHaveBeenCalledWith('Token created.');
        if (expectedRevokeId === null) {
          expect(apiFetchMock).not.toHaveBeenCalledWith(
            expect.stringMatching(/\/api\/admin\/service-tokens\/\d+/),
            { method: 'DELETE' },
          );
        } else {
          expect(apiFetchMock).toHaveBeenCalledWith(
            `/api/admin/service-tokens/${expectedRevokeId}`,
            { method: 'DELETE' },
          );
        }
      },
    );

    it('best-effort revokes a minted token when the dialog closes before mint resolves', async () => {
      await openTokensDialog(true);
      let resolveMint!: (value: unknown) => void;
      apiFetchMock.mockImplementationOnce((url: string, opts?: { method?: string }) => {
        if (opts?.method === 'POST' && url.includes('/tokens')) {
          return new Promise((resolve) => {
            resolveMint = resolve;
          });
        }
        return Promise.resolve({ items: [], nextCursor: null });
      });

      const mintForm = document.getElementById('saTokenMintForm');
      if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
      mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      const closeBtn = document.getElementById('saTokensClose');
      if (!(closeBtn instanceof HTMLElement)) throw new Error('missing close button');
      closeBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      resolveMint({ tokenId: 55, token: 'orphan-secret' });
      await flushPromises();

      expect(apiFetchMock).toHaveBeenCalledWith('/api/admin/service-tokens/55', {
        method: 'DELETE',
      });
      expect(document.getElementById('saTokenCreatedDisplay')).toBeNull();
    });

    it('does not revoke or show plaintext when the authenticated lifecycle ends during mint', async () => {
      await openTokensDialog(true);
      let resolveMint!: (value: unknown) => void;
      apiFetchMock.mockImplementationOnce((url: string, opts?: { method?: string }) => {
        if (opts?.method === 'POST' && url.includes('/tokens')) {
          return new Promise((resolve) => {
            resolveMint = resolve;
          });
        }
        return Promise.resolve({ items: [], nextCursor: null });
      });

      const mintForm = document.getElementById('saTokenMintForm');
      if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
      mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      invalidateServiceAccountsLifecycle();
      await flushPromises();

      resolveMint({ tokenId: 77, token: 'cross-session-secret' });
      await flushPromises();

      expect(apiFetchMock).not.toHaveBeenCalledWith('/api/admin/service-tokens/77', {
        method: 'DELETE',
      });
      expect(document.getElementById('saTokenCreatedDisplay')).toBeNull();
      expect(document.querySelector('[data-sa-tokens-dialog]')).toBeNull();
      expect(showToastMock).not.toHaveBeenCalledWith('Token created.');
    });

    it('clears displayed plaintext when the authenticated lifecycle ends after mint', async () => {
      await openTokensDialog(true);
      apiFetchMock.mockResolvedValueOnce({ tokenId: 88, token: 'sb_visible_once' });
      apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });

      const mintForm = document.getElementById('saTokenMintForm');
      if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
      mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      const secretInput = document.getElementById('saTokenCreatedDisplay');
      if (!(secretInput instanceof HTMLInputElement)) throw new Error('missing secret display');
      expect(secretInput.value).toBe('sb_visible_once');

      invalidateServiceAccountsLifecycle();
      await flushPromises();

      expect(document.getElementById('saTokenCreatedDisplay')).toBeNull();
      expect(document.querySelector('[data-sa-token-created-dialog]')).toBeNull();
      expect(document.querySelector('[data-sa-tokens-dialog]')).toBeNull();
    });

    it('copies the minted secret via clipboard and falls back when clipboard write fails', async () => {
      await openTokensDialog(true);
      apiFetchMock.mockResolvedValueOnce({ tokenId: 20, token: 'copy_me_sa' });
      apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });

      const mintForm = document.getElementById('saTokenMintForm');
      if (!(mintForm instanceof HTMLFormElement)) throw new Error('missing mint form');
      mintForm.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
      await flushPromises();

      const copyBtn = document.getElementById('saTokenCreatedCopy');
      if (!(copyBtn instanceof HTMLElement)) throw new Error('missing copy button');
      copyBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith('copy_me_sa');
      expect(showToastMock).toHaveBeenCalledWith('Token copied.');

      vi.mocked(navigator.clipboard.writeText).mockRejectedValueOnce(new Error('denied'));
      copyBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();
      expect(showToastMock).toHaveBeenCalledWith(
        'Select the token and copy manually (Ctrl+C)',
      );
    });

    it('escapes hostile token names in revoke button attributes', async () => {
      const hostile = '"><script>alert(1)</script>';
      await openTokensDialog(true, [
        {
          tokenId: 99,
          userId: 7,
          name: hostile,
          createdAt: '2026-01-01T00:00:00Z',
        },
      ]);

      const revokeBtn = document.querySelector('[data-action="sa-revoke"]');
      expect(revokeBtn?.getAttribute('data-token-name')).toBe(hostile);
      const nameCell = document.querySelector('#saTokensBody td');
      expect(nameCell?.innerHTML).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
      expect(nameCell?.innerHTML).not.toContain('<script>alert');
    });

    it('revokes a token after confirmation and refreshes the list', async () => {
      await openTokensDialog(true, [
        {
          tokenId: 42,
          userId: 7,
          name: 'CI job',
          createdAt: '2026-01-01T00:00:00Z',
        },
      ]);

      showConfirmDialogMock.mockResolvedValueOnce(true);
      apiFetchMock.mockResolvedValueOnce(undefined);
      apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });

      const revokeBtn = document.querySelector('[data-action="sa-revoke"]');
      if (!(revokeBtn instanceof HTMLElement)) throw new Error('missing revoke button');
      revokeBtn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      expect(showConfirmDialogMock).toHaveBeenCalledWith(
        "This immediately invalidates CI job. Anything still using it will stop working. This can't be undone.",
        'Revoke API token',
        'Revoke',
      );
      expect(apiFetchMock).toHaveBeenCalledWith('/api/admin/service-tokens/42', {
        method: 'DELETE',
      });
      expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
    });

    it('does not revoke when confirmation is declined', async () => {
      await openTokensDialog(true, [
        { tokenId: 42, userId: 7, createdAt: '2026-01-01T00:00:00Z' },
      ]);
      showConfirmDialogMock.mockResolvedValueOnce(false);

      document.querySelector('[data-action="sa-revoke"]')?.dispatchEvent(
        new Event('click', { bubbles: true }),
      );
      await flushPromises();

      const deleteCalls = apiFetchMock.mock.calls.filter(
        (call) => call[1]?.method === 'DELETE',
      );
      expect(deleteCalls.length).toBe(0);
    });

    it('re-enables the revoke button after a non-404 failure', async () => {
      await openTokensDialog(true, [
        { tokenId: 42, userId: 7, name: 'x', createdAt: '2026-01-01T00:00:00Z' },
      ]);
      showConfirmDialogMock.mockResolvedValue(true);
      apiFetchMock.mockRejectedValueOnce(new Error('revoke boom'));

      const btn = document.querySelector('[data-action="sa-revoke"]');
      if (!(btn instanceof HTMLButtonElement)) throw new Error('missing revoke button');
      btn.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      expect(showToastMock).toHaveBeenCalledWith('revoke boom');
      expect(btn.disabled).toBe(false);
    });

    it('treats a 404 on revoke as already gone', async () => {
      await openTokensDialog(true, [
        { tokenId: 42, userId: 7, createdAt: '2026-01-01T00:00:00Z' },
      ]);
      showConfirmDialogMock.mockResolvedValueOnce(true);
      const notFound = Object.assign(new Error('missing'), { status: 404 });
      apiFetchMock.mockRejectedValueOnce(notFound);
      apiFetchMock.mockResolvedValueOnce({ items: [], nextCursor: null });

      document.querySelector('[data-action="sa-revoke"]')?.dispatchEvent(
        new Event('click', { bubbles: true }),
      );
      await flushPromises();

      expect(showToastMock).toHaveBeenCalledWith('Token revoked.');
      expect(showToastMock).not.toHaveBeenCalledWith('Failed to revoke token.');
    });

    it('does not issue a second DELETE for the same token while the first revoke is pending', async () => {
      await openTokensDialog(true, [
        { tokenId: 42, userId: 7, name: 'x', createdAt: '2026-01-01T00:00:00Z' },
      ]);

      showConfirmDialogMock.mockResolvedValue(true);
      let resolveDelete!: () => void;
      apiFetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
        if (opts?.method === 'DELETE') {
          return new Promise((resolve) => {
            resolveDelete = () => resolve(undefined);
          });
        }
        return Promise.resolve({ items: [], nextCursor: null });
      });

      const btn = document.querySelector('[data-action="sa-revoke"]');
      btn?.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();
      btn?.dispatchEvent(new Event('click', { bubbles: true }));
      await flushPromises();

      const deleteCalls = apiFetchMock.mock.calls.filter(
        (call) => call[1]?.method === 'DELETE',
      );
      expect(deleteCalls.length).toBe(1);

      resolveDelete();
      await flushPromises();
    });
  });
});