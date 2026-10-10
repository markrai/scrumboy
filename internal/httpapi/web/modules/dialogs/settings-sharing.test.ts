// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { apiFetchMock, confirmMock, toastMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  confirmMock: vi.fn(),
  toastMock: vi.fn(),
}));

vi.mock('../api.js', () => ({ apiFetch: apiFetchMock }));
vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) => String(s).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;'),
  showConfirmDialog: confirmMock,
  showToast: toastMock,
}));
vi.mock('../i18n/index.js', () => ({ t: (key: string) => key }));
vi.mock('../platform/runtime.js', () => ({ getAppRuntime: () => ({ publicLinkOrigin: () => 'https://boards.example' }) }));

import { bindSharingTab, parsePublicationStatus, parsePublicationUpdate, renderSharingTabShell } from './settings-sharing.js';

const ENDPOINT = '/api/board/ignite/publication';

async function settle(): Promise<void> {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

function mount(): AbortController {
  document.body.innerHTML = renderSharingTabShell();
  const controller = new AbortController();
  bindSharingTab({ slug: 'ignite', signal: controller.signal });
  return controller;
}

function root(): HTMLElement {
  return document.querySelector('[data-sharing-root]') as HTMLElement;
}

function toggleButton(): HTMLButtonElement | null {
  return root().querySelector('[data-sharing-toggle]');
}

function patchCalls(): unknown[][] {
  return apiFetchMock.mock.calls.filter((call) => (call[1] as { method?: string } | undefined)?.method === 'PATCH');
}

describe('Settings Sharing tab', () => {
  beforeEach(() => {
    apiFetchMock.mockReset();
    confirmMock.mockReset();
    toastMock.mockReset();
  });
  afterEach(() => {
    document.body.innerHTML = '';
    vi.unstubAllGlobals();
  });

  it('shows Private status and publishes only after confirmation and server success', async () => {
    let release: (value: unknown) => void = () => {};
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) => {
      if (init?.method === 'PATCH') return new Promise((resolve) => { release = resolve; });
      return Promise.resolve({ enabled: false, publishable: true });
    });
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('private');
    expect(root().querySelector('#sharingPublicUrl')).toBeNull();

    toggleButton()!.click();
    await settle();
    expect(confirmMock).toHaveBeenCalledWith(
      'settings.sharing.confirmPublishMessage', 'settings.sharing.confirmPublishTitle', 'settings.sharing.confirmPublishAction', 'success',
    );
    // Pending: not yet shown as public, controls disabled, duplicate clicks ignored.
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('private');
    expect(toggleButton()!.disabled).toBe(true);
    toggleButton()!.click();
    await settle();
    expect(patchCalls()).toHaveLength(1);
    expect(patchCalls()[0][0]).toBe(ENDPOINT);
    expect(JSON.parse((patchCalls()[0][1] as { body: string }).body)).toEqual({ enabled: true });

    release({ enabled: true, changed: true });
    await settle();
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('public');
    expect((root().querySelector('#sharingPublicUrl') as HTMLInputElement).value).toBe('https://boards.example/ignite');
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.published');
  });

  it('does nothing when the confirmation is cancelled', async () => {
    apiFetchMock.mockResolvedValue({ enabled: false, publishable: true });
    confirmMock.mockResolvedValue(false);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(patchCalls()).toHaveLength(0);
  });

  it('unpublishes with a danger confirmation and reports success', async () => {
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) =>
      Promise.resolve(init?.method === 'PATCH' ? { enabled: false, changed: true } : { enabled: true, publishable: true }));
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(confirmMock.mock.calls[0][3]).toBe('danger');
    expect(confirmMock.mock.calls[0][0]).toBe('settings.sharing.confirmUnpublishMessage');
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('private');
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.unpublished');
  });

  it('copies the public link, and falls back to selecting it when the clipboard fails', async () => {
    apiFetchMock.mockResolvedValue({ enabled: true, publishable: true });
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    mount();
    await settle();
    (root().querySelector('[data-sharing-copy]') as HTMLButtonElement).click();
    await settle();
    expect(writeText).toHaveBeenCalledWith('https://boards.example/ignite');
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.copied');

    writeText.mockRejectedValueOnce(new Error('denied'));
    (root().querySelector('[data-sharing-copy]') as HTMLButtonElement).click();
    await settle();
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.copyFailed');
    expect(document.activeElement?.id).toBe('sharingPublicUrl');
  });

  it.each([403, 404])('shows no controls when the server denies access (%s)', async (status) => {
    apiFetchMock.mockRejectedValue(Object.assign(new Error('denied'), { status }));
    mount();
    await settle();
    expect(root().textContent).toContain('settings.sharing.notAllowed');
    expect(toggleButton()).toBeNull();
  });

  it('handles losing Maintainer rights while the dialog is open', async () => {
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) =>
      init?.method === 'PATCH'
        ? Promise.reject(Object.assign(new Error('forbidden'), { status: 403 }))
        : Promise.resolve({ enabled: false, publishable: true }));
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(root().textContent).toContain('settings.sharing.notAllowed');
    expect(toggleButton()).toBeNull();
  });

  it('reconciles an uncertain failure by refetching authoritative status', async () => {
    let statusCalls = 0;
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) => {
      if (init?.method === 'PATCH') return Promise.reject(Object.assign(new Error('Failed to fetch'), { status: undefined }));
      statusCalls += 1;
      // The server actually committed the change before the connection dropped.
      return Promise.resolve({ enabled: statusCalls > 1, publishable: true });
    });
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.failed');
    expect(statusCalls).toBe(2);
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('public');
  });

  it('disables publishing for a reserved slug', async () => {
    apiFetchMock.mockResolvedValue({ enabled: false, publishable: false });
    mount();
    await settle();
    expect(toggleButton()!.disabled).toBe(true);
    expect(root().textContent).toContain('settings.sharing.reservedSlug');
  });

  it('ignores a late response after the settings view re-rendered', async () => {
    let release: (value: unknown) => void = () => {};
    apiFetchMock.mockImplementation(() => new Promise((resolve) => { release = resolve; }));
    const controller = mount();
    controller.abort();
    const before = root().innerHTML;
    release({ enabled: true, publishable: true });
    await settle();
    expect(root().innerHTML).toBe(before);
  });
});

