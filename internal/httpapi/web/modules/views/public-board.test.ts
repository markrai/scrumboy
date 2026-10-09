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
import { setBoard, setBoardAccess, setProjectId, setSlug, setUser } from '../state/mutations.js';
import { getBoard, getBoardAccess, getProjectId } from '../state/selectors.js';
import {
  applyPublicBoardRoute,
  isPublicBoardSessionFor,
  resolvePublicBoard,
  stopPublicBoard,
  __getPublicBoardSessionForTest,
} from './public-board.js';

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
    expect(appEl.querySelector('.brand + .brand, .topbar .brand:nth-of-type(2)')?.textContent).toContain('Ignite <b>Board</b>');
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

  it('applies tag, sprint, priority, and search filters through the public query', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);

    (appEl.querySelector('[data-public-tag="api"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    let last = new URL(requests.filter((p) => p.startsWith('/api/public/board/ignite?')).at(-1)!, 'https://example.test');
    expect(last.searchParams.getAll('tag')).toEqual(['api']);
    expect(new URL(window.location.href).searchParams.getAll('tag')).toEqual(['api']);

    const sprint = appEl.querySelector('#publicSprintFilter') as HTMLSelectElement;
    sprint.value = '2';
    sprint.dispatchEvent(new Event('change', { bubbles: true }));
    await vi.advanceTimersByTimeAsync(10);
    const priority = appEl.querySelector('#publicPriorityFilter') as HTMLSelectElement;
    priority.value = 'high';
    priority.dispatchEvent(new Event('change', { bubbles: true }));
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
    (appEl.querySelector('[data-public-tag="api"]') as HTMLButtonElement).click();
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

  function sprintOptions(): string[] {
    return Array.from(appEl.querySelectorAll<HTMLOptionElement>('#publicSprintFilter option')).map((o) => o.value + ':' + o.textContent);
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
    (appEl.querySelector('[data-public-tag="api"]') as HTMLButtonElement).click();
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
    expect((appEl.querySelector('#publicSprintFilter') as HTMLSelectElement).value).toBe('');
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
    expect(appEl.querySelector('#publicSprintFilter')).not.toBeNull();

    snapshotOverride = () => ({ status: 200, body: snapshot({ project: { slug: 'ignite', name: 'Ignite', dominantColor: '', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: false } }) });
    await refreshViaStream();
    expect(appEl.querySelector('#publicSprintFilter')).toBeNull();
    expect(sprintRequests()).toBe(1);
    expect(new URL(window.location.href).searchParams.has('sprintNumber')).toBe(false);
    expect(lastSnapshotUrl().searchParams.has('sprintNumber')).toBe(false);

    snapshotOverride = null;
    await refreshViaStream();
    expect(sprintRequests()).toBe(2);
    expect(appEl.querySelector('#publicSprintFilter')).not.toBeNull();
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
    expect(appEl.querySelector('#publicSprintFilter')).toBeNull();
    expect(appEl.textContent).not.toContain('Late');
  });

  it('a failed filter request replaces stale cards with an error and retry', async () => {
    await resolvePublicBoard({ slug: 'ignite', openTodoSegment: null });
    await vi.advanceTimersByTimeAsync(10);
    expect(cards()).toHaveLength(1);

    snapshotOverride = (url) => (url.searchParams.getAll('tag').includes('api') ? { status: 503, body: {} } : null);
    (appEl.querySelector('[data-public-tag="api"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(10);
    expect(cards()).toHaveLength(0);
    expect(appEl.querySelector('[data-public-filter-error]')?.getAttribute('role')).toBe('alert');
    expect(appEl.querySelector('.public-board__no-results')).toBeNull();
    // The selected filter stays visible with the error, never with old cards.
    expect(appEl.querySelector('[data-public-tag="api"]')?.getAttribute('aria-pressed')).toBe('true');

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
