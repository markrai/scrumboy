// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { appEl, toastEl } = vi.hoisted(() => ({
  appEl: document.createElement('div'),
  toastEl: document.createElement('div'),
}));

vi.mock('../dom/elements.js', () => ({ app: appEl, toast: toastEl }));
vi.mock('../i18n/index.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../i18n/index.js')>()),
  t: (key: string) => key,
}));

import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from '../platform/runtime.js';
import type { ServerEventStream, ServerTransport } from '../platform/server-transport.js';
import { setAuthStatusAvailable, setBoard, setBoardAccess, setProjectId, setSlug, setUser } from '../state/mutations.js';
import { getBoard, getBoardAccess, getProjectId } from '../state/selectors.js';
import {
  applyPublicBoardRoute,
  isPublicBoardSessionFor,
  resolvePublicBoard,
  stopPublicBoard,
  __getPublicBoardSessionForTest,
} from './public-board.js';
import { bindBoardFilterUi, resetBoardFilterUiState } from './board-filters.js';
import { buildFilterPanelHtml, buildOmniFilterRowHtml } from './board-rendering.js';

class FakeStream implements ServerEventStream {
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  close = vi.fn();
  constructor(readonly path: string) {}
  open(): void {
    this.onopen?.(new Event('open'));
  }
  send(data: string): void {
    this.onmessage?.(new MessageEvent('message', { data }));
  }
  error(): void {
    this.onerror?.(new Event('error'));
  }
}

type Reply = { status: number; body?: unknown } | Promise<{ status: number; body?: unknown }>;

function snapshot(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    access: { kind: 'public', readOnly: true },
    project: { slug: 'ignite', name: 'Ignite <b>Board</b>', dominantColor: '#112233', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: true },
    workflow: [
      { key: 'backlog', name: 'Backlog', color: '#9ca3af', isDone: false, position: 0 },
      { key: 'done', name: 'Done', color: '#ef4444', isDone: true, position: 1 },
    ],
    priorities: [{ key: 'high', name: 'High', color: '#ff0000', position: 0 }],
    tags: [{ name: 'api', color: '#00ff00', activeCount: 1 }],
    columns: {
      backlog: [
        { localId: 7, title: '<img src=x onerror="alert(1)">', body: 'body', columnKey: 'backlog', estimationPoints: 3, priorityKey: 'high', sprintNumber: 2, tags: [{ name: 'api', color: '#00ff00' }] },
      ],
      done: [],
    },
    columnsMeta: {
      backlog: { hasMore: true, nextCursor: 'cursor-1', totalCount: 3 },
      done: { hasMore: false, nextCursor: null, totalCount: 0 },
    },
    ...overrides,
  };
}

let requests: string[];
let streams: FakeStream[];
let handler: (path: string) => Reply;
/** When false, simulates responses that already arrived before an abort took effect. */
let honorAbort = true;

function installTransport(): void {
  requests = [];
  streams = [];
  const transport: ServerTransport = {
    request: vi.fn(async (path: string, options?: { signal?: AbortSignal | null }) => {
      requests.push(path);
      const reply = await handler(path);
      if (honorAbort && options?.signal?.aborted) throw Object.assign(new Error('aborted'), { name: 'AbortError' });
      return {
        ok: reply.status >= 200 && reply.status < 300,
        status: reply.status,
        json: async () => reply.body,
        blob: async () => new Blob(),
      };
    }),
    openEventStream: vi.fn((path: string) => {
      const stream = new FakeStream(path);
      streams.push(stream);
      return stream;
    }),
    acquireResource: vi.fn(),
    logout: vi.fn(),
  };
  installAppRuntime({
    kind: 'browser',
    capability: () => null,
    assetOrigin: () => 'https://example.test',
    serverOrigin: () => 'https://example.test',
    publicLinkOrigin: () => 'https://example.test',
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: vi.fn(),
    transport: () => transport,
  } as unknown as AppRuntime);
}

function defaultHandler(path: string): Reply {
  const url = new URL(path, 'https://example.test');
  if (url.pathname === '/api/public/board/ignite') return { status: 200, body: snapshot() };
  if (url.pathname === '/api/public/board/ignite/sprints') return { status: 200, body: { sprints: [{ number: 2, name: 'Sprint Two', state: 'ACTIVE' }] } };
  if (url.pathname === '/api/public/board/ignite/todos/7') return { status: 200, body: { todo: { localId: 7, title: 'Seven <script>x</script>', body: '<img src=x onerror="alert(1)"> **bold**', columnKey: 'backlog', priorityKey: 'high', sprintNumber: 2, estimationPoints: 3, tags: [{ name: 'api', color: '#00ff00' }] } } };
  if (url.pathname === '/api/public/board/ignite/todos/7/links') return { status: 200, body: { links: [{ direction: 'outbound', localId: 3, title: 'Three' }] } };
  if (url.pathname === '/api/public/board/ignite/todos/3') return { status: 200, body: { todo: { localId: 3, title: 'Three', body: '', columnKey: 'backlog', tags: [] } } };
  if (url.pathname === '/api/public/board/ignite/todos/3/links') return { status: 200, body: { links: [] } };
  if (url.pathname === '/api/public/board/ignite/lanes/backlog') {
    return { status: 200, body: { items: [
      { localId: 7, title: 'dup', body: '', columnKey: 'backlog', tags: [] },
      { localId: 8, title: 'Eight', body: '', columnKey: 'backlog', tags: [] },
    ], hasMore: false, nextCursor: null, totalCount: 3 } };
  }
  return { status: 404, body: { error: { code: 'NOT_FOUND' } } };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) await Promise.resolve();
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function panelOption(attr: string, value: string): HTMLButtonElement | undefined {
  return Array.from(appEl.querySelectorAll<HTMLButtonElement>(`#publicFilterPanel [${attr}]`))
    .find((el) => el.getAttribute(attr) === value);
}

/** Opens the public filter popover and clicks the option with the given value. */
function pickPanelOption(attr: string, value: string): void {
  const toggle = appEl.querySelector('#publicFilterToggle') as HTMLButtonElement;
  const panel = appEl.querySelector('#publicFilterPanel') as HTMLElement;
  if (panel.hidden) toggle.click();
  const option = panelOption(attr, value);
  if (!option) throw new Error(`no option ${attr}=${value}`);
  option.click();
}

function cards(): HTMLElement[] {
  return Array.from(appEl.querySelectorAll<HTMLElement>('[data-public-local-id]'));
}

