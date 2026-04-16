// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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

async function loadTagsModule() {
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

  afterEach(() => {
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
      { name: 'zeta', color: '#00ff00', tagId: 9, canDelete: true },
      { name: 'alpha', color: null, tagId: null, canDelete: true },
      { name: 'bug', color: '#ff0000', tagId: 5, canDelete: true },
    ]);
    const mod = await loadTagsModule();

    const html = await mod.loadTagSettingsContent('/api/projects/7/tags');

    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects/7/tags');
    expect(setTagColorsMock).toHaveBeenCalledWith({
      bug: '#ff0000',
      zeta: '#00ff00',
    });
    expect(html.indexOf('alpha')).toBeLessThan(html.indexOf('bug'));
    expect(html.indexOf('bug')).toBeLessThan(html.indexOf('zeta'));

    render(html);
    const alphaPicker = document.querySelector('.settings-color-picker[data-tag="alpha"]');
    const alphaDelete = document.querySelector('.settings-tag-delete[data-tag="alpha"]');
    const bugDelete = document.querySelector('.settings-tag-delete[data-tag="bug"]');
    if (!(alphaPicker instanceof HTMLInputElement)) throw new Error('missing alpha picker');
    expect(alphaPicker.disabled).toBe(true);
    expect(alphaDelete).toBeNull();
    expect(bugDelete).not.toBeNull();

    apiFetchMock.mockClear();
    const cachedHtml = await mod.loadTagSettingsContent('/api/projects/7/tags');
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
      hasProjectAccess: true,
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
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42');
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
      hasProjectAccess: true,
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
    expect(invalidateBoardMock).toHaveBeenCalledWith('alpha', 'bug', 'query', '42');
    expect(rerender).not.toHaveBeenCalled();
  });

  it('uses the durable project color route when project settings are rendered without a board slug', async () => {
    selectorState.projectId = 7;
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`<input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      hasProjectAccess: true,
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

  it('deletes durable project tags through rerender-first flow and only invalidates the board when a slug exists', async () => {
    selectorState.projectId = 7;
    selectorState.tagColors = { bug: '#ff0000', keep: '#00ff00' };
    showConfirmDialogMock.mockResolvedValue(true);
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`<button class="settings-tag-delete" data-tag="bug" data-tag-id="5">Delete</button>`);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      hasProjectAccess: true,
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

  it('does not bind tag interactions when project access is unavailable', async () => {
    selectorState.slug = 'alpha';
    const rerender = vi.fn().mockResolvedValue(undefined);
    const mod = await loadTagsModule();

    render(`
      <input type="color" class="settings-color-picker" data-tag="bug" data-tag-id="5" value="#ff0000" />
      <button class="settings-tag-delete" data-tag="bug" data-tag-id="5">Delete</button>
    `);
    mod.bindTagTabInteractions({
      signal: new AbortController().signal,
      hasProjectAccess: false,
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
