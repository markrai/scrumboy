// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type LinkedStoryItem = {
  localId: number;
  title: string;
  linkType?: string;
};

const selectorState: {
  editingTodo: { localId: number; title?: string } | null;
  slug: string | null;
} = {
  editingTodo: { localId: 5, title: 'Current story' },
  slug: 'alpha',
};

const permissionState = {
  canEditLinks: true,
};

const apiFetchMock = vi.fn();
const recordLocalMutationMock = vi.fn();
const showToastMock = vi.fn();

vi.mock('../api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('../dom/elements.js', () => ({
  shareTodoBtn: document.getElementById('shareTodoBtn'),
  todoDialog: document.getElementById('todoDialog'),
}));

vi.mock('../state/selectors.js', () => ({
  getEditingTodo: () => selectorState.editingTodo,
  getSlug: () => selectorState.slug,
}));

vi.mock('../utils.js', () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#039;'),
  showToast: showToastMock,
}));

vi.mock('../realtime/guard.js', () => ({
  recordLocalMutation: recordLocalMutationMock,
}));

vi.mock('./todo-permissions.js', () => ({
  getTodoFormPermissions: () => permissionState,
}));

function renderLinksShell(): void {
  document.body.innerHTML = `
    <dialog id="todoDialog"></dialog>
    <div id="linksChips"></div>
    <div class="tags-input-container">
      <input id="linksSearchInput" type="text" />
      <button id="addLinkBtn" type="button">Add</button>
    </div>
    <button id="shareTodoBtn" type="button">Share</button>
  `;
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i++) {
    await Promise.resolve();
  }
}

async function loadTodoLinksModule() {
  const mod = await import('./todo-links.js');
  return mod;
}

