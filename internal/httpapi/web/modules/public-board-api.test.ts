// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from './platform/runtime.js';
import type { ServerTransport } from './platform/server-transport.js';
import {
  fetchPublicBoardSnapshot,
  fetchPublicLanePage,
  fetchPublicSprints,
  fetchPublicTodo,
  fetchPublicTodoLinks,
  normalizePublicBoardSnapshot,
  normalizePublicQuery,
  PublicBoardApiError,
  publicBoardEventsPath,
  EMPTY_PUBLIC_QUERY,
} from './public-board-api.js';

function publicSnapshotFixture(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    access: { kind: 'public', readOnly: true },
    project: { slug: 'ignite', name: 'Ignite', dominantColor: '#112233', estimationMode: 'MODIFIED_FIBONACCI', sprintsEnabled: true },
    workflow: [
      { key: 'done', name: 'Done', color: '#ef4444', isDone: true, position: 2 },
      { key: 'backlog', name: 'Backlog', color: '#9ca3af', isDone: false, position: 0 },
    ],
    priorities: [{ key: 'high', name: 'High', color: '#ff0000', position: 0 }],
    tags: [{ name: 'api', color: '#00ff00', activeCount: 2 }],
    columns: {
      backlog: [{ localId: 7, title: 'Card', body: 'Body', columnKey: 'backlog', estimationPoints: 3, priorityKey: 'high', sprintNumber: 2, tags: [{ name: 'api', color: '#00ff00' }] }],
      done: [],
    },
    columnsMeta: {
      backlog: { hasMore: true, nextCursor: 'opaque+/=cursor', totalCount: 30 },
      done: { hasMore: false, nextCursor: null, totalCount: 0 },
    },
    ...overrides,
  };
}

type Recorded = { path: string; options: any };

function installTransport(handler: (path: string) => { status: number; body?: unknown } | Error): Recorded[] {
  const calls: Recorded[] = [];
  const transport: ServerTransport = {
    request: vi.fn(async (path: string, options: any) => {
      calls.push({ path, options });
      const result = handler(path);
      if (result instanceof Error) throw result;
      return {
        ok: result.status >= 200 && result.status < 300,
        status: result.status,
        json: async () => {
          if (result.body === undefined) throw new SyntaxError('no json');
          return result.body;
        },
        blob: async () => new Blob(),
      };
    }),
    openEventStream: vi.fn(),
    acquireResource: vi.fn(),
    logout: vi.fn(),
  };
  const runtime = {
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
  } as unknown as AppRuntime;
  installAppRuntime(runtime);
  return calls;
}

