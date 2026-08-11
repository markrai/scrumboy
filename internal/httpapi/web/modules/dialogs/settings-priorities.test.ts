// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

type PriorityTier = { key: string; name: string; color?: string };

const priorityLocales: Record<string, Record<string, string>> = {
  en: enCatalog as Record<string, string>,
  de: deCatalog as Record<string, string>,
};

async function initPriorityI18n(locale: 'en' | 'de' = 'en'): Promise<typeof import('../i18n/index.js')> {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: async (code) => priorityLocales[code] ?? priorityLocales.en,
  });
  return i18n;
}

const selectorState: {
  board: { priorityOrder: PriorityTier[] } | null;
  search: string;
  activeTab: string;
  slug: string | null;
  tag: string;
} = {
  board: null,
  search: '',
  activeTab: 'priorities',
  slug: null,
  tag: '',
};

const apiFetchMock = vi.fn();
const invalidateBoardMock = vi.fn().mockResolvedValue(undefined);
const recordLocalMutationMock = vi.fn();
const showConfirmDialogMock = vi.fn();
const showToastMock = vi.fn();

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../orchestration/board-refresh.js', () => ({
  invalidateBoard: invalidateBoardMock,
}));

vi.mock('../realtime/guard.js', () => ({
  recordLocalMutation: recordLocalMutationMock,
}));

vi.mock('../state/selectors.js', () => ({
  getAssigneeFromUrl: () => new URL(window.location.href).searchParams.get('assignee'),
  getSortFromUrl: () => new URL(window.location.href).searchParams.get('sort'),
  getBoard: () => selectorState.board,
  getSearch: () => selectorState.search,
  getSettingsActiveTab: () => selectorState.activeTab,
  getSlug: () => selectorState.slug,
  getSprintIdFromUrl: () => new URL(window.location.href).searchParams.get('sprintId'),
  getTag: () => selectorState.tag,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  showConfirmDialog: showConfirmDialogMock,
  showToast: showToastMock,
}));

function makeBoard() {
  return {
    priorityOrder: [
      { key: 'low', name: 'Low', color: '#111111' },
      { key: 'medium', name: 'Medium', color: '#222222' },
      { key: 'urgent', name: 'Urgent', color: '#333333' },
    ],
  };
}

