// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

const sprintLocales: Record<string, Record<string, string>> = {
  en: enCatalog as Record<string, string>,
  de: deCatalog as Record<string, string>,
};

async function initSprintI18n(locale: 'en' | 'de' = 'en'): Promise<typeof import('../i18n/index.js')> {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: async (code) => sprintLocales[code] ?? sprintLocales.en,
  });
  return i18n;
}

const SPRINT_DATE_OPTS: Intl.DateTimeFormatOptions = {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
};

type SprintShape = {
  id: number;
  name: string;
  state: string;
  plannedStartAt: number;
  plannedEndAt: number;
  todoCount?: number;
};

const selectorState: {
  board: { project: { defaultSprintWeeks?: number; sprintsEnabled?: boolean } } | null;
  slug: string | null;
} = {
  board: null,
  slug: null,
};

const apiFetchMock = vi.fn();
const emitMock = vi.fn();
const invalidateBoardMock = vi.fn().mockResolvedValue(undefined);
const refreshSprintsAndChipsMock = vi.fn().mockResolvedValue(undefined);
const clearSprintChipDataMock = vi.fn();
const recordLocalMutationMock = vi.fn();
const setBoardMock = vi.fn((nextBoard: { project: { defaultSprintWeeks?: number; sprintsEnabled?: boolean } }) => {
  selectorState.board = nextBoard;
});
const showConfirmDialogMock = vi.fn();
const showToastMock = vi.fn();

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../events.js', () => ({
  emit: emitMock,
}));

vi.mock('../orchestration/board-refresh.js', () => ({
  invalidateBoard: invalidateBoardMock,
  refreshSprintsAndChips: refreshSprintsAndChipsMock,
}));

vi.mock('../views/board-filters.js', () => ({
  clearSprintChipData: clearSprintChipDataMock,
}));

vi.mock('../realtime/guard.js', () => ({
  recordLocalMutation: recordLocalMutationMock,
}));

vi.mock('../state/selectors.js', () => ({
  getBoard: () => selectorState.board,
  getSlug: () => selectorState.slug,
}));