describe('public board view', () => {
  beforeEach(() => {
    resetAppRuntimeForTests();
    handler = defaultHandler;
    installTransport();
    appEl.innerHTML = '<p id="previous">previous view</p>';
    document.body.replaceChildren(appEl, toastEl);
    history.replaceState({}, '', '/ignite');
    setUser(null);
    setSlug('ignite');
    setBoard(null);
    setProjectId(null);
    setBoardAccess(null);
  });

  afterEach(() => {
    stopPublicBoard();
    vi.useRealTimers();
    resetAppRuntimeForTests();
  });

  it('renders an anonymous visitor read-only board from public data only', async () => {
    await expect(resolvePublicBoard({ slug: 'ignite', openTodoSegment: null })).resolves.toBe('rendered');
    await settle();

    expect(getBoardAccess()).toEqual({ kind: 'public', readOnly: true });
    expect(getBoard()).toBeNull();
    expect(getProjectId()).toBeNull();
    expect(isPublicBoardSessionFor('ignite')).toBe(true);
    expect(appEl.querySelector('.public-board-badge')).not.toBeNull();
    expect(appEl.querySelector('[data-public-board]')).not.toBeNull();
    // User content is escaped, never parsed into elements.
    expect(appEl.querySelector('img[src="x"]')).toBeNull();
    expect(appEl.querySelector('.topbar .public-board__project-name')?.textContent).toContain('Ignite <b>Board</b>');
    expect(cards().map((c) => c.getAttribute('data-public-local-id'))).toEqual(['7']);
    // Every request and the stream stay inside the public namespace.
    expect(requests.every((path) => path.startsWith('/api/public/board/ignite'))).toBe(true);
    expect(streams.map((s) => s.path)).toEqual(['/api/public/board/ignite/events']);
  });

  it('gives a signed-in nonmember the identical public projection', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    const anonymousBoard = appEl.querySelector('[data-public-board]')?.innerHTML;
    stopPublicBoard();

    setUser({ id: 42, name: 'Nonmember', email: 'n@example.test' } as any);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    expect(appEl.querySelector('[data-public-board]')?.innerHTML).toBe(anonymousBoard);
    expect(getBoardAccess()).toEqual({ kind: 'public', readOnly: true });
    expect(streams.map((s) => s.path)).toEqual(['/api/public/board/ignite/events', '/api/public/board/ignite/events']);
    expect(requests.some((path) => path.startsWith('/api/me') || path.startsWith('/api/board/') || path.startsWith('/api/projects'))).toBe(false);
  });

  it.each([
    ['not found', { status: 404, body: { error: { code: 'NOT_FOUND' } } }],
    ['server error', { status: 500, body: {} }],
    ['malformed', { status: 200, body: { access: { kind: 'member' } } }],
  ])('returns not-public without rendering on %s', async (_label, reply) => {
    handler = () => reply;
    await expect(resolvePublicBoard({ slug: 'ignite', openTodoSegment: null })).resolves.toBe('not-public');
    expect(appEl.querySelector('#previous')).not.toBeNull();
    expect(getBoardAccess()).toBeNull();
    expect(streams).toHaveLength(0);
  });

  it('drops a late response after navigation to another board', async () => {
    let release: (value: { status: number; body?: unknown }) => void = () => {};
    handler = (path) => path.startsWith('/api/public/board/ignite?')
      ? new Promise((resolve) => { release = resolve; })
      : defaultHandler(path);
    const pending = resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    setSlug('another-board');
    release({ status: 200, body: snapshot() });
    await expect(pending).resolves.toBe('stale');
    expect(appEl.querySelector('#previous')).not.toBeNull();
    expect(getBoardAccess()).toBeNull();
    expect(streams).toHaveLength(0);
  });

  it('exposes no mutation surface and registers no write interactions', async () => {
    const sortable = vi.fn();
    (window as any).Sortable = sortable;
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    for (const selector of [
      '[data-todo-id]', '[data-todo-local-id]', '.card__drag-handle', '[draggable="true"]', '#newTodoBtn', '#wallBtn',
      '#archiveBtn', '#settingsBtn', '#manageMembersBtn', '#projectImageBtn', '#renameProjectBtn', '#voiceCommandBtn',
      '#mobileTabDropZones', 'form', 'textarea', '[contenteditable]',
    ]) {
      expect(appEl.querySelector(selector), selector).toBeNull();
    }
    const card = cards()[0];
    const contextMenu = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
    card.dispatchEvent(contextMenu);
    expect(contextMenu.defaultPrevented).toBe(false);
    expect(sortable).not.toHaveBeenCalled();
    const before = requests.length;
    card.dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: true }));
    await settle();
    // Ctrl-click opens the read view; it never selects for bulk editing.
    expect(appEl.querySelector('.card--selected')).toBeNull();
    expect(requests.slice(before).every((path) => path.startsWith('/api/public/board/ignite/todos/7'))).toBe(true);
    delete (window as any).Sortable;
  });

  it('opens story detail from the public endpoint with inert text, links, and URL handling', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    cards()[0].click();
    await settle();

    expect(window.location.pathname).toBe('/ignite/t/7');
    expect(requests).toContain('/api/public/board/ignite/todos/7');
    expect(requests).toContain('/api/public/board/ignite/todos/7/links');
    const dialog = document.getElementById('publicTodoDialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);
    expect(dialog.querySelector('#publicTodoDialogTitle')?.textContent).toBe('#7 Seven <script>x</script>');
    expect(dialog.querySelector('script, img')).toBeNull();
    expect(dialog.querySelector('.public-todo__notes')?.textContent).toContain('<img src=x onerror="alert(1)">');
    expect(dialog.querySelector('input, textarea, select, form, [data-link-remove]')).toBeNull();
    // Sprint metadata still comes from the public sprint vocabulary.
    expect(dialog.textContent).toContain('Sprint Two (2)');

    (dialog.querySelector('[data-public-link-open="3"]') as HTMLButtonElement).click();
    await settle();
    expect(window.location.pathname).toBe('/ignite/t/3');
    expect(requests).toContain('/api/public/board/ignite/todos/3');
    expect(dialog.querySelector('#publicTodoDialogTitle')?.textContent).toBe('#3 Three');

    (dialog.querySelector('[data-public-todo-close]') as HTMLButtonElement).click();
    await settle();
    expect(dialog.open).toBe(false);
    expect(window.location.pathname).toBe('/ignite');
    expect(requests.every((path) => path.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('fails a missing or archived story deep link safely', async () => {
    history.replaceState({}, '', '/ignite/t/99');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: '99' });
    await settle();
    const dialog = document.getElementById('publicTodoDialog') as HTMLDialogElement;
    expect(dialog.open).toBe(false);
    expect(dialog.querySelector('[data-public-todo-body]')?.textContent).not.toContain('99 ');
    expect(window.location.pathname).toBe('/ignite');
    expect(toastEl.textContent).toContain('publicBoard.story.unavailable');
  });

  it('applies tag, sprint deep link, priority, and search filters through the public query', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    history.replaceState({}, '', '/ignite?sprintNumber=2');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);

    (appEl.querySelector('#publicOmniCandidateViewport [data-public-omni-tag="api"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    let last = new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
    expect(last.searchParams.getAll('tag')).toEqual(['api']);
    expect(last.searchParams.get('sprintNumber')).toBe('2');
    expect(new URL(window.location.href).searchParams.getAll('tag')).toEqual(['api']);

    pickPanelOption('data-public-priority-option', 'high');
    await vi.advanceTimersByTimeAsync(10);

    const search = appEl.querySelector('#publicSearchInput') as HTMLInputElement;
    search.value = 'needle';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    const beforeDebounce = requests.length;
    await vi.advanceTimersByTimeAsync(100);
    expect(requests.length).toBe(beforeDebounce);
    await vi.advanceTimersByTimeAsync(400);

    last = new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
    expect(last.searchParams.getAll('tag')).toEqual(['api']);
    expect(last.searchParams.get('sprintNumber')).toBe('2');
    expect(last.searchParams.get('priority')).toBe('high');
    expect(last.searchParams.get('search')).toBe('needle');
    expect(last.searchParams.has('sort')).toBe(false);
    expect(last.searchParams.has('assignee')).toBe(false);
    // The focused search input survives filter re-rendering.
    expect(appEl.querySelector('#publicSearchInput')).toBe(search);
  });

  it('paginates with the opaque cursor, de-duplicates, and drops stale pages', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    (appEl.querySelector('[data-public-load-more="backlog"]') as HTMLButtonElement).click();
    await settle();
    const lane = new URL(requests.find((p) => p.includes('/lanes/backlog'))!, 'https://example.test');
    expect(lane.searchParams.get('afterCursor')).toBe('cursor-1');
    expect(cards().map((c) => c.getAttribute('data-public-local-id'))).toEqual(['7', '8']);
    expect(appEl.querySelector('[data-public-load-more="backlog"]')).toBeNull();

    // A filter change while a page is in flight invalidates that page.
    stopPublicBoard();
    let releaseLane: (value: { status: number; body?: unknown }) => void = () => {};
    handler = (path) => path.includes('/lanes/backlog')
      ? new Promise((resolve) => { releaseLane = resolve; })
      : defaultHandler(path);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    (appEl.querySelector('[data-public-load-more="backlog"]') as HTMLButtonElement).click();
    await settle();
    (appEl.querySelector('#publicOmniCandidateViewport [data-public-omni-tag="api"]') as HTMLButtonElement).click();
    await settle();
    releaseLane({ status: 200, body: { items: [{ localId: 55, title: 'Stale', body: '', columnKey: 'backlog', tags: [] }], hasMore: false, nextCursor: null, totalCount: 9 } });
    await settle();
    expect(cards().map((c) => c.getAttribute('data-public-local-id'))).not.toContain('55');
  });

  it('coalesces refresh_needed into one public snapshot reload that keeps filters', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    history.replaceState({}, '', '/ignite?tag=api');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    const before = requests.filter((p) => p.startsWith('/api/public/board/ignite?')).length;
    streams[0].open();
    streams[0].send('{"type":"refresh_needed"}');
    streams[0].send('{"type":"refresh_needed"}');
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    const snapshots = requests.filter((p) => p.startsWith('/api/public/board/ignite?'));
    expect(snapshots.length - before).toBe(1);
    expect(new URL(snapshots.at(-1)!, 'https://example.test').searchParams.getAll('tag')).toEqual(['api']);
    expect(requests.every((path) => path.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('access_revoked clears content, closes the story, stops the stream, and never reconnects', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: '7' });
    await vi.advanceTimersByTimeAsync(10);
    const dialog = document.getElementById('publicTodoDialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);
    streams[0].open();

    streams[0].send('{"type":"access_revoked"}');
    expect(dialog.open).toBe(false);
    expect(dialog.textContent).not.toContain('Seven');
    expect(cards()).toHaveLength(0);
    expect(appEl.querySelector('[data-public-board-state="unavailable"]')).not.toBeNull();
    expect(appEl.textContent).not.toContain('Ignite');
    expect(streams[0].close).toHaveBeenCalled();
    expect(getBoardAccess()).toBeNull();
    expect(isPublicBoardSessionFor('ignite')).toBe(false);
    const requestCount = requests.length;
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    expect(streams).toHaveLength(1);
    expect(requests.length).toBe(requestCount);
  });

  it('treats a stream failure as unconfirmed until a public read returns 404', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);

    handler = (path) => path.startsWith('/api/public/board/ignite?') ? { status: 503, body: {} } : defaultHandler(path);
    streams[0].error();
    await vi.advanceTimersByTimeAsync(1000);
    expect(cards()).toHaveLength(1);
    expect(isPublicBoardSessionFor('ignite')).toBe(true);

    handler = () => ({ status: 404, body: {} });
    await vi.advanceTimersByTimeAsync(10_000);
    streams.at(-1)!.error();
    await vi.advanceTimersByTimeAsync(1000);
    expect(cards()).toHaveLength(0);
    expect(appEl.querySelector('[data-public-board-state="unavailable"]')).not.toBeNull();
    const streamCount = streams.length;
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    expect(streams.length).toBe(streamCount);
  });

  it('in-board navigation closes or opens stories and reloads changed URL filters', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: '7' });
    await settle();
    const dialog = document.getElementById('publicTodoDialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);

    history.pushState({}, '', '/ignite?priority=high');
    await applyPublicBoardRoute({ slug: 'ignite', openTodoSegment: null });
    await settle();
    expect(dialog.open).toBe(false);
    const last = new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
    expect(last.searchParams.get('priority')).toBe('high');
  });

  it('stopPublicBoard tears down the stream and access state', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    stopPublicBoard();
    expect(streams[0].close).toHaveBeenCalled();
    expect(getBoardAccess()).toBeNull();
    expect(__getPublicBoardSessionForTest()).toBeNull();
  });
});

