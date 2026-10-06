// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import enCatalog from '../i18n/locales/en.json';

const selectorState: {
  availableTags: string[];
  availableTagsMap: Record<string, string>;
  autocompleteSuggestion: string | null;
  board: any;
  slug: string | null;
  tagColors: Record<string, string>;
} = {
  availableTags: [],
  availableTagsMap: {},
  autocompleteSuggestion: null,
  board: null,
  slug: 'alpha',
  tagColors: {},
};

const apiFetchMock = vi.fn();
const showToastMock = vi.fn();

const permissionState = {
  canChangeSprint: false,
  canChangeEstimation: true,
  canEditTags: true,
  canEditNotes: true,
  canEditAssignment: true,
  canDeleteTodo: false,
  canEditTitle: true,
  canEditStatus: true,
  canSubmitTodo: true,
  canEditLinks: false,
  canArchiveTodo: false,
  canRestoreTodo: false,
};

function installTodoDom(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog">
      <h2 id="todoDialogTitle"></h2>
      <form id="todoForm">
        <input id="todoTitle" />
        <div id="todoBodyToggle">
          <button id="todoBodyWriteTab" type="button"></button>
          <button id="todoBodyPreviewTab" type="button"></button>
        </div>
        <textarea id="todoBody"></textarea>
        <div id="todoBodyPreview"></div>
        <input id="todoTags" />
        <select id="todoStatus"></select>
        <select id="todoPriority"></select>
        <select id="todoAssignee"></select>
        <select id="todoSprint"></select>
        <select id="todoEstimationPoints"></select>
        <div id="todoEstimationField"></div>
        <div id="todoAssigneeField"></div>
        <div id="todoSprintField"></div>
        <div id="todoLinksField"></div>
        <div id="todoDialogCreated"><span class="todo-dialog-datetime-value"></span><span id="todoDialogCreatedBy"></span></div>
        <div id="todoDialogUpdated"><span class="todo-dialog-datetime-value"></span></div>
        <button id="closeTodoBtn"></button>
        <button id="deleteTodoBtn"></button>
        <button id="shareTodoBtn"></button>
        <button id="addTagBtn"></button>
        <div id="tagsChips"></div>
        <button id="saveTodoBtn"></button>
      </form>
    </dialog>
  `;
  const dialog = document.getElementById('todoDialog') as HTMLDialogElement;
  dialog.showModal = function showModalStub() {
    this.open = true;
  };
  dialog.close = function closeStub() {
    this.open = false;
    this.dispatchEvent(new Event('close'));
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

function boardWithTags() {
  return {
    columnOrder: [{ key: 'backlog', name: 'Backlog' }],
    tags: [
      { name: 'active', count: 2, color: '#ff0000' },
      { name: 'archive-only', count: 0 },
    ],
  };
}

async function loadOpenTodoDialog() {
  vi.doMock('../api.js', () => ({ apiFetch: apiFetchMock }));
  vi.doMock('../state/selectors.js', () => ({
    getAutocompleteSuggestion: () => selectorState.autocompleteSuggestion,
    getAvailableTags: () => selectorState.availableTags,
    getAvailableTagsMap: () => selectorState.availableTagsMap,
    getBoard: () => selectorState.board,
    getBoardMembers: () => [],
    getMarkdownNotesEnabled: () => false,
    getMermaidNotesEnabled: () => false,
    getSlug: () => selectorState.slug,
    getTagColors: () => selectorState.tagColors,
    getUser: () => null,
  }));
  vi.doMock('../state/mutations.js', () => ({
    setAutocompleteSuggestion: (next: string | null) => {
      selectorState.autocompleteSuggestion = next;
    },
    setAvailableTags: (tags: string[]) => {
      selectorState.availableTags = tags;
    },
    setAvailableTagsMap: (map: Record<string, string>) => {
      selectorState.availableTagsMap = map;
    },
    setEditingTodo: vi.fn(),
    setTagColors: (colors: Record<string, string>) => {
      selectorState.tagColors = colors;
    },
  }));
  vi.doMock('../utils.js', () => ({
    escapeHTML: (s: string) =>
      String(s)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#039;'),
    isAnonymousBoard: () => false,
    sanitizeHexColor: (color?: string | null) => {
      if (color && /^#[0-9a-fA-F]{6}$/.test(color.trim())) return color.trim();
      return null;
    },
    showConfirmDialog: vi.fn(),
    showToast: showToastMock,
  }));
  vi.doMock('../sprints.js', () => ({
    normalizeSprints: () => [],
    boardSprintsEnabled: () => false,
  }));
  vi.doMock('./todo-links.js', () => ({
    bindShareTodoButton: vi.fn(),
    bindTodoDialogLinkLifecycle: vi.fn(),
    initializeTodoDialogLinks: vi.fn(),
    resetTodoDialogLinks: vi.fn(),
  }));
  vi.doMock('./todo-permissions.js', () => ({
    computeTodoDialogPermissions: () => ({ ...permissionState }),
    getTodoFormPermissions: () => permissionState,
    setTodoFormPermissions: vi.fn(),
  }));

  const tagsMod = await import('./todo-tags.js');
  const i18n = await import('../i18n/index.js');
  await i18n.initI18n({
    locale: 'en',
    loadLocale: async () => enCatalog,
    storage: null,
  });
  const { openTodoDialog, requestTodoDialogClose } = await import('./todo.js');
  return { openTodoDialog, requestTodoDialogClose, tagsMod };
}

describe('todo dialog project tag catalog hydrate', () => {
  beforeEach(() => {
    vi.resetModules();
    apiFetchMock.mockReset();
    showToastMock.mockReset();
    selectorState.availableTags = [];
    selectorState.availableTagsMap = {};
    selectorState.autocompleteSuggestion = null;
    selectorState.board = boardWithTags();
    selectorState.slug = 'alpha';
    selectorState.tagColors = {};
    permissionState.canEditTags = true;
    permissionState.canSubmitTodo = true;
    permissionState.canEditTitle = true;
    permissionState.canEditStatus = true;
    permissionState.canEditNotes = true;
    installTodoDom();
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it('installs synchronous fallback immediately and hydrates the full catalog without blocking open', async () => {
    const pending = deferred<unknown>();
    apiFetchMock.mockImplementation((path: string) => {
      if (String(path).includes('/tags')) return pending.promise;
      return Promise.resolve([]);
    });

    const { openTodoDialog } = await loadOpenTodoDialog();
    const opened = openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });

    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    expect(document.getElementById('showAllProjectTagsBtn')).toBeNull();
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/tags');

    pending.resolve([
      { name: 'historical', count: 0 },
      { name: 'active', count: 2, color: '#ff0000' },
    ]);
    await opened;
    await flushPromises();

    expect(selectorState.availableTags).toEqual(['historical', 'active', 'story-only']);
    expect(showToastMock).not.toHaveBeenCalled();
  });

  it('updates autocomplete from the fetched catalog, including historical tags', async () => {
    apiFetchMock.mockImplementation(async (path: string) => {
      if (String(path).includes('/tags')) {
        return [
          { name: 'historical', count: 0 },
          { name: 'active', count: 2 },
        ];
      }
      return [];
    });

    const { openTodoDialog } = await loadOpenTodoDialog();
    await openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });
    await flushPromises();

    const input = document.getElementById('todoTags') as HTMLInputElement;
    input.value = 'hist';
    input.setSelectionRange(4, 4);
    input.dispatchEvent(new Event('input', { bubbles: true }));

    expect(selectorState.autocompleteSuggestion).toBe('historical');
    expect(document.getElementById('tagAutocompleteSuggestion')).not.toBeNull();
  });

  it('merges chips added while the catalog request is in flight', async () => {
    const pending = deferred<unknown>();
    apiFetchMock.mockImplementation((path: string) => {
      if (String(path).includes('/tags')) return pending.promise;
      return Promise.resolve([]);
    });

    const { openTodoDialog, tagsMod } = await loadOpenTodoDialog();
    const opened = openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });

    tagsMod.renderTagsChips(['story-only', 'late-chip'], { canRemove: true });
    pending.resolve([{ name: 'historical', count: 0 }]);
    await opened;
    await flushPromises();

    expect(selectorState.availableTags).toEqual(['historical', 'story-only', 'late-chip']);
  });

  it('ignores a stale catalog response after a newer dialog open', async () => {
    const first = deferred<unknown>();
    const second = deferred<unknown>();
    let tagsCalls = 0;
    apiFetchMock.mockImplementation((path: string) => {
      if (!String(path).includes('/tags')) return Promise.resolve([]);
      tagsCalls += 1;
      return tagsCalls === 1 ? first.promise : second.promise;
    });

    const { openTodoDialog } = await loadOpenTodoDialog();
    const firstOpen = openTodoDialog({
      mode: 'edit',
      todo: { title: 'First', tags: ['first-tag'] },
      role: 'contributor',
    });
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/tags');
    expect(selectorState.availableTags).toEqual(['active', 'first-tag']);

    selectorState.slug = 'beta';
    selectorState.board = {
      columnOrder: [{ key: 'backlog', name: 'Backlog' }],
      tags: [{ name: 'beta-active', count: 1 }],
    };
    const secondOpen = openTodoDialog({
      mode: 'edit',
      todo: { title: 'Second', tags: ['second-tag'] },
      role: 'contributor',
    });
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/beta/tags');
    expect(selectorState.availableTags).toEqual(['beta-active', 'second-tag']);

    first.resolve([{ name: 'stale-historical', count: 0 }]);
    await firstOpen;
    await flushPromises();
    expect(selectorState.availableTags).toEqual(['beta-active', 'second-tag']);

    second.resolve([{ name: 'beta-historical', count: 0 }]);
    await secondOpen;
    await flushPromises();
    expect(selectorState.availableTags).toEqual(['beta-historical', 'second-tag']);
  });

  it('keeps the synchronous fallback when the catalog fetch fails and does not toast', async () => {
    const pending = deferred<unknown>();
    apiFetchMock.mockImplementation((path: string) => {
      if (String(path).includes('/tags')) return pending.promise;
      return Promise.resolve([]);
    });
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { openTodoDialog } = await loadOpenTodoDialog();
    const opened = openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });

    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    pending.reject(new Error('catalog down'));
    await opened;
    await flushPromises();

    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    expect(showToastMock).not.toHaveBeenCalled();
    expect(errorSpy).toHaveBeenCalled();
  });

  it('skips the catalog request for anonymous or no-slug boards', async () => {
    selectorState.slug = null;
    apiFetchMock.mockResolvedValue([]);

    const { openTodoDialog } = await loadOpenTodoDialog();
    await openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });
    await flushPromises();

    expect(apiFetchMock.mock.calls.some(([path]) => String(path).includes('/tags'))).toBe(false);
    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    expect(document.getElementById('showAllProjectTagsBtn')).toBeNull();
  });

  it('does not request the project tag catalog when tag editing is disabled', async () => {
    permissionState.canEditTags = false;
    apiFetchMock.mockResolvedValue([]);

    const { openTodoDialog } = await loadOpenTodoDialog();
    await openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });
    await flushPromises();

    expect(apiFetchMock.mock.calls.some(([path]) => String(path).includes('/tags'))).toBe(false);
    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
  });

  it('ignores a catalog response after the todo dialog has been closed', async () => {
    const pending = deferred<unknown>();
    apiFetchMock.mockImplementation((path: string) => {
      if (String(path).includes('/tags')) return pending.promise;
      return Promise.resolve([]);
    });

    const { openTodoDialog, requestTodoDialogClose } = await loadOpenTodoDialog();
    await openTodoDialog({
      mode: 'edit',
      todo: { title: 'Story', tags: ['story-only'] },
      role: 'contributor',
    });
    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/tags');

    await requestTodoDialogClose({ force: true, reason: 'cancel' });
    expect((document.getElementById('todoDialog') as HTMLDialogElement).open).toBe(false);

    pending.resolve([{ name: 'historical', count: 0 }]);
    await flushPromises();

    expect(selectorState.availableTags).toEqual(['active', 'story-only']);
    expect(selectorState.autocompleteSuggestion).toBeNull();
    expect(document.getElementById('tagAutocompleteSuggestion')).toBeNull();
  });
});
