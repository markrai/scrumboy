// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import deCatalog from '../i18n/locales/de.json';
import enCatalog from '../i18n/locales/en.json';

const tagLocales: Record<string, Record<string, string>> = {
  en: enCatalog as Record<string, string>,
  de: deCatalog as Record<string, string>,
};

async function initTagI18n(locale: 'en' | 'de' = 'en'): Promise<typeof import('../i18n/index.js')> {
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale,
    loadLocale: async (code) => tagLocales[code] ?? tagLocales.en,
  });
  return i18n;
}

const selectorState: {
  search: string;
  projectId: number | null;
  slug: string | null;
  tag: string;
  tagColors: Record<string, string>;
  user: { id: number } | null;
} = {
  search: '',
  projectId: null,
  slug: null,
  tag: '',
  tagColors: {},
  user: null,
};

const apiFetchMock = vi.fn();
const invalidateBoardMock = vi.fn().mockResolvedValue(undefined);
const recordLocalMutationMock = vi.fn();
const setTagColorsMock = vi.fn((colors: Record<string, string>) => {
  selectorState.tagColors = { ...colors };
});
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
  getSearch: () => selectorState.search,
  getSettingsProjectId: () => selectorState.projectId,
  getSlug: () => selectorState.slug,
  getSprintIdFromUrl: () => new URL(window.location.href).searchParams.get('sprintId'),
  getTag: () => selectorState.tag,
  getTagColors: () => selectorState.tagColors,
  getUser: () => selectorState.user,
}));

