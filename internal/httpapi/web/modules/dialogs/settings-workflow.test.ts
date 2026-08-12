// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

type WorkflowLane = { key: string; name: string; color?: string; isDone?: boolean };

const workflowLocales: Record<string, Record<string, string>> = {
  en: enCatalog as Record<string, string>,
  de: deCatalog as Record<string, string>,
};

async function initWorkflowI18n(locale: 'en' | 'de' = 'en'): Promise<typeof import('../i18n/index.js')> {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: async (code) => workflowLocales[code] ?? workflowLocales.en,
  });
  return i18n;
}

const selectorState: {
  board: { columnOrder: WorkflowLane[] } | null;
  search: string;
  activeTab: string;
  slug: string | null;
  tag: string;
} = {
  board: null,
  search: '',
  activeTab: 'workflow',
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
  getPriorityFromUrl: () => new URL(window.location.href).searchParams.get('priority'),
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
    columnOrder: [
      { key: 'backlog', name: 'Backlog', color: '#111111', isDone: false },
      { key: 'doing', name: 'Doing', color: '#222222', isDone: false },
      { key: 'done', name: 'Done', color: '#333333', isDone: true },
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

async function loadWorkflowModule(locale: 'en' | 'de' = 'en') {
  await initWorkflowI18n(locale);
  const mod = await import('./settings-workflow.js');
  return mod;
}

async function primeOkWorkflowState(mod: Awaited<ReturnType<typeof loadWorkflowModule>>, rerender: () => Promise<void>) {
  apiFetchMock.mockResolvedValue({
    countsByColumnKey: {
      backlog: 0,
      doing: 0,
      done: 0,
    },
  });
  const first = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
  expect(first).toContain('Checking lane occupancy');
  await flushPromises();
  apiFetchMock.mockClear();
  rerender.mockClear();
}

describe('settings-workflow', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/alpha?sprintId=42');
    selectorState.board = makeBoard();
    selectorState.search = 'query';
    selectorState.activeTab = 'workflow';
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
    selectorState.activeTab = 'workflow';
    selectorState.slug = null;
    selectorState.tag = '';
  });

  it('loads lane counts asynchronously and then serves cached workflow content immediately', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    apiFetchMock.mockResolvedValue({
      countsByColumnKey: {
        backlog: 0,
        doing: 0,
        done: 0,
      },
    });
    const mod = await loadWorkflowModule();

    const first = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
    expect(first).toContain('Checking lane occupancy');
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/workflow/counts');

    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);

    apiFetchMock.mockClear();
    rerender.mockClear();

    const second = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
    expect(second).not.toContain('Checking lane occupancy');
    expect(second).toContain('data-workflow-delete="backlog"');
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('renders retry state for count-load failures and retry clears cache then rerenders', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    apiFetchMock.mockRejectedValue(new Error('boom'));
    const mod = await loadWorkflowModule();

    const first = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
    expect(first).toContain('Checking lane occupancy');
    await flushPromises();
    expect(rerender).toHaveBeenCalledTimes(1);

    rerender.mockClear();
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({
      countsByColumnKey: {
        backlog: 0,
        doing: 0,
        done: 0,
      },
    });

    const errorHtml = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
    expect(errorHtml).toContain('Could not load lane occupancy');
    render(errorHtml);

    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const retryBtn = document.querySelector('[data-workflow-counts-retry]');
    if (!(retryBtn instanceof HTMLElement)) throw new Error('missing workflow retry button');
    retryBtn.click();
    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);

    const next = mod.loadWorkflowTabContent({ slug: 'alpha', rerender });
    expect(next).toContain('Checking lane occupancy');
  });

  it('enables Save when a lane draft changes and Cancel resets the draft baseline', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule();
    await primeOkWorkflowState(mod, rerender);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const saveBtn = document.querySelector('[data-workflow-save-changes]');
    const nameInput = document.querySelector('[data-workflow-name="doing"]');
    const cancelBtn = document.querySelector('[data-workflow-draft-cancel]');
    if (!(saveBtn instanceof HTMLButtonElement)) throw new Error('missing workflow save button');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing workflow name input');
    if (!(cancelBtn instanceof HTMLElement)) throw new Error('missing workflow cancel button');

    expect(saveBtn.disabled).toBe(true);

    nameInput.value = 'In Progress';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    expect(saveBtn.disabled).toBe(false);
    expect(mod.isWorkflowDraftDirty()).toBe(true);

    cancelBtn.click();
    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);
    expect(mod.isWorkflowDraftDirty()).toBe(false);
  });

  it('adds a lane through the workflow route and then invalidates the board', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule();
    await primeOkWorkflowState(mod, rerender);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const addInput = document.querySelector('[data-workflow-ghost-input]');
    const addBtn = document.querySelector('[data-workflow-add]');
    if (!(addInput instanceof HTMLInputElement)) throw new Error('missing workflow add input');
    if (!(addBtn instanceof HTMLElement)) throw new Error('missing workflow add button');

    addInput.value = '  Review  ';
    addBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/workflow', {
      method: 'POST',
      body: JSON.stringify({ name: 'Review' }),
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('shows a localized backend validation reason when adding a workflow lane fails', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule('de');
    await primeOkWorkflowState(mod, rerender);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const addInput = document.querySelector('[data-workflow-ghost-input]');
    const addBtn = document.querySelector('[data-workflow-add]');
    if (!(addInput instanceof HTMLInputElement)) throw new Error('missing workflow add input');
    if (!(addBtn instanceof HTMLElement)) throw new Error('missing workflow add button');

    const err = new Error('validation: name required') as Error & { data?: unknown };
    err.data = {
      error: {
        code: 'VALIDATION_ERROR',
        message: 'validation: name required',
        details: { reason: 'name_required', field: 'name' },
      },
    };
    apiFetchMock.mockRejectedValueOnce(err);

    addInput.value = 'Review';
    addBtn.click();
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith(deCatalog['errors.VALIDATION_ERROR.name_required']);
    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('patches only changed workflow lanes and then rerenders', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule();
    await primeOkWorkflowState(mod, rerender);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const nameInput = document.querySelector('[data-workflow-name="doing"]');
    const saveBtn = document.querySelector('[data-workflow-save-changes]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing workflow name input');
    if (!(saveBtn instanceof HTMLElement)) throw new Error('missing workflow save button');

    nameInput.value = 'Working';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    apiFetchMock.mockClear();
    rerender.mockClear();
    recordLocalMutationMock.mockClear();
    invalidateBoardMock.mockClear();

    saveBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/workflow/doing', {
      method: 'PATCH',
      body: JSON.stringify({ name: 'Working', color: '#222222' }),
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('deletes an empty non-done lane through the workflow delete route', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule();
    await primeOkWorkflowState(mod, rerender);
    showConfirmDialogMock.mockResolvedValue(true);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const deleteBtn = document.querySelector('[data-workflow-delete="backlog"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing workflow delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/workflow/backlog', {
      method: 'DELETE',
    });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, null);
    expect(rerender).toHaveBeenCalledTimes(1);
  });

  it('uses localized delete confirm copy with the raw lane name', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule('de');
    await primeOkWorkflowState(mod, rerender);
    showConfirmDialogMock.mockResolvedValue(false);

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: null,
      closeSettingsBtn: null,
      rerender,
    });

    const deleteBtn = document.querySelector('[data-workflow-delete="backlog"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing workflow delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      (workflowLocales.de['settings.workflow.deleteConfirm.message'] ?? '').replace('{name}', 'Backlog'),
      workflowLocales.de['settings.workflow.deleteConfirm.title'],
      workflowLocales.de['settings.workflow.deleteConfirm.confirm'],
    );
  });

  it('only intercepts modal close actions when the workflow draft is dirty', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule();
    await primeOkWorkflowState(mod, rerender);

    const dialog = document.createElement('dialog') as HTMLDialogElement;
    const closeSpy = vi.fn();
    (dialog as HTMLDialogElement & { close: () => void }).close = closeSpy;
    const closeSettingsBtn = document.createElement('button');

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
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
    const nameInput = document.querySelector('[data-workflow-name="doing"]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing workflow name input');
    nameInput.value = 'Dirty change';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));
    expect(mod.isWorkflowDraftDirty()).toBe(true);

    const closeClick = new MouseEvent('click', { bubbles: true, cancelable: true });
    closeSettingsBtn.dispatchEvent(closeClick);
    await flushPromises();

    expect(closeClick.defaultPrevented).toBe(true);
    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(closeSpy).toHaveBeenCalledTimes(1);
    expect(mod.isWorkflowDraftDirty()).toBe(false);
  });

  it('uses localized unsaved-draft confirm copy for cancel and close actions', async () => {
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadWorkflowModule('de');
    await primeOkWorkflowState(mod, rerender);

    const dialog = document.createElement('dialog') as HTMLDialogElement;
    const closeSpy = vi.fn();
    (dialog as HTMLDialogElement & { close: () => void }).close = closeSpy;
    const closeSettingsBtn = document.createElement('button');

    render(mod.loadWorkflowTabContent({ slug: 'alpha', rerender }));
    mod.bindWorkflowTabInteractions({
      signal: new AbortController().signal,
      settingsDialog: dialog,
      closeSettingsBtn,
      rerender,
    });

    const nameInput = document.querySelector('[data-workflow-name="doing"]');
    if (!(nameInput instanceof HTMLInputElement)) throw new Error('missing workflow name input');
    nameInput.value = 'Dirty change';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));

    showConfirmDialogMock.mockResolvedValueOnce(false);
    const cancelEvent = new Event('cancel', { cancelable: true });
    dialog.dispatchEvent(cancelEvent);
    await flushPromises();

    const closeClick = new MouseEvent('click', { bubbles: true, cancelable: true });
    showConfirmDialogMock.mockResolvedValueOnce(true);
    closeSettingsBtn.dispatchEvent(closeClick);
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenNthCalledWith(
      1,
      workflowLocales.de['settings.workflow.unsavedConfirm.message'],
      workflowLocales.de['settings.workflow.unsavedConfirm.title'],
      workflowLocales.de['settings.workflow.unsavedConfirm.confirm'],
    );
    expect(showConfirmDialogMock).toHaveBeenNthCalledWith(
      2,
      workflowLocales.de['settings.workflow.unsavedConfirm.message'],
      workflowLocales.de['settings.workflow.unsavedConfirm.title'],
      workflowLocales.de['settings.workflow.unsavedConfirm.confirm'],
    );
    expect(closeSpy).toHaveBeenCalledTimes(1);
  });
});