function render(html: string): void {
  document.body.innerHTML = html;
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadPriorityModule(locale: 'en' | 'de' = 'en') {
  await initPriorityI18n(locale);
  const mod = await import('./settings-priorities.js');
  return mod;
}

async function primeOkPriorityState(mod: Awaited<ReturnType<typeof loadPriorityModule>>, rerender: () => Promise<void>) {
  apiFetchMock.mockResolvedValue({
    countsByPriorityKey: {
      low: 0,
      medium: 0,
      urgent: 0,
    },
  });
  const first = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
  expect(first).toContain('Checking priority usage');
  await flushPromises();
  apiFetchMock.mockClear();
  rerender.mockClear();
}

describe('settings-priorities', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/alpha?sprintId=42');
    selectorState.board = makeBoard();
    selectorState.search = 'query';
    selectorState.activeTab = 'priorities';
    selectorState.slug = 'alpha';
    selectorState.tag = 'bug';
    apiFetchMock.mockReset();
    invalidateBoardMock.mockClear();
    invalidateBoardMock.mockResolvedValue(undefined);
    recordLocalMutationMock.mockClear();
    showConfirmDialogMock.mockReset();
    showToastMock.mockClear();
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    selectorState.board = null;
    selectorState.search = '';
    selectorState.activeTab = 'priorities';
    selectorState.slug = null;
    selectorState.tag = '';
  });

  it('loads tier counts asynchronously and then serves cached priority content immediately', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    apiFetchMock.mockResolvedValue({
      countsByPriorityKey: { low: 0, medium: 0, urgent: 0 },
    });
    const mod = await loadPriorityModule();

    const first = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    expect(first).toContain('Checking priority usage');
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/priorities/counts');

    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);

    apiFetchMock.mockClear();
    rerender.mockClear();

    const second = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    expect(second).not.toContain('Checking priority usage');
    expect(second).toContain('data-priority-delete="low"');
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('renders retry state for count-load failures and retry clears cache then rerenders', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    apiFetchMock.mockRejectedValue(new Error('boom'));
    const mod = await loadPriorityModule();

    const first = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    expect(first).toContain('Checking priority usage');
    await flushPromises();
    expect(rerender).toHaveBeenCalledTimes(1);

    rerender.mockClear();
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({
      countsByPriorityKey: { low: 0, medium: 0, urgent: 0 },
    });

    const errorHtml = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    expect(errorHtml).toContain('Could not load priority usage');
    render(errorHtml);

    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const retryBtn = document.querySelector('[data-priority-counts-retry]');
    if (!(retryBtn instanceof HTMLElement)) throw new Error('missing priority retry button');
    retryBtn.click();
    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);

    const next = mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    expect(next).toContain('Checking priority usage');
  });

  it('enables Save when a tier draft changes and Cancel resets the draft baseline', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const saveBtn = document.querySelector('[data-priority-save-changes]');
    const nameInput = document.querySelector('[data-priority-name="medium"]');
    const cancelBtn = document.querySelector('[data-priority-draft-cancel]');
    if (!(saveBtn instanceof HTMLButtonElement)) throw new Error('missing priority save button');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing priority name input');
    if (!(cancelBtn instanceof HTMLElement)) throw new Error('missing priority cancel button');

    expect(saveBtn.disabled).toBe(true);

    nameInput.value = 'Mid';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    expect(saveBtn.disabled).toBe(false);
    expect(mod.isPriorityDraftDirty()).toBe(true);

    cancelBtn.click();
    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);
    expect(mod.isPriorityDraftDirty()).toBe(false);
  });

  it('adds a tier through the priorities route and then invalidates the board', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const addInput = document.querySelector('[data-priority-ghost-input]');
    const addBtn = document.querySelector('[data-priority-add]');
    if (!(addInput instanceof HTMLInputElement)) throw new Error('missing priority add input');
    if (!(addBtn instanceof HTMLElement)) throw new Error('missing priority add button');

    addInput.value = '  Critical  ';
    addBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/priorities', {
      method: 'POST',
      body: JSON.stringify({ name: 'Critical' }),
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('shows a localized backend validation reason when adding a priority tier fails', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule('de');
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const addInput = document.querySelector('[data-priority-ghost-input]');
    const addBtn = document.querySelector('[data-priority-add]');
    if (!(addInput instanceof HTMLInputElement)) throw new Error('missing priority add input');
    if (!(addBtn instanceof HTMLElement)) throw new Error('missing priority add button');

    const err = new Error('validation: name required') as Error & { data?: unknown };
    err.data = {
      error: {
        code: 'VALIDATION_ERROR',
        message: 'validation: name required',
        details: { reason: 'name_required', field: 'name' },
      },
    };
    apiFetchMock.mockRejectedValueOnce(err);

    addInput.value = 'Critical';
    addBtn.click();
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith(deCatalog['errors.VALIDATION_ERROR.name_required']);
    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('patches only changed priority tiers and then rerenders', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const nameInput = document.querySelector('[data-priority-name="medium"]');
    const saveBtn = document.querySelector('[data-priority-save-changes]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing priority name input');
    if (!(saveBtn instanceof HTMLElement)) throw new Error('missing priority save button');

    nameInput.value = 'Mid';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    apiFetchMock.mockClear();
    rerender.mockClear();
    recordLocalMutationMock.mockClear();
    invalidateBoardMock.mockClear();

    saveBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/priorities/medium', {
      method: 'PATCH',
      body: JSON.stringify({ name: 'Mid', color: '#222222' }),
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('stops after a partial PATCH failure and resynchronizes the draft from the server', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const lowInput = document.querySelector('[data-priority-name="low"]');
    const mediumInput = document.querySelector('[data-priority-name="medium"]');
    const urgentInput = document.querySelector('[data-priority-name="urgent"]');
    const saveBtn = document.querySelector('[data-priority-save-changes]');
    if (!(lowInput instanceof HTMLInputElement)) throw new Error('missing low priority input');
    if (!(mediumInput instanceof HTMLInputElement)) throw new Error('missing medium priority input');
    if (!(urgentInput instanceof HTMLInputElement)) throw new Error('missing urgent priority input');
    if (!(saveBtn instanceof HTMLElement)) throw new Error('missing priority save button');

    lowInput.value = 'Low server change';
    lowInput.dispatchEvent(new Event('input', { bubbles: true }));
    mediumInput.value = 'Medium rejected change';
    mediumInput.dispatchEvent(new Event('input', { bubbles: true }));
    urgentInput.value = 'Urgent unsent change';
    urgentInput.dispatchEvent(new Event('input', { bubbles: true }));

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValueOnce({}).mockRejectedValueOnce(new Error('second patch failed'));
    invalidateBoardMock.mockImplementationOnce(async () => {
      selectorState.board = {
        priorityOrder: [
          { key: 'low', name: 'Low server change', color: '#111111' },
          { key: 'medium', name: 'Medium', color: '#222222' },
          { key: 'urgent', name: 'Urgent', color: '#333333' },
        ],
      };
    });

    saveBtn.click();
    await flushPromises(12);

    expect(apiFetchMock).toHaveBeenCalledTimes(2);
    expect(apiFetchMock.mock.calls[0]?.[0]).toBe('/api/board/alpha/priorities/low');
    expect(apiFetchMock.mock.calls[1]?.[0]).toBe('/api/board/alpha/priorities/medium');
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, true);
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(mod.isPriorityDraftDirty()).toBe(false);
    expect(showToastMock).toHaveBeenCalledWith('second patch failed');
  });

  it('refetches after an ambiguous first failure and requires reload if resynchronization fails', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const nameInput = document.querySelector('[data-priority-name="low"]');
    const saveBtn = document.querySelector('[data-priority-save-changes]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing priority input');
    if (!(saveBtn instanceof HTMLElement)) throw new Error('missing priority save button');
    nameInput.value = 'Ambiguous write';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    apiFetchMock.mockReset();
    apiFetchMock.mockRejectedValueOnce(new Error('network result unknown'));
    invalidateBoardMock.mockRejectedValueOnce(new Error('refresh failed'));

    saveBtn.click();
    await flushPromises(12);

    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, true);
    expect(rerender).not.toHaveBeenCalled();
    expect(mod.isPriorityDraftDirty()).toBe(false);
    expect(showToastMock).toHaveBeenNthCalledWith(1, 'network result unknown');
    expect(showToastMock).toHaveBeenNthCalledWith(2, enCatalog['settings.priorities.toast.reloadRequired']);
  });

  it('deletes an empty priority tier through the priorities delete route', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);
    showConfirmDialogMock.mockResolvedValue(true);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const deleteBtn = document.querySelector('[data-priority-delete="low"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing priority delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/priorities/low', {
      method: 'DELETE',
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('disables delete when only one tier remains, even if reported empty', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    selectorState.board = { priorityOrder: [{ key: 'low', name: 'Low', color: '#111111' }] };
    apiFetchMock.mockResolvedValue({ countsByPriorityKey: { low: 0 } });
    const mod = await loadPriorityModule();

    mod.loadPriorityTabContent({ slug: 'alpha', rerender });
    await flushPromises();
    const html = mod.loadPriorityTabContent({ slug: 'alpha', rerender });

    expect(html).not.toContain('data-priority-delete="low"');
    expect(html).toContain('disabled');
  });

  it('uses localized delete confirm copy with the raw tier name', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule('de');
    await primeOkPriorityState(mod, rerender);
    showConfirmDialogMock.mockResolvedValue(false);

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const deleteBtn = document.querySelector('[data-priority-delete="low"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing priority delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      (priorityLocales.de['settings.priorities.deleteConfirm.message'] ?? '').replace('{name}', 'Low'),
      priorityLocales.de['settings.priorities.deleteConfirm.title'],
      priorityLocales.de['settings.priorities.deleteConfirm.confirm'],
    );
  });

  it('only intercepts modal close actions when the priority draft is dirty', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadPriorityModule();
    await primeOkPriorityState(mod, rerender);

    const dialog = document.createElement('dialog') as HTMLDialogElement;
    const closeSpy = vi.fn();
    (dialog as HTMLDialogElement & { close: () => void }).close = closeSpy;
    const closeSettingsBtn = document.createElement('button');

    render(mod.loadPriorityTabContent({ slug: 'alpha', rerender }));
    mod.bindPriorityTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: dialog,
      closeSettingsBtn,
      rerender,
    });

    const cleanCancelEvent = new Event('cancel', { cancelable: true });
    dialog.dispatchEvent(cleanCancelEvent);
    expect(cleanCancelEvent.defaultPrevented).toBe(false);
    expect(showConfirmDialogMock).not.toHaveBeenCalled();

    showConfirmDialogMock.mockResolvedValue(true);
    const nameInput = document.querySelector('[data-priority-name="medium"]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing priority name input');
    nameInput.value = 'Dirty change';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));
    expect(mod.isPriorityDraftDirty()).toBe(true);

    const closeClick = new MouseEvent('click', { bubbles: true, cancelable: true });
    closeSettingsBtn.dispatchEvent(closeClick);
    await flushPromises();

    expect(closeClick.defaultPrevented).toBe(true);
    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(closeSpy).toHaveBeenCalledTimes(1);
    expect(mod.isPriorityDraftDirty()).toBe(false);
  });
});