describe('Sharing tab strict response validation', () => {
  beforeEach(() => {
    apiFetchMock.mockReset();
    confirmMock.mockReset();
    toastMock.mockReset();
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('parsers accept only exact boolean contracts', () => {
    expect(parsePublicationStatus({ enabled: true, publishable: false })).toEqual({ enabled: true, publishable: false });
    for (const bad of [null, undefined, [], 'x', {}, { enabled: true }, { enabled: 'true', publishable: true }, { enabled: 1, publishable: true }, { enabled: true, publishable: null }]) {
      expect(parsePublicationStatus(bad)).toBeNull();
    }
    expect(parsePublicationUpdate({ enabled: true, changed: false }, true)).toEqual({ enabled: true, changed: false });
    for (const bad of [null, {}, { enabled: true }, { changed: true }, { enabled: 'true', changed: true }, { enabled: true, changed: 0 }, [true, true]]) {
      expect(parsePublicationUpdate(bad, true)).toBeNull();
    }
    expect(parsePublicationUpdate({ enabled: false, changed: true }, true)).toBeNull();
    expect(parsePublicationUpdate({ enabled: true, changed: true }, false)).toBeNull();
  });

  it.each([
    ['missing fields', {}],
    ['missing publishable', { enabled: false }],
    ['string booleans', { enabled: 'false', publishable: 'true' }],
    ['numeric booleans', { enabled: 0, publishable: 1 }],
    ['null body', null],
  ])('a malformed GET (%s) is never shown as a publication state and offers retry', async (_label, body) => {
    apiFetchMock.mockResolvedValueOnce(body).mockResolvedValueOnce({ enabled: false, publishable: true });
    mount();
    await settle();
    expect(root().querySelector('[data-sharing-state]')).toBeNull();
    expect(toggleButton()).toBeNull();
    expect(root().textContent).toContain('settings.sharing.loadFailed');
    (root().querySelector('[data-sharing-retry]') as HTMLButtonElement).click();
    await settle();
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('private');
  });

  it.each([
    ['missing changed', { enabled: true }, true],
    ['missing enabled', { changed: true }, true],
    ['string enabled', { enabled: 'true', changed: true }, true],
    ['contradictory enabled', { enabled: false, changed: true }, false],
    ['empty body', null, false],
  ])('a 2xx PATCH with %s is uncertain: no success toast, status refetched from the server', async (_label, patchBody, serverCommitted) => {
    let statusCalls = 0;
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) => {
      if (init?.method === 'PATCH') return Promise.resolve(patchBody);
      statusCalls += 1;
      return Promise.resolve({ enabled: statusCalls > 1 ? serverCommitted : false, publishable: true });
    });
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(toastMock).not.toHaveBeenCalledWith('settings.sharing.published');
    expect(toastMock).not.toHaveBeenCalledWith('settings.sharing.unpublished');
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.failed');
    expect(statusCalls).toBe(2);
    // The displayed state is whatever the server confirms afterwards.
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe(serverCommitted ? 'public' : 'private');
    expect(toggleButton()!.disabled).toBe(false);
  });

  it('a valid idempotent PATCH (changed:false) is confirmed without refetching', async () => {
    let statusCalls = 0;
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) => {
      if (init?.method === 'PATCH') return Promise.resolve({ enabled: true, changed: false });
      statusCalls += 1;
      return Promise.resolve({ enabled: false, publishable: true });
    });
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(statusCalls).toBe(1);
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('public');
    expect(toastMock).toHaveBeenCalledWith('settings.sharing.published');
  });

  it('unpublishing a legacy reserved-slug board keeps publishing disabled', async () => {
    apiFetchMock.mockImplementation((url: string, init?: { method?: string }) =>
      Promise.resolve(init?.method === 'PATCH' ? { enabled: false, changed: true } : { enabled: true, publishable: false }));
    confirmMock.mockResolvedValue(true);
    mount();
    await settle();
    toggleButton()!.click();
    await settle();
    expect(root().querySelector('[data-sharing-state]')?.getAttribute('data-sharing-state')).toBe('private');
    expect(toggleButton()!.disabled).toBe(true);
    expect(root().textContent).toContain('settings.sharing.reservedSlug');
  });
});
