// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

const apiFetchMock = vi.hoisted(() => vi.fn());
const invalidateBoardMock = vi.hoisted(() => vi.fn());
const setBulkUpdatingMock = vi.hoisted(() => vi.fn());
const selectorState = vi.hoisted(() => ({
  board: {
    project: { id: 1 },
    tags: [{ name: 'Bug', color: '#ff0000' }],
    columnOrder: [
      { key: 'backlog', name: 'Backlog' },
      { key: 'done', name: 'Done' },
    ],
    columns: {
      backlog: [
        { id: 1, localId: 1, title: 'One', body: '', status: 'backlog', tags: [] },
        { id: 2, localId: 2, title: 'Two', body: '', status: 'backlog', tags: [] },
      ],
    },
  } as any,
  slug: 'alpha',
  tag: '',
  search: '',
  sprintId: null as string | null,
  boardMembers: [] as any[],
}));

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  showToast: vi.fn(),
  isAnonymousBoard: () => false,
  sanitizeHexColor: (color?: string | null) => {
    if (color && /^#[0-9a-fA-F]{6}$/.test(color.trim())) return color.trim();
    return null;
  },
}));

vi.mock('../field-tooltips.js', () => ({
  applyFieldTooltips: vi.fn(),
  BULK_EDIT_TOOLTIPS: {},
}));

vi.mock('../state/selectors.js', () => ({
  getBoard: () => selectorState.board,
  getSlug: () => selectorState.slug,
  getTag: () => selectorState.tag,
  getSearch: () => selectorState.search,
  getSprintIdFromUrl: () => selectorState.sprintId,
  getBoardMembers: () => selectorState.boardMembers,
}));

vi.mock('../orchestration/board-refresh.js', () => ({
  invalidateBoard: invalidateBoardMock,
}));

vi.mock('../realtime/guard.js', () => ({
  setBulkUpdating: setBulkUpdatingMock,
}));

vi.mock('../sprints.js', () => ({
  normalizeSprints: (value: { sprints?: any[] } | null | undefined) => value?.sprints ?? [],
  boardSprintsEnabled: (board: { project?: { sprintsEnabled?: boolean } } | null | undefined) =>
    board?.project?.sprintsEnabled !== false,
}));

vi.mock('./settings.js', () => ({
  invalidateTagsCache: vi.fn(),
}));

vi.mock('./todo.js', () => ({
  normalizeTagName: (value: string) => value.trim(),
  resolveColumnKey: (value: string) => value,
}));

function installBulkEditDOM(): void {
  document.body.innerHTML = `
    <dialog id="bulkEditDialog">
      <form id="bulkEditForm">
        <div id="bulkEditDialogTitle"></div>
        <div id="bulkEditHint"></div>
        <div id="bulkEditAssigneeRow"></div>
        <div id="bulkEditSprintRow"></div>
        <div id="bulkEditTagsRow"></div>
        <div id="bulkEditEstimationRow"></div>
        <input id="bulkApplyAssignee" type="checkbox" />
        <input id="bulkApplySprint" type="checkbox" />
        <input id="bulkApplyStatus" type="checkbox" />
        <input id="bulkApplyTags" type="checkbox" />
        <input id="bulkApplyEstimation" type="checkbox" />
        <select id="bulkAssignee"></select>
        <select id="bulkSprint"></select>
        <select id="bulkStatus"></select>
        <select id="bulkEstimation"></select>
        <input id="bulkTagsInput" />
        <button id="bulkAddTagBtn" type="button"></button>
        <div id="bulkTagsChips"></div>
        <button id="saveBulkEditBtn" type="submit"></button>
        <button id="closeBulkEditBtn" type="button"></button>
        <button id="cancelBulkEditBtn" type="button"></button>
      </form>
    </dialog>
  `;
  const dialog = document.getElementById('bulkEditDialog') as HTMLDialogElement;
  dialog.showModal = vi.fn(() => {
    dialog.setAttribute('open', '');
    dialog.open = true;
  });
}

async function initI18n(locale: 'en' | 'de') {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: vi.fn(async (next: 'en' | 'de') => (next === 'de' ? deCatalog : enCatalog)),
  });
  return i18n;
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

describe('bulk edit i18n', () => {
  beforeEach(() => {
    vi.resetModules();
    installBulkEditDOM();
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({ sprints: [] });
    invalidateBoardMock.mockReset();
    setBulkUpdatingMock.mockReset();
    delete selectorState.board.project.sprintsEnabled;
  });

  afterEach(async () => {
    const i18n = await import('../i18n/index.js');
    i18n.resetI18nForTests();
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('relocalizes open dialog copy and tag aria labels without changing selected state', async () => {
    const i18n = await initI18n('en');
    const bulkEdit = await import('./bulk-edit.js');
    bulkEdit.initBulkEditDialog(vi.fn());

    await bulkEdit.openBulkEditDialog([1, 2], { role: 'maintainer', onPruned: vi.fn() });
    await flushPromises();

    expect(document.getElementById('bulkEditDialogTitle')?.textContent).toBe(enCatalog['board.bulkEdit.title']);
    expect(document.getElementById('bulkEditHint')?.textContent).toBe(i18n.t('board.bulkEdit.editingMultiple', { count: 2 }));

    const tagsInput = document.getElementById('bulkTagsInput') as HTMLInputElement;
    tagsInput.value = 'Bug';
    (document.getElementById('bulkAddTagBtn') as HTMLButtonElement).click();
    expect(document.querySelector('.tag-chip-remove')?.getAttribute('aria-label')).toBe(enCatalog['board.bulkEdit.removeTag']);

    (document.getElementById('bulkApplyTags') as HTMLInputElement).checked = true;
    tagsInput.value = 'draft';
    apiFetchMock.mockClear();

    await i18n.setLocale('de');
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(document.getElementById('bulkEditDialogTitle')?.textContent).toBe(deCatalog['board.bulkEdit.title']);
    expect(document.getElementById('bulkEditHint')?.textContent).toBe(i18n.t('board.bulkEdit.editingMultiple', { count: 2 }));
    expect(document.querySelector('.tag-chip-remove')?.getAttribute('aria-label')).toBe(deCatalog['board.bulkEdit.removeTag']);
    expect((document.getElementById('bulkApplyTags') as HTMLInputElement).checked).toBe(true);
    expect((document.getElementById('bulkTagsInput') as HTMLInputElement).value).toBe('draft');
  });

  it('does not submit a stale sprint mutation after the board is disabled', async () => {
    await initI18n('en');
    const bulkEdit = await import('./bulk-edit.js');
    bulkEdit.initBulkEditDialog(vi.fn());
    apiFetchMock.mockResolvedValueOnce({
      sprints: [{ id: 9, name: 'Sprint 9', state: 'PLANNED' }],
    });
    await bulkEdit.openBulkEditDialog([1], { role: 'maintainer', onPruned: vi.fn() });

    (document.getElementById('bulkApplySprint') as HTMLInputElement).checked = true;
    (document.getElementById('bulkSprint') as HTMLSelectElement).value = '9';
    selectorState.board.project.sprintsEnabled = false;
    apiFetchMock.mockClear();
    (document.getElementById('bulkEditForm') as HTMLFormElement).dispatchEvent(
      new SubmitEvent('submit', { bubbles: true, cancelable: true }),
    );
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
  });
});