describe('public board sprint vocabulary and filter failures', () => {
  let sprintsReply: (() => Reply) | null;
  let snapshotOverride: ((url: URL) => Reply | null) | null;

  function sprintRequests(): number {
    return requests.filter((p) => p === '/api/public/board/ignite/sprints').length;
  }

  function lastSnapshotUrl(): URL {
    return new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
  }

  /** The public sprint vocabulary (used by story details and the deep-link pill), never a popover section. */
  function sprintOptions(): string[] {
    return (__getPublicBoardSessionForTest()?.sprints ?? []).map((sprint) => `${sprint.number}:${sprint.name}`);
  }

  async function refreshViaStream(): Promise<void> {
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
  }

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    resetAppRuntimeForTests();
    sprintsReply = null;
    snapshotOverride = null;
    handler = (path) => {
      const url = new URL(path, 'https://example.test');
      if (url.pathname === '/api/public/board/ignite/sprints' && sprintsReply) return sprintsReply();
      if (url.pathname === '/api/public/board/ignite' && snapshotOverride) {
        const reply = snapshotOverride(url);
        if (reply) return reply;
      }
      return defaultHandler(path);
    };
    installTransport();
    appEl.innerHTML = '';
    toastEl.textContent = '';
    document.body.replaceChildren(appEl, toastEl);
    history.replaceState({}, '', '/ignite');
    setUser(null);
    setSlug('ignite');
    setBoard(null);
    setProjectId(null);
    setBoardAccess(null);
  });

  afterEach(() => {
    honorAbort = true;
    stopPublicBoard();
    vi.useRealTimers();
    resetAppRuntimeForTests();
  });

  it('a superseded sprint response that already arrived (abort too late) is still ignored', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    honorAbort = false;
    let releaseOld: (value: { status: number; body?: unknown }) => void = () => {};
    sprintsReply = () => new Promise((resolve) => { releaseOld = resolve; });
    await refreshViaStream();
    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 4, name: 'Newest', state: 'ACTIVE' }] } });
    await refreshViaStream();
    releaseOld({ status: 200, body: { sprints: [{ number: 1, name: 'Old', state: 'CLOSED' }] } });
    await vi.advanceTimersByTimeAsync(10);
    expect(sprintOptions()).toContain('4:Newest');
    expect(sprintOptions()).not.toContain('1:Old');
  });

  it('refresh reloads the sprint vocabulary for created and renamed sprints, once per refresh', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(sprintOptions()).toContain('2:Sprint Two');
    expect(sprintRequests()).toBe(1);

    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 2, name: 'Renamed Two', state: 'ACTIVE' }, { number: 3, name: 'Sprint Three', state: 'PLANNED' }] } });
    streams[0].send('{"type":"refresh_needed"}');
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);

    expect(sprintRequests()).toBe(2);
    expect(sprintOptions()).toEqual(expect.arrayContaining(['2:Renamed Two', '3:Sprint Three']));
    expect(sprintOptions()).not.toContain('2:Sprint Two');
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('filter reloads do not refetch the sprint vocabulary', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    (appEl.querySelector('#publicOmniCandidateViewport [data-public-omni-tag="api"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    expect(sprintRequests()).toBe(1);
  });

  it('a removed active sprint clears only the sprint filter, with feedback, and reloads', async () => {
    history.replaceState({}, '', '/ignite?sprintNumber=2&tag=api');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(lastSnapshotUrl().searchParams.get('sprintNumber')).toBe('2');

    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 3, name: 'Sprint Three', state: 'PLANNED' }] } });
    await refreshViaStream();

    expect(toastEl.textContent).toContain('publicBoard.sprintFilterCleared');
    const url = new URL(window.location.href);
    expect(url.searchParams.has('sprintNumber')).toBe(false);
    expect(url.searchParams.getAll('tag')).toEqual(['api']);
    expect(lastSnapshotUrl().searchParams.has('sprintNumber')).toBe(false);
    expect(lastSnapshotUrl().searchParams.getAll('tag')).toEqual(['api']);
    expect(appEl.querySelector('[data-public-clear-sprint]')).toBeNull();
    expect(appEl.querySelector('[data-public-sprint-option], [data-public-sprint-section]')).toBeNull();
  });

  it('clears an initial URL sprint filter that does not exist', async () => {
    history.replaceState({}, '', '/ignite?sprintNumber=99');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(new URL(window.location.href).searchParams.has('sprintNumber')).toBe(false);
    expect(lastSnapshotUrl().searchParams.has('sprintNumber')).toBe(false);
  });

  it('handles sprints being disabled and re-enabled', async () => {
    history.replaceState({}, '', '/ignite?sprintNumber=2');
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('#publicOmniPinnedTags [data-public-clear-sprint]')).not.toBeNull();
    expect(sprintOptions()).toEqual(['2:Sprint Two']);

    snapshotOverride = () => ({ status: 200, body: snapshot({ project: { slug: 'ignite', name: 'Ignite', dominantColor: '', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: false } }) });
    await refreshViaStream();
    expect(appEl.querySelector('[data-public-clear-sprint]')).toBeNull();
    expect(sprintOptions()).toEqual([]);
    expect(sprintRequests()).toBe(1);
    expect(new URL(window.location.href).searchParams.has('sprintNumber')).toBe(false);
    expect(lastSnapshotUrl().searchParams.has('sprintNumber')).toBe(false);

    snapshotOverride = null;
    await refreshViaStream();
    expect(sprintRequests()).toBe(2);
    expect(sprintOptions()).toEqual(['2:Sprint Two']);
    expect(appEl.querySelector('[data-public-sprint-section], [data-public-sprint-option]')).toBeNull();
  });

  it('ignores a late sprint response from a superseded refresh', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    let releaseOld: (value: { status: number; body?: unknown }) => void = () => {};
    sprintsReply = () => new Promise((resolve) => { releaseOld = resolve; });
    await refreshViaStream();
    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 4, name: 'Newest', state: 'ACTIVE' }] } });
    await refreshViaStream();
    expect(sprintOptions()).toContain('4:Newest');
    releaseOld({ status: 200, body: { sprints: [{ number: 1, name: 'Old', state: 'CLOSED' }] } });
    await vi.advanceTimersByTimeAsync(10);
    expect(sprintOptions()).toContain('4:Newest');
    expect(sprintOptions()).not.toContain('1:Old');
  });

  it('ignores a late sprint response after revocation', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    let release: (value: { status: number; body?: unknown }) => void = () => {};
    sprintsReply = () => new Promise((resolve) => { release = resolve; });
    await refreshViaStream();
    streams[0].send('{"type":"access_revoked"}');
    release({ status: 200, body: { sprints: [{ number: 9, name: 'Late', state: 'ACTIVE' }] } });
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('[data-public-board-state="unavailable"]')).not.toBeNull();
    expect(appEl.querySelector('[data-public-clear-sprint]')).toBeNull();
    expect(appEl.textContent).not.toContain('Late');
  });

  it('a failed filter request replaces stale cards with an error and retry', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(cards()).toHaveLength(1);

    snapshotOverride = (url) => (url.searchParams.getAll('tag').includes('api') ? { status: 503, body: {} } : null);
    (appEl.querySelector('#publicOmniCandidateViewport [data-public-omni-tag="api"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    expect(cards()).toHaveLength(0);
    expect(appEl.querySelector('[data-public-filter-error]')?.getAttribute('role')).toBe('alert');
    expect(appEl.querySelector('.public-board__no-results')).toBeNull();
    // The selected filter stays visible with the error, never with old cards.
    expect(appEl.querySelector('#publicOmniPinnedTags [data-public-omni-clear-tag="api"]')).not.toBeNull();

    snapshotOverride = null;
    (appEl.querySelector('[data-public-retry]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('[data-public-filter-error]')).toBeNull();
    expect(cards()).toHaveLength(1);
    expect(lastSnapshotUrl().searchParams.getAll('tag')).toEqual(['api']);
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('a failed realtime refresh keeps current cards, which still match the current filters', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    snapshotOverride = () => ({ status: 503, body: {} });
    await refreshViaStream();
    expect(cards()).toHaveLength(1);
    expect(appEl.querySelector('[data-public-filter-error]')).toBeNull();
  });
});

describe('public board sign-in affordance', () => {
  beforeEach(() => {
    resetAppRuntimeForTests();
    handler = defaultHandler;
    installTransport();
    appEl.innerHTML = '';
    document.body.replaceChildren(appEl, toastEl);
    setBoard(null);
    setProjectId(null);
    setBoardAccess(null);
    setSlug('ignite');
  });

  afterEach(() => {
    stopPublicBoard();
    setAuthStatusAvailable(false);
    setUser(null);
    vi.restoreAllMocks();
    resetAppRuntimeForTests();
  });

  it('offers Sign In to a logged-out Full Mode visitor and returns to the exact deep link and filters', async () => {
    setAuthStatusAvailable(true);
    setUser(null);
    history.replaceState({}, '', '/ignite/t/7?tag=api&priority=high');
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    const button = appEl.querySelector<HTMLButtonElement>('#publicSignInBtn');
    expect(button?.type).toBe('button');
    button!.click();
    expect(assign).toHaveBeenCalledWith('/auth/login?next=' + encodeURIComponent('/ignite/t/7?tag=api&priority=high'));
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('offers Sign In inside an open story so the deep link survives sign-in', async () => {
    setAuthStatusAvailable(true);
    setUser(null);
    history.replaceState({}, '', '/ignite/t/7?tag=api');
    const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {});
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: '7' });
    await settle();
    const dialog = document.getElementById('publicTodoDialog') as HTMLDialogElement;
    expect(dialog.open).toBe(true);
    const signIn = dialog.querySelector<HTMLButtonElement>('[data-public-todo-sign-in]')!;
    expect(signIn.hidden).toBe(false);
    signIn.click();
    expect(assign).toHaveBeenCalledWith('/auth/login?next=' + encodeURIComponent('/ignite/t/7?tag=api'));
  });

  it('does not offer Sign In to a signed-in nonmember or in Anonymous Mode', async () => {
    setAuthStatusAvailable(true);
    setUser({ id: 9, name: 'Nonmember', email: 'n@example.test' } as any);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    expect(appEl.querySelector('#publicSignInBtn')).toBeNull();
    stopPublicBoard();
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: '7' });
    await settle();
    expect((document.querySelector('[data-public-todo-sign-in]') as HTMLButtonElement).hidden).toBe(true);
    stopPublicBoard();

    setAuthStatusAvailable(false);
    setUser(null);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await settle();
    expect(appEl.querySelector('#publicSignInBtn')).toBeNull();
  });
});