describe('todo-links', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    renderLinksShell();
    selectorState.editingTodo = { localId: 5, title: 'Current story' };
    selectorState.slug = 'alpha';
    permissionState.canEditLinks = true;
    apiFetchMock.mockReset();
    recordLocalMutationMock.mockClear();
    showToastMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.resetModules();
    document.body.innerHTML = '';
    selectorState.editingTodo = { localId: 5, title: 'Current story' };
    selectorState.slug = 'alpha';
    permissionState.canEditLinks = true;
  });

  it('loads and caches linked stories, renders inbound/outbound chips, and navigates through the linked-todo callback', async () => {
    const navigate = vi.fn();
    apiFetchMock.mockResolvedValue({
      outbound: [{ localId: 7, title: 'Outbound', linkType: 'relates_to' }],
      inbound: [{ localId: 9, title: 'Inbound', linkType: 'blocks' }],
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5, navigate);

    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith('/api/board/alpha/todos/5/links');
    expect(document.querySelector('[data-link-direction="outbound"]')).not.toBeNull();
    expect(document.querySelector('[data-link-direction="inbound"]')).not.toBeNull();
    expect(document.querySelector('[data-link-remove="7"]')).not.toBeNull();

    const openBtn = document.querySelector('[data-link-open="7"]');
    if (!(openBtn instanceof HTMLElement)) throw new Error('missing linked story open button');
    openBtn.click();
    expect(navigate).toHaveBeenCalledWith('/alpha/t/7');

    await mod.initializeTodoDialogLinks('alpha', 5, navigate);
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
  });

  it('disables search/add controls and remove buttons when link editing is not allowed', async () => {
    permissionState.canEditLinks = false;
    apiFetchMock.mockResolvedValue({
      outbound: [{ localId: 7, title: 'Outbound' }],
      inbound: [{ localId: 9, title: 'Inbound' }],
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5);

    const input = document.getElementById('linksSearchInput') as HTMLInputElement | null;
    const addBtn = document.getElementById('addLinkBtn') as HTMLButtonElement | null;
    if (!input || !addBtn) throw new Error('missing link controls');

    expect(input.disabled).toBe(true);
    expect(addBtn.disabled).toBe(true);
    expect(document.querySelector('[data-link-remove]')).toBeNull();
  });

  it('adds direct #id links, records the mutation first, and rerenders the refreshed link set', async () => {
    let linksState = {
      outbound: [] as LinkedStoryItem[],
      inbound: [] as LinkedStoryItem[],
    };
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/todos/5/links' && !init) {
        return {
          outbound: linksState.outbound,
          inbound: linksState.inbound,
        };
      }
      if (url === '/api/board/alpha/todos/5/links' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ targetLocalId: 7 });
        linksState = {
          outbound: [{ localId: 7, title: 'Linked 7' }],
          inbound: [],
        };
        return {};
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5);

    const input = document.getElementById('linksSearchInput') as HTMLInputElement | null;
    if (!input) throw new Error('missing linksSearchInput');

    input.value = '#7';
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/board/alpha/todos/5/links', {
      method: 'POST',
      body: JSON.stringify({ targetLocalId: 7 }),
    });
    expect(apiFetchMock).toHaveBeenNthCalledWith(3, '/api/board/alpha/todos/5/links');
    expect(input.value).toBe('');
    expect(document.getElementById('linksAutocompleteSuggestion')).toBeNull();
    expect(document.querySelector('[data-link-open="7"]')).not.toBeNull();
  });

  it('searches with debounce and exclude params, then accepts the first suggestion through Tab', async () => {
    let linksState = {
      outbound: [{ localId: 2, title: 'Existing outbound' }] as LinkedStoryItem[],
      inbound: [{ localId: 9, title: 'Existing inbound' }] as LinkedStoryItem[],
    };
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/todos/5/links' && !init) {
        return {
          outbound: linksState.outbound,
          inbound: linksState.inbound,
        };
      }
      if (typeof url === 'string' && url.startsWith('/api/board/alpha/todos/search?')) {
        return [{ localId: 12, title: 'Feature work' }];
      }
      if (url === '/api/board/alpha/todos/5/links' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ targetLocalId: 12 });
        linksState = {
          outbound: [...linksState.outbound, { localId: 12, title: 'Feature work' }],
          inbound: linksState.inbound,
        };
        return {};
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5);

    const input = document.getElementById('linksSearchInput') as HTMLInputElement | null;
    if (!input) throw new Error('missing linksSearchInput');

    input.value = 'fea';
    input.dispatchEvent(new Event('input', { bubbles: true }));

    vi.advanceTimersByTime(299);
    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    await flushPromises();

    const searchCall = apiFetchMock.mock.calls.find(
      ([url]) => typeof url === 'string' && url.startsWith('/api/board/alpha/todos/search?'),
    );
    if (!searchCall) throw new Error('missing board todo search call');
    const searchUrl = new URL(String(searchCall[0]), 'https://example.test');
    expect(searchUrl.searchParams.get('q')).toBe('fea');
    expect(searchUrl.searchParams.get('limit')).toBe('20');
    expect((searchUrl.searchParams.get('exclude') || '').split(',').sort()).toEqual(['2', '5', '9']);
    expect(document.getElementById('linksAutocompleteSuggestion')).not.toBeNull();

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[data-link-open="12"]')).not.toBeNull();
    expect(document.getElementById('linksAutocompleteSuggestion')).toBeNull();
    expect(input.value).toBe('');
  });

  it('removes outbound links through DELETE and shows a toast if removal fails', async () => {
    let linksState = {
      outbound: [{ localId: 7, title: 'Outbound' }] as LinkedStoryItem[],
      inbound: [] as LinkedStoryItem[],
    };
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/todos/5/links' && !init) {
        return {
          outbound: linksState.outbound,
          inbound: linksState.inbound,
        };
      }
      if (url === '/api/board/alpha/todos/5/links/7' && init?.method === 'DELETE') {
        linksState = { outbound: [], inbound: [] };
        return {};
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5);

    const removeBtn = document.querySelector('[data-link-remove="7"]');
    if (!(removeBtn instanceof HTMLElement)) throw new Error('missing link remove button');
    removeBtn.click();
    await flushPromises();

    expect(recordLocalMutationMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/board/alpha/todos/5/links/7', {
      method: 'DELETE',
    });
    expect(document.querySelector('[data-link-open="7"]')).toBeNull();

    renderLinksShell();
    vi.resetModules();
    permissionState.canEditLinks = true;
    selectorState.editingTodo = { localId: 5, title: 'Current story' };
    selectorState.slug = 'alpha';
    apiFetchMock.mockReset();
    recordLocalMutationMock.mockClear();
    showToastMock.mockClear();

    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/todos/5/links' && !init) {
        return {
          outbound: [{ localId: 7, title: 'Outbound' }],
          inbound: [],
        };
      }
      if (url === '/api/board/alpha/todos/5/links/7' && init?.method === 'DELETE') {
        throw new Error('Failed to remove link');
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });
    const failingMod = await loadTodoLinksModule();
    await failingMod.initializeTodoDialogLinks('alpha', 5);

    const failingRemoveBtn = document.querySelector('[data-link-remove="7"]');
    if (!(failingRemoveBtn instanceof HTMLElement)) throw new Error('missing failing link remove button');
    failingRemoveBtn.click();
    await flushPromises();

    expect(showToastMock).toHaveBeenCalledWith('Failed to remove link');
  });

  it('clears pending search state, chips, and overlays through resetTodoDialogLinks', async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === '/api/board/alpha/todos/5/links' && !init) {
        return {
          outbound: [],
          inbound: [],
        };
      }
      if (typeof url === 'string' && url.startsWith('/api/board/alpha/todos/search?')) {
        return [{ localId: 12, title: 'Feature work' }];
      }
      throw new Error(`unexpected apiFetch call: ${url} ${init?.method ?? 'GET'}`);
    });
    const mod = await loadTodoLinksModule();

    await mod.initializeTodoDialogLinks('alpha', 5);

    const input = document.getElementById('linksSearchInput') as HTMLInputElement | null;
    if (!input) throw new Error('missing linksSearchInput');

    input.value = 'fea';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    mod.resetTodoDialogLinks();

    expect(document.getElementById('linksChips')?.innerHTML).toBe('');
    expect(document.getElementById('linksAutocompleteSuggestion')).toBeNull();

    vi.advanceTimersByTime(300);
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledTimes(1);

  });
});