describe('public board API client', () => {
  beforeEach(() => resetAppRuntimeForTests());
  afterEach(() => resetAppRuntimeForTests());

  it('normalizes the exact snapshot contract without fabricating identifiers', async () => {
    const calls = installTransport(() => ({ status: 200, body: publicSnapshotFixture() }));
    const snapshot = await fetchPublicBoardSnapshot('ignite', EMPTY_PUBLIC_QUERY);

    expect(calls).toHaveLength(1);
    expect(calls[0].path).toBe('/api/public/board/ignite?limitPerLane=20');
    expect(calls[0].options.method).toBe('GET');
    expect(calls[0].options.headers).not.toHaveProperty('X-Scrumboy');
    // Workflow is ordered by server position; only workflow lanes are kept.
    expect(snapshot.workflow.map((c) => c.key)).toEqual(['backlog', 'done']);
    expect(snapshot.columns.backlog[0]).toEqual({
      localId: 7, title: 'Card', body: 'Body', columnKey: 'backlog', estimationPoints: 3,
      priorityKey: 'high', sprintNumber: 2, tags: [{ name: 'api', color: '#00ff00' }],
    });
    expect(snapshot.columnsMeta.backlog).toEqual({ hasMore: true, nextCursor: 'opaque+/=cursor', totalCount: 30 });
    const serialized = JSON.stringify(snapshot);
    for (const forbidden of ['"id"', 'projectId', 'userId', 'assignee', 'creator', 'role', 'members', 'image']) {
      expect(serialized).not.toContain(forbidden);
    }
  });

  it('drops unknown fields so private data can never pass through the adapter', () => {
    const fixture = publicSnapshotFixture();
    (fixture.project as any).id = 99;
    (fixture.project as any).ownerUserId = 5;
    ((fixture.columns as any).backlog[0] as any).assigneeUserId = 12;
    ((fixture.columns as any).backlog[0] as any).id = 4242;
    const snapshot = normalizePublicBoardSnapshot(fixture, 'ignite');
    expect(snapshot.project).not.toHaveProperty('id');
    expect(snapshot.project).not.toHaveProperty('ownerUserId');
    expect(snapshot.columns.backlog[0]).not.toHaveProperty('assigneeUserId');
    expect(snapshot.columns.backlog[0]).not.toHaveProperty('id');
  });

  it.each([
    ['non-public access', { access: { kind: 'member', readOnly: true } }],
    ['writable access', { access: { kind: 'public', readOnly: false } }],
    ['slug mismatch', { project: { slug: 'other', name: 'x', dominantColor: '', estimationMode: 'x', sprintsEnabled: false } }],
    ['bad workflow', { workflow: 'nope' }],
    ['bad todo id', { columns: { backlog: [{ localId: 0, title: 't', body: '', columnKey: 'backlog', tags: [] }] } }],
    ['bad todo title', { columns: { backlog: [{ localId: 1, title: 5, body: '', columnKey: 'backlog', tags: [] }] } }],
    ['bad meta', { columnsMeta: { backlog: { hasMore: 'yes', nextCursor: null, totalCount: 1 } } }],
  ])('fails closed on a malformed snapshot: %s', (_label, override) => {
    expect(() => normalizePublicBoardSnapshot(publicSnapshotFixture(override), 'ignite')).toThrow(PublicBoardApiError);
  });

  it('sanitizes colors and never renders an unsafe value', () => {
    const snapshot = normalizePublicBoardSnapshot(publicSnapshotFixture({
      tags: [{ name: 'x', color: 'red;background:url(javascript:alert(1))' }],
    }), 'ignite');
    expect(snapshot.tags[0].color).toBeNull();
  });

  it.each([
    [404, 'not_found'],
    [429, 'rate_limited'],
    [400, 'invalid_request'],
    [500, 'unavailable'],
    [503, 'unavailable'],
  ])('maps HTTP %s to %s', async (status, kind) => {
    installTransport(() => ({ status, body: { error: { code: 'X' } } }));
    await expect(fetchPublicBoardSnapshot('ignite', EMPTY_PUBLIC_QUERY)).rejects.toMatchObject({ kind, status });
  });

  it('distinguishes network failure, abort, and malformed JSON', async () => {
    installTransport(() => new TypeError('offline'));
    await expect(fetchPublicBoardSnapshot('ignite', EMPTY_PUBLIC_QUERY)).rejects.toMatchObject({ kind: 'network' });

    const controller = new AbortController();
    controller.abort();
    installTransport(() => Object.assign(new Error('aborted'), { name: 'AbortError' }));
    await expect(fetchPublicBoardSnapshot('ignite', EMPTY_PUBLIC_QUERY, { signal: controller.signal })).rejects.toMatchObject({ kind: 'aborted' });

    installTransport(() => ({ status: 200 }));
    await expect(fetchPublicBoardSnapshot('ignite', EMPTY_PUBLIC_QUERY)).rejects.toMatchObject({ kind: 'malformed' });
  });

  it('bounds queries to the public contract and never sends sort', async () => {
    const calls = installTransport(() => ({ status: 200, body: publicSnapshotFixture() }));
    const tags = Array.from({ length: 14 }, (_, i) => `t${i}`);
    await fetchPublicBoardSnapshot('ignite', {
      search: `  ${'é'.repeat(250)}  `,
      tags: [...tags, 'T0'],
      sprintNumber: 4,
      priority: 'high',
    }, { limitPerLane: 500 });
    const url = new URL(calls[0].path, 'https://example.test');
    expect(url.pathname).toBe('/api/public/board/ignite');
    expect(url.searchParams.get('limitPerLane')).toBe('50');
    expect(Array.from(url.searchParams.get('search') ?? '')).toHaveLength(200);
    expect(url.searchParams.getAll('tag')).toEqual(tags.slice(0, 10));
    expect(url.searchParams.get('sprintNumber')).toBe('4');
    expect(url.searchParams.get('priority')).toBe('high');
    expect(url.searchParams.has('sort')).toBe(false);
    expect([...url.searchParams.keys()].every((key) => ['limitPerLane', 'search', 'tag', 'sprintNumber', 'priority'].includes(key))).toBe(true);
  });

  it('sends the opaque lane cursor unchanged and caps the page size', async () => {
    const calls = installTransport(() => ({ status: 200, body: { items: [], hasMore: false, nextCursor: null, totalCount: 0 } }));
    await fetchPublicLanePage('ignite', 'in progress', { ...EMPTY_PUBLIC_QUERY, tags: ['a'] }, 'v1.ab+/=', { limit: 999 });
    const url = new URL(calls[0].path, 'https://example.test');
    expect(url.pathname).toBe('/api/public/board/ignite/lanes/in%20progress');
    expect(url.searchParams.get('afterCursor')).toBe('v1.ab+/=');
    expect(url.searchParams.get('limit')).toBe('50');
    expect(url.searchParams.getAll('tag')).toEqual(['a']);
  });

  it('reads story detail, links, and sprints only from public endpoints', async () => {
    const calls = installTransport((path) => {
      if (path.endsWith('/links')) return { status: 200, body: { links: [{ direction: 'outbound', localId: 3, title: 'Linked' }] } };
      if (path.endsWith('/sprints')) return { status: 200, body: { sprints: [{ number: 2, name: 'S2', state: 'ACTIVE' }] } };
      return { status: 200, body: { todo: { localId: 7, title: 'T', body: '', columnKey: 'backlog', tags: [] } } };
    });
    await expect(fetchPublicTodo('ignite', 7)).resolves.toMatchObject({ localId: 7, estimationPoints: null, priorityKey: null, sprintNumber: null });
    await expect(fetchPublicTodoLinks('ignite', 7)).resolves.toEqual([{ direction: 'outbound', localId: 3, title: 'Linked' }]);
    await expect(fetchPublicSprints('ignite')).resolves.toEqual([{ number: 2, name: 'S2', state: 'ACTIVE' }]);
    expect(calls.map((c) => c.path)).toEqual([
      '/api/public/board/ignite/todos/7',
      '/api/public/board/ignite/todos/7/links',
      '/api/public/board/ignite/sprints',
    ]);
  });

  it('rejects a detail for a different story and unknown link directions', async () => {
    installTransport((path) => path.endsWith('/links')
      ? { status: 200, body: { links: [{ direction: 'sideways', localId: 3, title: 'x' }] } }
      : { status: 200, body: { todo: { localId: 8, title: 'T', body: '', columnKey: 'backlog', tags: [] } } });
    await expect(fetchPublicTodo('ignite', 7)).rejects.toMatchObject({ kind: 'malformed' });
    await expect(fetchPublicTodoLinks('ignite', 7)).rejects.toMatchObject({ kind: 'malformed' });
  });

  it('refuses invalid slugs and IDs before any request', async () => {
    const calls = installTransport(() => ({ status: 200, body: {} }));
    await expect(fetchPublicBoardSnapshot('../api/board/x', EMPTY_PUBLIC_QUERY)).rejects.toMatchObject({ kind: 'not_found' });
    await expect(fetchPublicTodo('ignite', -1)).rejects.toMatchObject({ kind: 'not_found' });
    expect(() => publicBoardEventsPath('Bad Slug')).toThrow(PublicBoardApiError);
    expect(calls).toHaveLength(0);
    expect(publicBoardEventsPath('ignite')).toBe('/api/public/board/ignite/events');
  });

  it('normalizes queries deterministically', () => {
    expect(normalizePublicQuery({ search: '  x ', tags: [' a ', 'A', ''], sprintNumber: -2, priority: ' ' }))
      .toEqual({ search: 'x', tags: ['a'], sprintNumber: null, priority: null });
  });
});