vi.mock('../state/mutations.js', () => ({
  setTagColors: setTagColorsMock,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  sanitizeHexColor: (color?: string | null, fallback?: string) => {
    if (color && /^#[0-9a-fA-F]{6}$/.test(color.trim())) return color.trim();
    return fallback ?? null;
  },
  showConfirmDialog: showConfirmDialogMock,
  showToast: showToastMock,
}));

function render(html: string): void {
  document.body.innerHTML = html;
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadTagsModule(locale: 'en' | 'de' = 'en') {
  await initTagI18n(locale);
  const mod = await import('./settings-tags.js');
  return mod;
}

describe('settings-tags', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/alpha?sprintId=42');
    selectorState.search = 'query';
    selectorState.projectId = null;
    selectorState.slug = null;
    selectorState.tag = 'bug';
    selectorState.tagColors = {};
    selectorState.user = null;
    apiFetchMock.mockReset();
    invalidateBoardMock.mockClear();
    invalidateBoardMock.mockResolvedValue(undefined);
    recordLocalMutationMock.mockClear();
    setTagColorsMock.mockClear();
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
    selectorState.search = '';
    selectorState.projectId = null;
    selectorState.slug = null;
    selectorState.tag = '';
    selectorState.tagColors = {};
    selectorState.user = null;
  });

  it('loads tags sorted by name, updates local tag colors, and respects durable rendering rules', async () => {
    selectorState.projectId = 7;
    apiFetchMock.mockResolvedValue([
      { name: 'zeta', color: '#00ff00', tagId: 9, deleteScope: 'project', canDelete: true, canUpdateColor: true },
      { name: 'alpha', color: null, deleteScope: 'mine', canDelete: true, canUpdateColor: true },
      { name: 'bug', color: '#ff0000', tagId: 5, deleteScope: 'none', canDelete: false, canUpdateColor: false },
    ]);
    const mod = await loadTagsModule();

    const html = await mod.loadTagSettingsContent('/api/projects/7/tags', 'project');

    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags');
    expect(setTagColorsMock).toHaveBeenCalledWith({
      bug: '#ff0000',
      zeta: '#00ff00',
    });
    expect(html.indexOf('alpha')).toBeLessThan(html.indexOf('bug'));
    expect(html.indexOf('bug')).toBeLessThan(html.indexOf('zeta'));

    render(html);
    const alphaPicker = document.querySelector('.settings-color-picker[data-tag="alpha"]');
    const bugPicker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    const alphaDelete = document.querySelector('.settings-tag-delete[data-tag="alpha"]');
    const bugDelete = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    const zetaDelete = document.querySelector('.settings-tag-delete[data-tag="zeta"]');
    if (!(alphaPicker instanceof HTMLInputElement)) throw new Error('missing alpha picker');
    if (!(bugPicker instanceof HTMLInputElement)) throw new Error('missing bug picker');
    // Grouped personal labels have no tagId but the picker is always usable and
    // delete is driven by deleteScope, so alpha (deleteScope "mine") is deletable.
    // Durable board-scoped tags with canUpdateColor false disable the shared picker.
    expect(alphaPicker.disabled).toBe(false);
    expect(bugPicker.disabled).toBe(true);
    expect(alphaDelete).not.toBeNull();
    expect(bugDelete).toBeNull();
    expect(zetaDelete).not.toBeNull();

    apiFetchMock.mockClear();
    const cachedHtml = await mod.loadTagSettingsContent('/api/projects/7/tags', 'project');
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(cachedHtml).toBe(html);
  });

  it('updates board tag colors by tag id and invalidates the board without rerendering', async () => {
    selectorState.slug = 'alpha';
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`
      <input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />
      <button class="settings-color-clear" data-tag="bug" data-tag-id="5">Clear</button>
    `);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'board',
      rerender,
    });

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing tag color picker');
    picker.value = '#123456';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/tags/id/5/color', {
      method: 'PATCH',
      body: JSON.stringify({ color: '#123456' }),
    });
    expect(selectorState.tagColors).toEqual({ bug: '#123456' });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, null);
    expect(rerender).not.toHaveBeenCalled();
  });

  it('clears board tag colors through the name-based board route when tagId is absent', async () => {
    selectorState.slug = 'alpha';
    selectorState.tagColors = { bug: '#ff0000', keep: '#00ff00' };
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`<button class="settings-color-clear" data-tag="bug">Clear</button>`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'board',
      rerender,
    });

    const clearBtn = document.querySelector('.settings-color-clear[data-tag="bug"]');
    if (!(clearBtn instanceof HTMLElement)) throw new Error('missing tag clear button');
    clearBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/tags/bug/color', {
      method: 'PATCH',
      body: JSON.stringify({ color: null }),
    });
    expect(selectorState.tagColors).toEqual({ keep: '#00ff00' });
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42', null, null, null);
    expect(rerender).not.toHaveBeenCalled();
  });

  it('uses the durable project color route for explicit project scope', async () => {
    selectorState.projectId = 7;
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`<input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'project',
      rerender,
    });

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing durable tag color picker');
    picker.value = '#abcdef';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags/id/5/color', {
      method: 'PATCH',
      body: JSON.stringify({ color: '#abcdef' }),
    });
    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('uses mine routes for the global personal library even when settingsProjectId is set for charts', async () => {
    // Projects-screen Settings sets settingsProjectId for burndown charts, but the
    // Tag Colors list is /api/tags/mine — mutations must not use that arbitrary project.
    selectorState.projectId = 7;
    showConfirmDialogMock.mockResolvedValue(true);
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`
      <input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="42" value="#ff0000" />
      <button class="settings-tag-delete" data-tag="bug" data-tag-id="42" data-delete-scope="mine">Delete</button>
    `);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'mine',
      rerender,
    });

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing mine picker');
    picker.value = '#abcdef';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/tags/mine/42/color', {
      method: 'PATCH',
      body: JSON.stringify({ color: '#abcdef' }),
    });

    apiFetchMock.mockClear();
    const deleteBtn = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing mine delete');
    deleteBtn.click();
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/tags/mine/42', {
      method: 'DELETE',
    });
  });

  it('updates and deletes durable personal labels through name-based routes when tagId is absent', async () => {
    selectorState.projectId = 7;
    showConfirmDialogMock.mockResolvedValue(true);
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`
      <input type="color" class="settings-color-picker" data-tag="bug" value="#ff0000" />
      <button class="settings-tag-delete" data-tag="bug" data-delete-scope="mine">Delete</button>
    `);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'project',
      rerender,
    });

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing durable name picker');
    picker.value = '#abcdef';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags/bug/color', {
      method: 'PATCH',
      body: JSON.stringify({ color: '#abcdef' }),
    });

    apiFetchMock.mockClear();
    const deleteBtn = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing durable name delete');
    deleteBtn.click();
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags/bug', {
      method: 'DELETE',
    });
  });

  it('shows a localized backend validation reason when a tag color update fails', async () => {
    selectorState.projectId = 7;
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule('de');

    render(`<input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'project',
      rerender,
    });

    const err = new Error('validation: invalid tag color "nope"') as Error & { data?: unknown };
    err.data = {
      error: {
        code: 'VALIDATION_ERROR',
        message: 'validation: invalid tag color "nope"',
        details: { reason: 'invalid_tag_color' },
      },
    };
    apiFetchMock.mockRejectedValueOnce(err);

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing durable tag color picker');
    picker.value = '#abcdef';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith(deCatalog['errors.VALIDATION_ERROR.invalid_tag_color']);
    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });

  it('deletes durable project tags through rerender-first flow and only invalidates the board when a slug exists', async () => {
    selectorState.projectId = 7;
    selectorState.tagColors = { bug: '#ff0000', keep: '#00ff00' };
    showConfirmDialogMock.mockResolvedValue(true);
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`<button class="settings-tag-delete" data-tag="bug" data-tag-id="5">Delete</button>`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'project',
      rerender,
    });

    const deleteBtn = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing tag delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledTimes(1);
    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags/id/5', {
      method: 'DELETE',
    });
    expect(selectorState.tagColors).toEqual({ keep: '#00ff00' });
    expect(rerender).toHaveBeenCalledTimes(1);
    expect(invalidateBoardMock).not.toHaveBeenCalled();
  });

  it('uses localized delete confirm copy with the raw tag name', async () => {
    selectorState.projectId = 7;
    showConfirmDialogMock.mockResolvedValue(false);
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule('de');

    render(`<button class="settings-tag-delete" data-tag="bug" data-tag-id="5">Delete</button>`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: 'project',
      rerender,
    });

    const deleteBtn = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing tag delete button');
    deleteBtn.click();
    await flushPromises();

    expect(showConfirmDialogMock).toHaveBeenCalledWith(
      deCatalog['settings.tagColors.deleteConfirm.message'].replace('{name}', 'bug'),
      deCatalog['settings.tagColors.deleteConfirm.title'],
    );
  });

  it('does not bind tag interactions when scope is unavailable', async () => {
    selectorState.slug = 'alpha';
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`
      <input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />
      <button class="settings-tag-delete" data-tag="bug" data-tag-id="5">Delete</button>
    `);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      scope: null,
      rerender,
    });

    const picker = document.querySelector('.settings-color-picker[data-tag="bug"]');
    const deleteBtn = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(picker instanceof HTMLInputElement)) throw new Error('missing no-access picker');
    if (!(deleteBtn instanceof HTMLElement)) throw new Error('missing no-access delete button');

    picker.value = '#123456';
    picker.dispatchEvent(new Event('change', { bubbles: true }));
    deleteBtn.click();
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(recordLocalMutationMock).not.toHaveBeenCalled();
    expect(rerender).not.toHaveBeenCalled();
  });
});