vi.mock('../state/mutations.js', () => ({
  setBoard: setBoardMock,
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (res: { sprints?: SprintShape[] } | null | undefined) => res?.sprints ?? [],
  boardSprintsEnabled: (board: { project?: { sprintsEnabled?: boolean } } | null | undefined) =>
    board?.project?.sprintsEnabled !== false,
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

function render(html: string): void {
  document.body.innerHTML = html;
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadSprintsModule(locale: 'en' | 'de' = 'en') {
  await initSprintI18n(locale);
  return import('./settings-sprints.js');
}

function makeSprint(id: number, state: string, overrides?: Partial<SprintShape>): SprintShape {
  const now = Date.now();
  return {
    id,
    name: `Sprint ${id}`,
    state,
    plannedStartAt: now,
    plannedEndAt: now + 7 * 24 * 60 * 60 * 1000,
    todoCount: 0,
    ...overrides,
  };
}

describe('settings-sprints', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-16T15:30:00Z'));
    window.history.replaceState({}, '', '/alpha');
    selectorState.board = { project: { defaultSprintWeeks: 1 } };
    selectorState.slug = 'alpha';
    apiFetchMock.mockReset();
    emitMock.mockClear();
    invalidateBoardMock.mockClear();
    invalidateBoardMock.mockResolvedValue(undefined);
    refreshSprintsAndChipsMock.mockClear();
    refreshSprintsAndChipsMock.mockResolvedValue(undefined);
    clearSprintChipDataMock.mockClear();
    recordLocalMutationMock.mockClear();
    setBoardMock.mockClear();
    showConfirmDialogMock.mockReset();
    showToastMock.mockClear();
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    vi.useRealTimers();
    vi.restoreAllMocks();
    document.body.innerHTML = '';
    window.history.replaceState({}, '', '/');
    selectorState.board = null;
    selectorState.slug = null;
  });

  it('renders default sprint form values and state-specific action buttons', async () => {
    apiFetchMock.mockResolvedValue({
      sprints: [
        makeSprint(1, 'PLANNED'),
        makeSprint(2, 'ACTIVE'),
        makeSprint(3, 'CLOSED'),
      ],
    });
    const mod = await loadSprintsModule();

    const html = await mod.renderSprintsTabContent();
    render(html);

    const weeksSelect = document.getElementById('sprintDefaultWeeksSelect') as HTMLSelectElement | null;
    const startInput = document.getElementById('sprintStartInput') as HTMLInputElement | null;
    const endInput = document.getElementById('sprintEndInput') as HTMLInputElement | null;
    if (!weeksSelect || !startInput || !endInput) throw new Error('missing sprint create form');

    expect(weeksSelect.value).toBe('1');
    expect(startInput.value).not.toBe('');
    expect(endInput.value).not.toBe('');
    expect(document.querySelector('[data-sprint-activate="1"]')).not.toBeNull();
    expect(document.querySelector('[data-sprint-close="2"]')).not.toBeNull();
    expect(document.querySelector('[data-sprint-id="3"] .settings-sprint-row__action-placeholder')).not.toBeNull();
  });

  it('clears an active sprint filter and reloads unscheduled board state when disabled', async () => {
    window.history.replaceState({}, '', '/alpha?tag=focus&sprintId=7');
    apiFetchMock.mockResolvedValueOnce({ sprints: [] });
    const mod = await loadSprintsModule();
    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender: vi.fn().mockResolvedValue(undefined),
      invalidateSprintChartsCache: vi.fn(),
    });
    apiFetchMock.mockResolvedValueOnce({ sprintsEnabled: false });

    const toggle = document.getElementById('sprintsEnabledToggle') as HTMLInputElement | null;
    if (!toggle) throw new Error('missing sprints enabled toggle');
    toggle.checked = false;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(window.location.search).toBe('?tag=focus');
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'focus', undefined, null, null, null, true);
    expect(clearSprintChipDataMock).toHaveBeenCalledTimes(1);
    expect(selectorState.board?.project.sprintsEnabled).toBe(false);
  });

  it('force-reloads preserved sprint state and chips when re-enabled', async () => {
    selectorState.board = { project: { defaultSprintWeeks: 1, sprintsEnabled: false } };
    const mod = await loadSprintsModule();
    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender: vi.fn().mockResolvedValue(undefined),
      invalidateSprintChartsCache: vi.fn(),
    });
    apiFetchMock.mockResolvedValueOnce({ sprintsEnabled: true });

    const toggle = document.getElementById('sprintsEnabledToggle') as HTMLInputElement | null;
    if (!toggle) throw new Error('missing sprints enabled toggle');
    toggle.checked = true;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', undefined, undefined, null, null, null, true);
    expect(refreshSprintsAndChipsMock).toHaveBeenCalledWith('alpha');
    expect(selectorState.board?.project.sprintsEnabled).toBe(true);
  });

  it('keeps the committed capability state when post-patch refresh fails', async () => {
    const mod = await loadSprintsModule();
    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender: vi.fn().mockRejectedValue(new Error('render failed')),
      invalidateSprintChartsCache: vi.fn(),
    });
    apiFetchMock.mockResolvedValueOnce({ sprintsEnabled: false });
    invalidateBoardMock.mockRejectedValueOnce(new Error('refresh failed'));

    const toggle = document.getElementById('sprintsEnabledToggle') as HTMLInputElement | null;
    if (!toggle) throw new Error('missing sprints enabled toggle');
    toggle.checked = false;
    toggle.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(toggle.checked).toBe(false);
    expect(selectorState.board?.project.sprintsEnabled).toBe(false);
    expect(showToastMock).toHaveBeenCalledWith(enCatalog['settings.sprints.toast.disabled']);
  });

  it('keeps sprint edit state in module state after clicking Edit', async () => {
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED')],
    });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    const editBtn = document.querySelector('[data-sprint-edit="1"]');
    if (!(editBtn instanceof HTMLElement)) throw new Error('missing sprint edit button');
    editBtn.click();
    await flushPromises();

    expect(rerender).toHaveBeenCalledTimes(1);

    render(await mod.renderSprintsTabContent());
    expect(document.querySelector('[data-sprint-id="1"].settings-sprint-row--editing')).not.toBeNull();
    expect(document.querySelector('[data-sprint-save="1"]')).not.toBeNull();
    expect(document.querySelector('[data-sprint-cancel="1"]')).not.toBeNull();
  });

  it('updates the computed end date until the user manually edits it', async () => {
    apiFetchMock.mockResolvedValue({ sprints: [] });
    const mod = await loadSprintsModule();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender: vi.fn().mockResolvedValue(undefined),
      invalidateSprintChartsCache: vi.fn(),
    });

    const weeksSelect = document.getElementById('sprintDefaultWeeksSelect') as HTMLSelectElement | null;
    const endInput = document.getElementById('sprintEndInput') as HTMLInputElement | null;
    if (!weeksSelect || !endInput) throw new Error('missing sprint defaults controls');

    const originalEnd = endInput.value;
    weeksSelect.value = '2';
    weeksSelect.dispatchEvent(new Event('change', { bubbles: true }));
    expect(endInput.value).not.toBe(originalEnd);

    endInput.value = '2030-01-01T10:00';
    endInput.dispatchEvent(new Event('input', { bubbles: true }));
    weeksSelect.value = '1';
    weeksSelect.dispatchEvent(new Event('change', { bubbles: true }));
    expect(endInput.value).toBe('2030-01-01T10:00');
  });

  it('creates sprints, persists default weeks best-effort, and refreshes sprint chips instead of invalidating the board', async () => {
    apiFetchMock.mockResolvedValue({ sprints: [] });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();
    setBoardMock.mockClear();

    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/sprints' && init?.method === 'POST') {
        return {};
      }
      if (url === '/api/board/alpha/settings' && init?.method === 'PATCH') {
        return { defaultSprintWeeks: 1 };
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });

    const nameInput = document.getElementById('sprintNameInput') as HTMLInputElement | null;
    const startInput = document.getElementById('sprintStartInput') as HTMLInputElement | null;
    const endInput = document.getElementById('sprintEndInput') as HTMLInputElement | null;
    const weeksSelect = document.getElementById('sprintDefaultWeeksSelect') as HTMLSelectElement | null;
    const createBtn = document.getElementById('createSprintBtn');
    if (!nameInput || !startInput || !endInput || !weeksSelect || !(createBtn instanceof HTMLElement)) {
      throw new Error('missing sprint create controls');
    }

    nameInput.value = 'Release Sprint';
    startInput.value = '2026-04-13T09:00';
    endInput.value = '2026-04-19T23:59';
    weeksSelect.value = '1';
    createBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(2);
    expect(apiFetchMock).toHaveBeenNthCalledWith(1, '/api/board/alpha/sprints', {
      method: 'POST',
      body: JSON.stringify({
        name: 'Release Sprint',
        plannedStartAt: new Date('2026-04-13T09:00').getTime(),
        plannedEndAt: new Date('2026-04-19T23:59').getTime(),
      }),
    });
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/board/alpha/settings', {
      method: 'PATCH',
      body: JSON.stringify({ defaultSprintWeeks: 1 }),
    });
    expect(setBoardMock).toHaveBeenCalledTimes(1);
    expect(invalidateSprintChartsCache).toHaveBeenCalledTimes(1);
    expect(refreshSprintsAndChipsMock).toHaveBeenCalledWith('alpha');
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(emitMock).not.toHaveBeenCalled();
  });

  it('shows a localized backend validation reason when sprint creation fails', async () => {
    apiFetchMock.mockResolvedValue({ sprints: [] });
    const mod = await loadSprintsModule('de');
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();

    const err = new Error('validation: a sprint with this name already exists in the project') as Error & { data?: unknown };
    err.data = {
      error: {
        code: 'VALIDATION_ERROR',
        message: 'validation: a sprint with this name already exists in the project',
        details: { reason: 'sprint_name_exists' },
      },
    };
    apiFetchMock.mockRejectedValueOnce(err);

    const nameInput = document.getElementById('sprintNameInput') as HTMLInputElement | null;
    const startInput = document.getElementById('sprintStartInput') as HTMLInputElement | null;
    const endInput = document.getElementById('sprintEndInput') as HTMLInputElement | null;
    const createBtn = document.getElementById('createSprintBtn');
    if (!nameInput || !startInput || !endInput || !(createBtn instanceof HTMLElement)) {
      throw new Error('missing sprint create controls');
    }

    nameInput.value = 'Release Sprint';
    startInput.value = '2026-04-13T09:00';
    endInput.value = '2026-04-19T23:59';
    createBtn.click();
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith(deCatalog['errors.VALIDATION_ERROR.sprint_name_exists']);
    expect(invalidateSprintChartsCache).not.toHaveBeenCalled();
    expect(refreshSprintsAndChipsMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('shows localized activation confirm with raw sprint name and locale-formatted planned date', async () => {
    const plannedStartAt = new Date('2026-06-01T09:00:00').getTime();
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED', { name: 'Release Train', plannedStartAt })],
    });
    const i18n = await initSprintI18n('de');
    const mod = await loadSprintsModule('de');
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();

    const activateBtn = document.querySelector('[data-sprint-activate="1"]');
    if (!(activateBtn instanceof HTMLElement)) throw new Error('missing sprint activate button');
    activateBtn.click();
    await flushPromises();

    const plannedLabel = i18n.formatDate(plannedStartAt, SPRINT_DATE_OPTS);
    const expectedMessage = i18n.t('settings.sprints.activateConfirm.message', {
      name: 'Release Train',
      plannedDate: plannedLabel,
    });
    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      expectedMessage,
      deCatalog['settings.sprints.activateConfirm.title'],
      deCatalog['settings.sprints.activateConfirm.confirm']
    );
    expect(expectedMessage).toContain('Release Train');
    expect(expectedMessage).toContain(plannedLabel);
    expect(expectedMessage).not.toContain('will start now');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it('activates planned sprints and emits sprint-updated without refreshing sprint chips', async () => {
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED', { plannedStartAt: Date.now() })],
    });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();

    const activateBtn = document.querySelector('[data-sprint-activate="1"]');
    if (!(activateBtn instanceof HTMLElement)) throw new Error('missing sprint activate button');
    activateBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).not.toHaveBeenCalled();
    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/sprints/1/activate', {
      method: 'POST',
    });
    expect(invalidateSprintChartsCache).toHaveBeenCalledTimes(1);
    expect(emitMock).toHaveBeenCalledWith('sprint-updated', { sprintId: 1, state: 'ACTIVE' });
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(refreshSprintsAndChipsMock).not.toHaveBeenCalled();
  });

  it('closes active sprints and emits sprint-updated without refreshing sprint chips', async () => {
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(2, 'ACTIVE')],
    });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();

    const closeBtn = document.querySelector('[data-sprint-close="2"]');
    if (!(closeBtn instanceof HTMLElement)) throw new Error('missing sprint close button');
    closeBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/sprints/2/close', {
      method: 'POST',
    });
    expect(invalidateSprintChartsCache).toHaveBeenCalledTimes(1);
    expect(emitMock).toHaveBeenCalledWith('sprint-updated', { sprintId: 2, state: 'CLOSED' });
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(refreshSprintsAndChipsMock).not.toHaveBeenCalled();
  });

  it('saves edited sprint fields through PATCH and then refreshes sprint chips', async () => {
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED')],
    });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    const editBtn = document.querySelector('[data-sprint-edit="1"]');
    if (!(editBtn instanceof HTMLElement)) throw new Error('missing sprint edit button');
    editBtn.click();
    await flushPromises();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();
    refreshSprintsAndChipsMock.mockClear();
    emitMock.mockClear();
    rerender.mockClear();

    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/sprints/1' && init?.method === 'PATCH') {
        return {};
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });

    const nameInput = document.querySelector('[data-sprint-edit-name]') as HTMLInputElement | null;
    const startInput = document.querySelector('[data-sprint-edit-start]') as HTMLInputElement | null;
    const endInput = document.querySelector('[data-sprint-edit-end]') as HTMLInputElement | null;
    const saveBtn = document.querySelector('[data-sprint-save="1"]');
    if (!nameInput || !startInput || !endInput || !(saveBtn instanceof HTMLElement)) {
      throw new Error('missing sprint edit controls');
    }

    nameInput.value = 'Sprint 1 updated';
    startInput.value = '2026-04-14T09:00';
    endInput.value = '2026-04-20T23:59';
    saveBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/sprints/1', {
      method: 'PATCH',
      body: JSON.stringify({
        name: 'Sprint 1 updated',
        plannedStartAt: new Date('2026-04-14T09:00').getTime(),
        plannedEndAt: new Date('2026-04-20T23:59').getTime(),
      }),
    });
    expect(invalidateSprintChartsCache).toHaveBeenCalledTimes(1);
    expect(refreshSprintsAndChipsMock).toHaveBeenCalledWith('alpha');
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(emitMock).not.toHaveBeenCalled();

    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED')],
    });
    const postSaveHtml = await mod.renderSprintsTabContent();
    expect(postSaveHtml).not.toContain('settings-sprint-row--editing');
  });

  it('deletes sprints through DELETE and then refreshes sprint chips', async () => {
    showConfirmDialogMock.mockResolvedValue(true);
    apiFetchMock.mockResolvedValue({
      sprints: [makeSprint(1, 'PLANNED', { todoCount: 2 })],
    });
    const mod = await loadSprintsModule();
    const rerender = vi.fn().mockResolvedValue(undefined);
    const invalidateSprintChartsCache = vi.fn();

    render(await mod.renderSprintsTabContent());
    mod.bindSprintsTabInteractions({
      signal: new AbortController().signal,
      rerender,
      invalidateSprintChartsCache,
    });

    apiFetchMock.mockClear();
    recordLocalMutationMock.mockClear();
    refreshSprintsAndChipsMock.mockClear();
    emitMock.mockClear();

    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/sprints/1' && init?.method === 'DELETE') {
        return {};
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });

    const deleteBtn = document.querySelector('[data-sprint-delete="1"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing sprint delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/sprints/1', {
      method: 'DELETE',
    });
    expect(invalidateSprintChartsCache).toHaveBeenCalledTimes(1);
    expect(refreshSprintsAndChipsMock).toHaveBeenCalledWith('alpha');
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(emitMock).not.toHaveBeenCalled();
  });
});