describe('public board filter panel', () => {
  let sprintsReply: (() => Reply) | null;
  let snapshotOverride: ((url: URL) => Reply | null) | null;

  function lastSnapshotUrl(): URL {
    return new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
  }
  function snapshotRequests(): number {
    return requests.filter((p) => p.startsWith('/api/public/board/ignite?')).length;
  }
  function toggle(): HTMLButtonElement {
    return appEl.querySelector('#publicFilterToggle') as HTMLButtonElement;
  }
  function panel(): HTMLElement {
    return appEl.querySelector('#publicFilterPanel') as HTMLElement;
  }
  function searchInput(): HTMLInputElement {
    return appEl.querySelector('#publicSearchInput') as HTMLInputElement;
  }
  function activeValue(attr: string): string | null {
    return appEl.querySelector(`#publicFilterPanel [${attr}].is-active`)?.getAttribute(attr) ?? null;
  }
  async function render(url = '/ignite'): Promise<void> {
    history.replaceState({}, '', url);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
  }

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    resetAppRuntimeForTests();
    sprintsReply = null;
    snapshotOverride = null;
    handler = (path) => {
      const url = new URL(path, 'https://example.test');
      if (url.pathname === '/api/public/board/ignite/sprints' && sprintsReply) return sprintsReply();
      if (url.pathname === '/api/public/board/ignite' && snapshotOverride) {
        const reply = snapshotOverride(url);
        if (reply) return reply;
      }
      return defaultHandler(path);
    };
    installTransport();
    appEl.innerHTML = '';
    toastEl.textContent = '';
    document.body.replaceChildren(appEl, toastEl);
    setUser(null);
    setSlug('ignite');
    setBoard(null);
    setProjectId(null);
    setBoardAccess(null);
  });

  afterEach(() => {
    stopPublicBoard();
    vi.useRealTimers();
    resetAppRuntimeForTests();
  });

  it('lives in the topbar search control, replacing the old dropdowns', async () => {
    await render();
    const wrapper = appEl.querySelector('.topbar .search-input-wrapper')!;
    expect(wrapper.contains(searchInput())).toBe(true);
    expect(wrapper.contains(toggle())).toBe(true);
    expect(wrapper.contains(panel())).toBe(true);
    expect(appEl.querySelector('select, #publicSprintFilter, #publicPriorityFilter')).toBeNull();
    expect(toggle().hidden).toBe(false);
    expect(panel().hidden).toBe(true);
  });

  it('selects, marks active, updates the URL, and clears priority', async () => {
    await render('/ignite?tag=api');
    pickPanelOption('data-public-priority-option', 'high');
    // Selection state updates immediately; the popover closes and returns focus to the toggle.
    expect(activeValue('data-public-priority-option')).toBe('high');
    expect(panelOption('data-public-priority-option', 'high')?.getAttribute('aria-pressed')).toBe('true');
    expect(panel().hidden).toBe(true);
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(toggle());
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(true);
    await vi.advanceTimersByTimeAsync(10);
    let url = new URL(window.location.href);
    expect(url.searchParams.get('priority')).toBe('high');
    expect(url.searchParams.getAll('tag')).toEqual(['api']);
    expect(lastSnapshotUrl().searchParams.get('priority')).toBe('high');

    pickPanelOption('data-public-priority-option', '');
    await vi.advanceTimersByTimeAsync(10);
    url = new URL(window.location.href);
    expect(url.searchParams.has('priority')).toBe(false);
    expect(url.searchParams.getAll('tag')).toEqual(['api']);
    expect(lastSnapshotUrl().searchParams.has('priority')).toBe(false);
    expect(activeValue('data-public-priority-option')).toBe('');
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(false);

    // Re-selecting the current value issues no request.
    const before = snapshotRequests();
    pickPanelOption('data-public-priority-option', '');
    await vi.advanceTimersByTimeAsync(10);
    expect(snapshotRequests()).toBe(before);
  });

  it('never offers a Sprint section, for zero, one, or many sprints', async () => {
    const many = Array.from({ length: 25 }, (_, i) => ({ number: i + 1, name: `Sprint ${i + 1}`, state: 'PLANNED' }));
    for (const sprints of [[], many.slice(0, 1), many]) {
      sprintsReply = () => ({ status: 200, body: { sprints } });
      await render();
      toggle().click();
      expect(panel().querySelectorAll('.search-filter-panel__section')).toHaveLength(1);
      expect(panel().querySelector('[data-public-priority-section]')).not.toBeNull();
      expect(panel().querySelector('[data-public-sprint-section], [data-public-sprint-option], [data-sprint-option]')).toBeNull();
      expect(panel().textContent).not.toContain('board.filters.sprint');
      stopPublicBoard();
    }
  });

  it('shows a sprint-number deep link as a removable pill and clears only that filter', async () => {
    await render('/ignite?sprintNumber=2&tag=api&priority=high&search=needle');
    expect(lastSnapshotUrl().searchParams.get('sprintNumber')).toBe('2');
    const pill = appEl.querySelector('#publicOmniPinnedTags .omni-tag-pill--applied')!;
    expect(pill.textContent).toContain('board.filters.sprint: Sprint Two');
    expect(appEl.querySelector('#publicFilterPanel [data-public-clear-sprint]')).toBeNull();
    // The popover glows only for its own (priority) filter.
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(true);

    const before = snapshotRequests();
    (pill.querySelector('[data-public-clear-sprint]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    expect(snapshotRequests()).toBe(before + 1);
    const url = new URL(window.location.href);
    expect(url.searchParams.has('sprintNumber')).toBe(false);
    expect(url.searchParams.getAll('tag')).toEqual(['api']);
    expect(url.searchParams.get('priority')).toBe('high');
    expect(url.searchParams.get('search')).toBe('needle');
    expect(lastSnapshotUrl().searchParams.has('sprintNumber')).toBe(false);
    expect(lastSnapshotUrl().searchParams.getAll('tag')).toEqual(['api']);
    expect(appEl.querySelector('[data-public-clear-sprint]')).toBeNull();
    expect(appEl.querySelector('#publicOmniPinnedTags [data-public-omni-clear-tag="api"]')).not.toBeNull();
    expect(document.activeElement).toBe(searchInput());
  });

  it('labels a sprint deep link by number until the vocabulary arrives', async () => {
    let release: (value: { status: number; body?: unknown }) => void = () => {};
    sprintsReply = () => new Promise((resolve) => { release = resolve; });
    await render('/ignite?sprintNumber=2');
    expect(appEl.querySelector('#publicOmniPinnedTags .omni-tag-pill--applied')?.textContent).toContain('board.filters.sprint: #2');
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(false);
    release({ status: 200, body: { sprints: [{ number: 2, name: 'Sprint Two', state: 'ACTIVE' }] } });
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('#publicOmniPinnedTags .omni-tag-pill--applied')?.textContent).toContain('board.filters.sprint: Sprint Two');
  });

  it('opens and closes with the toggle, Escape, and outside clicks', async () => {
    await render();
    toggle().click();
    expect(panel().hidden).toBe(false);
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    toggle().click();
    expect(panel().hidden).toBe(true);

    toggle().click();
    // Clicking non-option content inside the panel keeps it open.
    (panel().querySelector('.search-filter-panel__label') as HTMLElement).click();
    expect(panel().hidden).toBe(false);
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(panel().hidden).toBe(true);
    expect(toggle().getAttribute('aria-expanded')).toBe('false');

    toggle().click();
    panelOption('data-public-priority-option', 'high')!.focus();
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    document.activeElement!.dispatchEvent(escape);
    expect(panel().hidden).toBe(true);
    expect(escape.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(toggle());

    // Escape with the panel closed is left alone (e.g. for the story dialog).
    const idle = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    document.dispatchEvent(idle);
    expect(idle.defaultPrevented).toBe(false);
  });

  it('is keyboard reachable: real buttons in DOM order after the toggle', async () => {
    await render();
    expect(toggle().type).toBe('button');
    expect(toggle().getAttribute('aria-haspopup')).toBe('true');
    expect(toggle().getAttribute('aria-controls')).toBe('publicFilterPanel');
    expect(toggle().nextElementSibling).toBe(panel());
    const options = Array.from(panel().querySelectorAll<HTMLButtonElement>('button'));
    expect(options.length).toBeGreaterThan(0);
    for (const option of options) {
      expect(option.type).toBe('button');
      expect(option.hasAttribute('aria-pressed')).toBe(true);
    }
  });

  it('remains available and usable at mobile width', async () => {
    const original = window.innerWidth;
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 400 });
    try {
      await render();
      expect(searchInput().getAttribute('placeholder')).toBe('board.search.placeholder.mobile');
      expect(toggle().hidden).toBe(false);
      toggle().click();
      expect(panel().hidden).toBe(false);
      // Positioned inside the narrow viewport.
      expect(parseFloat(panel().style.left)).toBeGreaterThanOrEqual(8);
      window.dispatchEvent(new Event('resize'));
      expect(panel().hidden).toBe(false);
      panelOption('data-public-priority-option', 'high')!.click();
      await vi.advanceTimersByTimeAsync(10);
      expect(lastSnapshotUrl().searchParams.get('priority')).toBe('high');
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: original });
    }
  });

  it('loads the sprint vocabulary asynchronously without changing the popover or rebuilding the search input', async () => {
    let release: (value: { status: number; body?: unknown }) => void = () => {};
    sprintsReply = () => new Promise((resolve) => { release = resolve; });
    await render();
    const input = searchInput();
    const toggleEl = toggle();
    const panelHtml = panel().innerHTML;
    input.focus();
    expect(appEl.querySelector('[data-public-priority-section]')).not.toBeNull();

    release({ status: 200, body: { sprints: [{ number: 2, name: 'Sprint Two', state: 'ACTIVE' }, { number: 5, name: 'Later', state: 'PLANNED' }] } });
    await vi.advanceTimersByTimeAsync(10);
    expect(__getPublicBoardSessionForTest()?.sprints.map((s) => s.number)).toEqual([2, 5]);
    expect(panel().innerHTML).toBe(panelHtml);
    expect(searchInput()).toBe(input);
    expect(toggle()).toBe(toggleEl);
    expect(document.activeElement).toBe(input);

    // An open popover stays open while the vocabulary changes underneath it.
    toggle().click();
    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 2, name: 'Sprint Two', state: 'ACTIVE' }] } });
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(__getPublicBoardSessionForTest()?.sprints.map((s) => s.number)).toEqual([2]);
    expect(panel().innerHTML).toBe(panelHtml);
    expect(panel().hidden).toBe(false);
    expect(searchInput()).toBe(input);
  });

  it('keeps focus on a still-present option when the vocabulary refreshes', async () => {
    await render();
    toggle().click();
    panelOption('data-public-priority-option', 'high')!.focus();
    sprintsReply = () => ({ status: 200, body: { sprints: [{ number: 2, name: 'Renamed', state: 'ACTIVE' }] } });
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(__getPublicBoardSessionForTest()?.sprints.map((s) => s.name)).toEqual(['Renamed']);
    expect(document.activeElement).toBe(panelOption('data-public-priority-option', 'high'));
  });

  it('hides the toggle when priorities disappear, moving focus to search', async () => {
    await render();
    toggle().click();
    panelOption('data-public-priority-option', '')!.focus();
    snapshotOverride = () => ({ status: 200, body: snapshot({ priorities: [] }) });
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(toggle().hidden).toBe(true);
    expect(panel().hidden).toBe(true);
    expect(panel().querySelector('button')).toBeNull();
    expect(document.activeElement).toBe(searchInput());
  });

  it('a failed filter keeps the selection visible with the error and keeps search focus', async () => {
    await render();
    const input = searchInput();
    snapshotOverride = (url) => (url.searchParams.get('priority') === 'high' ? { status: 503, body: {} } : null);
    pickPanelOption('data-public-priority-option', 'high');
    input.focus();
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('[data-public-filter-error]')).not.toBeNull();
    expect(cards()).toHaveLength(0);
    expect(activeValue('data-public-priority-option')).toBe('high');
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(true);
    expect(searchInput()).toBe(input);
    expect(document.activeElement).toBe(input);

    // Clearing the failed filter recovers.
    snapshotOverride = null;
    pickPanelOption('data-public-priority-option', '');
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('[data-public-filter-error]')).toBeNull();
    expect(cards()).toHaveLength(1);
  });

  it('preserves search focus and debounce across realtime refresh', async () => {
    await render();
    const input = searchInput();
    input.focus();
    input.value = 'nee';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(searchInput()).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe('nee');
    expect(lastSnapshotUrl().searchParams.get('search')).toBe('nee');
  });

  it('URL navigation updates search, panel, and sprint pill state in place', async () => {
    await render('/ignite?search=old');
    const input = searchInput();
    const toggleEl = toggle();
    const topbar = appEl.querySelector('.topbar');
    // A pending keystroke loses to the navigation.
    input.value = 'typing';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    history.pushState({}, '', '/ignite?priority=high&sprintNumber=2');
    await applyPublicBoardRoute({ slug: 'ignite', openTodoSegment: null });
    expect(appEl.querySelector('.topbar')).toBe(topbar);
    expect(searchInput()).toBe(input);
    expect(toggle()).toBe(toggleEl);
    expect(input.value).toBe('');
    expect(appEl.querySelector('#publicSearchClear')?.hasAttribute('hidden')).toBe(true);
    expect(activeValue('data-public-priority-option')).toBe('high');
    expect(appEl.querySelector('#publicOmniPinnedTags [data-public-clear-sprint]')).not.toBeNull();
    expect(toggle().classList.contains('search-filter-toggle--active')).toBe(true);
    await vi.advanceTimersByTimeAsync(1000);
    expect(lastSnapshotUrl().searchParams.get('priority')).toBe('high');
    expect(lastSnapshotUrl().searchParams.get('sprintNumber')).toBe('2');
    expect(lastSnapshotUrl().searchParams.has('search')).toBe(false);
    expect(new URL(window.location.href).searchParams.has('search')).toBe(false);
  });

  it('ignores unsupported values even if an option is tampered with', async () => {
    await render();
    toggle().click();
    const priority = panelOption('data-public-priority-option', 'high')!;
    const before = requests.length;
    for (const value of ['**none**', 'urgent', 'scheduled', '2']) {
      priority.setAttribute('data-public-priority-option', value);
      priority.click();
    }
    // A forged sprint option inside the popover has no handler.
    const forged = document.createElement('button');
    forged.setAttribute('data-public-sprint-option', '2');
    panel().appendChild(forged);
    forged.click();
    await vi.advanceTimersByTimeAsync(10);
    expect(requests.length).toBe(before);
    const url = new URL(window.location.href);
    expect(url.searchParams.has('sprintNumber')).toBe(false);
    expect(url.searchParams.has('priority')).toBe(false);
  });

  it('offers no unsupported options, mutation controls, or authenticated requests', async () => {
    await render();
    toggle().click();
    const anonymousPanel = panel().innerHTML;
    stopPublicBoard();

    setUser({ id: 42, name: 'Nonmember', email: 'n@example.test' } as any);
    await render();
    toggle().click();
    // Anonymous and authenticated visitors get the same filter structure.
    expect(panel().innerHTML).toBe(anonymousPanel);
    const priorityValues = Array.from(appEl.querySelectorAll('[data-public-priority-option]')).map((o) => o.getAttribute('data-public-priority-option'));
    expect(priorityValues).toEqual(['', 'high']);
    expect(panel().querySelector('[data-public-sprint-option], [data-assignee-option], [data-sort-option], [data-sprint-option], [data-priority-option], input, select, form')).toBeNull();
    expect(panel().textContent).not.toMatch(/sprint|scheduled|noPriority|assignee|sort/i);
    pickPanelOption('data-public-priority-option', 'high');
    await vi.advanceTimersByTimeAsync(10);
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('after a member board, public controls never reach the member filter controller', async () => {
    // Member board first: installs board-filters' persistent document listeners.
    history.replaceState({}, '', '/ignite');
    appEl.innerHTML = `<div class="topbar"><div class="search-input-wrapper"><input id="searchInput" />${buildFilterPanelHtml(null, null, [], null, null, [{ key: 'high', name: 'High', color: '#ff0000', position: 0 }])}</div></div>`;
    const memberReload = vi.fn(async () => {});
    bindBoardFilterUi({ reloadBoard: memberReload, showError: () => {} });
    (document.getElementById('searchFilterToggle') as HTMLButtonElement).click();
    expect((document.getElementById('searchFilterPanel') as HTMLElement).hidden).toBe(false);
    resetBoardFilterUiState();

    // Navigate to the public board with a sprint deep link.
    await render('/ignite?sprintNumber=2');
    expect(document.getElementById('searchFilterToggle')).toBeNull();
    toggle().click();
    expect(panel().hidden).toBe(false);
    pickPanelOption('data-public-priority-option', 'high');
    await vi.advanceTimersByTimeAsync(10);
    toggle().click();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(panel().hidden).toBe(true);
    (appEl.querySelector('[data-public-clear-sprint]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);

    expect(memberReload).not.toHaveBeenCalled();
    expect(toastEl.textContent).not.toContain('board.filters.filteringOn');
    const url = new URL(window.location.href);
    for (const memberParam of ['sprintId', 'assignee', 'sort']) expect(url.searchParams.has(memberParam)).toBe(false);
    expect(url.searchParams.get('priority')).toBe('high');
    expect(url.searchParams.has('sprintNumber')).toBe(false);
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
  });

  it('shows the search clear button while there is text and hides it again when cleared', async () => {
    await render();
    const clear = appEl.querySelector('#publicSearchClear') as HTMLButtonElement;
    expect(clear.hidden).toBe(true);
    searchInput().value = 'nee';
    searchInput().dispatchEvent(new Event('input', { bubbles: true }));
    expect(clear.hidden).toBe(false);
    clear.click();
    expect(clear.hidden).toBe(true);
    expect(searchInput().value).toBe('');
    expect(document.activeElement).toBe(searchInput());
    await vi.advanceTimersByTimeAsync(1000);
    expect(new URL(window.location.href).searchParams.has('search')).toBe(false);
  });

  it('removes its document listeners when the public board stops', async () => {
    await render();
    toggle().click();
    stopPublicBoard();
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(panel().hidden).toBe(false);
  });
});

describe('public board Omni tags', () => {
  const TAGS = [
    { name: 'api', color: '#00ff00', activeCount: 3 },
    { name: 'apex', color: 'javascript:alert(1)', activeCount: 1 },
    { name: 'backend', color: '#0000ff', activeCount: 2 },
    { name: 'idle', color: '#ff00ff', activeCount: 0 },
  ];
  let tags: typeof TAGS;
  let snapshotFail: ((url: URL) => boolean) | null;

  function snapshotRequests(): URL[] {
    return requests.filter((p) => p.startsWith('/api/public/board/ignite?')).map((p) => new URL(p, 'https://example.test'));
  }
  function names(selector: string, attr: string): string[] {
    return Array.from(appEl.querySelectorAll(selector)).map((el) => el.getAttribute(attr) ?? '');
  }
  const candidates = () => names('#publicOmniCandidateViewport [data-public-omni-tag]', 'data-public-omni-tag');
  const pinned = () => names('#publicOmniPinnedTags [data-public-omni-clear-tag]', 'data-public-omni-clear-tag');
  const mobile = () => Array.from(appEl.querySelectorAll('#publicOmniMobileTagPills .omni-tag-pill')).map((el) => el.querySelector('span')?.textContent ?? el.textContent);
  const input = () => appEl.querySelector('#publicSearchInput') as HTMLInputElement;
  function type(value: string): void {
    input().focus();
    input().value = value;
    input().dispatchEvent(new Event('input', { bubbles: true }));
  }
  function candidate(name: string): HTMLButtonElement {
    return Array.from(appEl.querySelectorAll<HTMLButtonElement>('#publicOmniCandidateViewport [data-public-omni-tag]'))
      .find((el) => el.getAttribute('data-public-omni-tag') === name)!;
  }
  function clearButton(name: string): HTMLButtonElement {
    return Array.from(appEl.querySelectorAll<HTMLButtonElement>('#publicOmniPinnedTags [data-public-omni-clear-tag]'))
      .find((el) => el.getAttribute('data-public-omni-clear-tag') === name)!;
  }
  async function render(url = '/ignite'): Promise<void> {
    history.replaceState({}, '', url);
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
  }

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    resetAppRuntimeForTests();
    tags = TAGS;
    snapshotFail = null;
    handler = (path) => {
      const url = new URL(path, 'https://example.test');
      if (url.pathname === '/api/public/board/ignite') {
        if (snapshotFail?.(url)) return { status: 503, body: {} };
        return { status: 200, body: snapshot({ tags }) };
      }
      return defaultHandler(path);
    };
    installTransport();
    appEl.innerHTML = '';
    toastEl.textContent = '';
    document.body.replaceChildren(appEl, toastEl);
    setUser(null);
    setSlug('ignite');
    setBoard(null);
    setProjectId(null);
    setBoardAccess(null);
  });

  afterEach(() => {
    stopPublicBoard();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    resetAppRuntimeForTests();
  });

  it('replaces the static chips with Browse candidates ordered by active count; no All chip', async () => {
    await render();
    expect(appEl.querySelector('.chip, [data-public-tag], #publicTagChips')).toBeNull();
    expect(candidates()).toEqual(['api', 'backend', 'apex']);
    expect(appEl.querySelectorAll('#publicOmniCandidateViewport .omni-tag-pill--browse')).toHaveLength(3);
    // Empty search: no Browse candidates on the mobile rail.
    expect(mobile()).toEqual([]);
    // Unsafe color dropped; safe colors tint only, text keeps the theme color.
    expect(candidate('apex').hasAttribute('style')).toBe(false);
    expect(candidate('api').getAttribute('style')).toContain('color:var(--text)');
  });

  it('shows matching suggestions immediately while typing, before the debounce', async () => {
    await render();
    const before = snapshotRequests().length;
    type('AP');
    expect(candidates()).toEqual(['apex', 'api']);
    expect(appEl.querySelectorAll('#publicOmniCandidateViewport .omni-tag-pill--suggestion')).toHaveLength(2);
    expect(mobile()).toEqual(['apex', 'api']);
    expect(snapshotRequests().length).toBe(before);
    type('');
    expect(candidates()).toEqual(['api', 'backend', 'apex']);
  });

  it('selecting a suggestion before the debounce consumes the search and issues exactly one reload', async () => {
    await render('/ignite?priority=high&sprintNumber=2');
    const before = snapshotRequests().length;
    type('ape');
    expect(appEl.querySelector<HTMLButtonElement>('#publicSearchClear')!.hidden).toBe(false);
    candidate('apex').click();
    expect(input().value).toBe('');
    expect(appEl.querySelector<HTMLButtonElement>('#publicSearchClear')!.hidden).toBe(true);
    expect(document.activeElement).toBe(input());
    expect(pinned()).toEqual(['apex']);
    expect(candidates()).toEqual(['api', 'backend']);
    await vi.advanceTimersByTimeAsync(1000);
    const after = snapshotRequests().slice(before);
    expect(after).toHaveLength(1);
    expect(after[0].searchParams.getAll('tag')).toEqual(['apex']);
    expect(after[0].searchParams.has('search')).toBe(false);
    expect(after[0].searchParams.get('priority')).toBe('high');
    expect(after[0].searchParams.get('sprintNumber')).toBe('2');
    const url = new URL(window.location.href);
    expect(url.searchParams.getAll('tag')).toEqual(['apex']);
    expect(url.searchParams.has('search')).toBe(false);
  });

  it('adds the canonical published name even if the typed case differs', async () => {
    tags = [{ name: 'Backend', color: null as unknown as string, activeCount: 1 }];
    await render();
    const btn = candidate('Backend');
    btn.setAttribute('data-public-omni-tag', 'BACKEND');
    btn.click();
    await vi.advanceTimersByTimeAsync(10);
    expect(snapshotRequests().at(-1)!.searchParams.getAll('tag')).toEqual(['Backend']);
  });

  it('removing one tag keeps every other filter, including a pending search, in one reload', async () => {
    await render('/ignite?tag=api&tag=backend&priority=high&sprintNumber=2');
    expect(pinned()).toEqual(['api', 'backend']);
    const before = snapshotRequests().length;
    type('needle');
    clearButton('api').click();
    expect(pinned()).toEqual(['backend']);
    expect(document.activeElement).toBe(input());
    await vi.advanceTimersByTimeAsync(1000);
    const after = snapshotRequests().slice(before);
    expect(after).toHaveLength(1);
    expect(after[0].searchParams.getAll('tag')).toEqual(['backend']);
    expect(after[0].searchParams.get('search')).toBe('needle');
    expect(after[0].searchParams.get('priority')).toBe('high');
    expect(after[0].searchParams.get('sprintNumber')).toBe('2');
  });

  it('rejects unknown or tampered values and stops at ten tags', async () => {
    tags = Array.from({ length: 12 }, (_, i) => ({ name: `t${i}`, color: '#00ff00', activeCount: 12 - i }));
    await render();
    const before = requests.length;
    const btn = candidate('t0');
    for (const value of ['nope', '', 't0 ']) {
      btn.setAttribute('data-public-omni-tag', value);
      btn.click();
    }
    const clear = document.createElement('button');
    clear.setAttribute('data-public-omni-clear-tag', 'not-selected');
    appEl.querySelector('#publicOmniPinnedTags')!.append(clear);
    clear.click();
    await vi.advanceTimersByTimeAsync(10);
    expect(requests.length).toBe(before);

    stopPublicBoard();
    await render(`/ignite?${tags.slice(0, 10).map((tag) => `tag=${tag.name}`).join('&')}`);
    expect(pinned()).toHaveLength(10);
    expect(candidates()).toEqual([]);
    type('t1');
    expect(candidates()).toEqual([]);
    // Even a forged add of a published tag is refused at the limit.
    const forged = document.createElement('button');
    forged.setAttribute('data-public-omni-tag', 't11');
    appEl.querySelector('#publicOmniCandidateViewport')!.append(forged);
    const count = snapshotRequests().length;
    forged.click();
    await vi.advanceTimersByTimeAsync(1000);
    expect(new URL(window.location.href).searchParams.getAll('tag')).toHaveLength(10);
    expect(snapshotRequests().every((u) => u.searchParams.getAll('tag').length <= 10)).toBe(true);
    expect(snapshotRequests().slice(count).every((u) => !u.searchParams.getAll('tag').includes('t11'))).toBe(true);
  });

  it('clearing a still-debouncing search restores Browse and sends nothing', async () => {
    await render();
    const before = snapshotRequests().length;
    type('ap');
    expect(candidates()).toEqual(['apex', 'api']);
    (appEl.querySelector('#publicSearchClear') as HTMLButtonElement).click();
    expect(candidates()).toEqual(['api', 'backend', 'apex']);
    expect(mobile()).toEqual([]);
    await vi.advanceTimersByTimeAsync(1000);
    expect(snapshotRequests().length).toBe(before);
  });

  it('keeps a URL tag that left the vocabulary as a removable pinned pill', async () => {
    await render('/ignite?tag=Vanished&tag=api');
    expect(pinned()).toEqual(['Vanished', 'api']);
    clearButton('Vanished').click();
    await vi.advanceTimersByTimeAsync(10);
    expect(new URL(window.location.href).searchParams.getAll('tag')).toEqual(['api']);
    expect(pinned()).toEqual(['api']);
  });

  it('a failed tag filter shows the error with the pinned tag still visible', async () => {
    await render();
    snapshotFail = (url) => url.searchParams.getAll('tag').includes('api');
    candidate('api').click();
    await vi.advanceTimersByTimeAsync(10);
    expect(appEl.querySelector('[data-public-filter-error]')).not.toBeNull();
    expect(pinned()).toEqual(['api']);
  });

  it('SSE refreshes keep the Omni shell, unchanged pills, scroll, and focus', async () => {
    await render('/ignite?tag=api');
    const shell = appEl.querySelector('[data-public-omni]');
    const viewport = appEl.querySelector<HTMLElement>('#publicOmniCandidateViewport')!;
    const pill = candidate('backend');
    pill.focus();
    Object.defineProperty(viewport, 'scrollWidth', { configurable: true, value: 500 });
    Object.defineProperty(viewport, 'clientWidth', { configurable: true, value: 100 });
    viewport.scrollLeft = 60;
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(appEl.querySelector('[data-public-omni]')).toBe(shell);
    expect(candidate('backend')).toBe(pill);
    expect(document.activeElement).toBe(pill);
    expect(viewport.scrollLeft).toBe(60);

    // A vocabulary change re-renders the candidates and keeps focus on the same tag.
    tags = [...TAGS, { name: 'zed', color: '#00ff00', activeCount: 9 }];
    streams[0].send('{"type":"refresh_needed"}');
    await vi.advanceTimersByTimeAsync(1000);
    expect(candidates()).toEqual(['zed', 'backend', 'apex']);
    expect(document.activeElement).toBe(candidate('backend'));
    expect(appEl.querySelector('[data-public-omni]')).toBe(shell);
  });

  it('URL navigation updates pinned tags in place', async () => {
    await render('/ignite?tag=api');
    const shell = appEl.querySelector('[data-public-omni]');
    history.pushState({}, '', '/ignite?tag=backend&tag=apex');
    await applyPublicBoardRoute({ slug: 'ignite', openTodoSegment: null });
    expect(pinned()).toEqual(['backend', 'apex']);
    expect(candidates()).toEqual(['api']);
    expect(appEl.querySelector('[data-public-omni]')).toBe(shell);
  });

  it('teardown disconnects the resize observer and drops Omni listeners', async () => {
    const observers: { disconnect: ReturnType<typeof vi.fn>; observe: ReturnType<typeof vi.fn> }[] = [];
    vi.stubGlobal('ResizeObserver', class {
      disconnect = vi.fn();
      observe = vi.fn();
      constructor() { observers.push(this); }
    });
    await render();
    expect(observers).toHaveLength(1);
    expect(observers[0].observe).toHaveBeenCalledTimes(2);
    const btn = candidate('api');
    stopPublicBoard();
    expect(observers[0].disconnect).toHaveBeenCalledTimes(1);
    const before = requests.length;
    btn.click();
    window.dispatchEvent(new Event('resize'));
    await vi.advanceTimersByTimeAsync(10);
    expect(requests.length).toBe(before);
  });

  it('after a member Omni board, member handlers never act on public Omni controls', async () => {
    history.replaceState({}, '', '/ignite');
    appEl.innerHTML = `<div class="topbar"><div class="search-input-wrapper"><input id="searchInput" />${buildFilterPanelHtml(null, null, [], null)}</div></div>${buildOmniFilterRowHtml({} as never, { searchInTopbar: true })}`;
    const memberReload = vi.fn(async () => {});
    bindBoardFilterUi({ reloadBoard: memberReload, showError: () => {} });
    const memberBar = appEl.querySelector<HTMLElement>('#omniPinnedTags')!.closest<HTMLElement>('.omni-bar')!;
    expect(memberBar.onclick).not.toBeNull();
    resetBoardFilterUiState();

    await render('/ignite');
    expect(appEl.querySelector('[id^="omni"]')).toBeNull();
    expect(appEl.querySelector<HTMLElement>('[data-public-omni] .omni-bar')!.onclick).toBeNull();
    candidate('api').click();
    await vi.advanceTimersByTimeAsync(10);
    clearButton('api').click();
    window.dispatchEvent(new Event('resize'));
    await vi.advanceTimersByTimeAsync(10);
    expect(memberReload).not.toHaveBeenCalled();
    const url = new URL(window.location.href);
    for (const memberParam of ['sprintId', 'assignee', 'sort']) expect(url.searchParams.has(memberParam)).toBe(false);
    expect(requests.every((p) => p.startsWith('/api/public/board/ignite'))).toBe(true);
    expect(snapshotRequests().some((u) => u.searchParams.getAll('tag').includes('api'))).toBe(true);
  });
});
