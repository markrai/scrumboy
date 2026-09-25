// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  hydrateDashboardWidgetFromNetwork,
  publishCompleteDashboardWidgetSnapshot,
  publishDashboardWidgetSnapshot,
  setDashboardWidgetCurrentUser,
} from './dashboard-widget-publish.js';
import { DASHBOARD_WIDGET_CAPABILITY } from './platform/dashboard-widget.js';
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from './platform/runtime.js';
import type { ServerTransport } from './platform/server-transport.js';
import type { DashboardSummary, DashboardTodo } from './types.js';

const { apiFetchMock, getUserMock, getDashboardTodoSortMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn(),
  getUserMock: vi.fn(),
  getDashboardTodoSortMock: vi.fn(() => 'activity'),
}));

vi.mock('./api.js', () => ({
  apiFetch: apiFetchMock,
}));

vi.mock('./state/selectors.js', () => ({
  getUser: getUserMock,
  getDashboardTodoSort: getDashboardTodoSortMock,
}));

afterEach(() => {
  resetAppRuntimeForTests();
  vi.restoreAllMocks();
  apiFetchMock.mockReset();
  getUserMock.mockReset();
  getDashboardTodoSortMock.mockReset();
  getDashboardTodoSortMock.mockReturnValue('activity');
});

function installMobile(capability: unknown): { publish: ReturnType<typeof vi.fn>; clear: ReturnType<typeof vi.fn>; setCurrentUser: ReturnType<typeof vi.fn> } {
  const publish = vi.fn(async () => undefined);
  const clear = vi.fn(async () => undefined);
  const setCurrentUser = vi.fn(async () => undefined);
  const transport = {} as ServerTransport;
  const runtime: AppRuntime = {
    kind: 'capacitor',
    capability: (name) => (name === DASHBOARD_WIDGET_CAPABILITY
      ? (capability === undefined ? { publish, clear, setCurrentUser } : capability)
      : null) as never,
    assetOrigin: () => 'https://localhost',
    serverOrigin: () => 'https://scrumboy.example',
    publicLinkOrigin: () => 'https://scrumboy.example',
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => true,
    startInteractiveOIDC: vi.fn(async () => undefined),
    transport: () => transport,
  };
  installAppRuntime(runtime);
  return { publish, clear, setCurrentUser };
}

const summary: DashboardSummary = {
  assignedCount: 1,
  totalAssignedStoryPoints: 1,
  wipCount: 1,
};
const todos: DashboardTodo[] = [{
  id: 1,
  localId: 4,
  title: 'Card',
  projectId: 1,
  projectName: 'P',
  projectSlug: 'p',
  projectDominantColor: '#000000',
  status: 'doing',
  statusName: 'Doing',
  statusColor: '#112233',
  updatedAt: '2026-01-01T00:00:00.000Z',
}];

describe('dashboard widget publication', () => {
  it('no-ops when the dashboard-widget capability is absent', async () => {
    const runtime: AppRuntime = {
      kind: 'browser',
      capability: () => null,
      assetOrigin: () => '',
      serverOrigin: () => '',
      publicLinkOrigin: () => '',
      supportsPWA: () => true,
      supportsWebPush: () => true,
      supportsInteractiveOIDC: () => true,
      startInteractiveOIDC: vi.fn(async () => undefined),
      transport: () => ({}) as ServerTransport,
    };
    installAppRuntime(runtime);
    await expect(publishDashboardWidgetSnapshot(summary, todos, 1)).resolves.toBeUndefined();
  });

  it('publishes mapped payloads and swallows plugin failures', async () => {
    const { publish } = installMobile(undefined);
    await publishDashboardWidgetSnapshot(summary, todos, 9);
    expect(publish).toHaveBeenCalledTimes(1);
    expect(publish.mock.calls[0][0]).toMatchObject({
      userId: 9,
      assignedCount: 1,
      wipCount: 1,
      items: [{ localId: 4, title: 'Card' }],
    });

    publish.mockRejectedValueOnce(new Error('native failed'));
    await expect(publishDashboardWidgetSnapshot(summary, todos, 9)).resolves.toBeUndefined();
  });

  it('clears native widget identity when the current user is missing', async () => {
    const { clear, setCurrentUser } = installMobile(undefined);
    await setDashboardWidgetCurrentUser(null);
    expect(clear).toHaveBeenCalledOnce();
    expect(setCurrentUser).not.toHaveBeenCalled();
  });

  it('does not fetch or publish until native current-user identity is established', async () => {
    let identityReady = false;
    let releaseIdentity: () => void = () => undefined;
    const identityGate = new Promise<void>((resolve) => {
      releaseIdentity = resolve;
    });
    const setCurrentUser = vi.fn(async () => {
      await identityGate;
      identityReady = true;
    });
    const publish = vi.fn(async () => {
      expect(identityReady).toBe(true);
    });
    const clear = vi.fn(async () => undefined);
    installMobile({ publish, clear, setCurrentUser });
    getUserMock.mockReturnValue({ id: 7 });
    apiFetchMock.mockImplementation(async (path: string) => {
      expect(identityReady).toBe(true);
      if (String(path).includes('/summary')) return summary;
      return { items: todos };
    });

    hydrateDashboardWidgetFromNetwork();
    await vi.waitFor(() => expect(setCurrentUser).toHaveBeenCalledWith(7));
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(publish).not.toHaveBeenCalled();

    releaseIdentity();
    await vi.waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
    expect(identityReady).toBe(true);
    expect(apiFetchMock).toHaveBeenCalled();
  });

  it('follows Dashboard pagination until the complete ordered list is published', async () => {
    const { publish } = installMobile(undefined);
    getUserMock.mockReturnValue({ id: 7 });
    apiFetchMock.mockImplementation(async (path: string) => {
      const url = String(path);
      if (url.includes('/summary')) return { assignedCount: 3, totalAssignedStoryPoints: 3, wipCount: 1 };
      if (url.includes('sort=board')) throw new Error('unexpected board sort');
      if (!url.includes('cursor=')) {
        return {
          items: [
            { ...todos[0], localId: 1, title: 'A' },
            { ...todos[0], localId: 2, title: 'B' },
          ],
          nextCursor: 'page-2',
        };
      }
      if (url.includes('cursor=page-2')) {
        return { items: [{ ...todos[0], localId: 3, title: 'C' }], nextCursor: null };
      }
      throw new Error(`unexpected todos url ${url}`);
    });

    await publishCompleteDashboardWidgetSnapshot(7);
    expect(publish).toHaveBeenCalledTimes(1);
    expect(publish.mock.calls[0][0].items.map((item: { localId: number }) => item.localId)).toEqual([1, 2, 3]);
    expect(apiFetchMock.mock.calls.map((call) => String(call[0])).filter((url) => url.includes('/todos'))).toEqual([
      '/api/dashboard/todos?limit=20',
      '/api/dashboard/todos?limit=20&cursor=page-2',
    ]);
  });

  it('keeps the previous snapshot when a later Dashboard page fails', async () => {
    const { publish } = installMobile(undefined);
    getUserMock.mockReturnValue({ id: 7 });
    apiFetchMock.mockImplementation(async (path: string) => {
      const url = String(path);
      if (url.includes('/summary')) return summary;
      if (!url.includes('cursor=')) {
        return { items: todos, nextCursor: 'page-2' };
      }
      throw new Error('page two failed');
    });

    await publishCompleteDashboardWidgetSnapshot(7);
    expect(publish).not.toHaveBeenCalled();
  });

  it('preserves board sort across every Dashboard todos page', async () => {
    const { publish } = installMobile(undefined);
    getUserMock.mockReturnValue({ id: 7 });
    getDashboardTodoSortMock.mockReturnValue('board');
    apiFetchMock.mockImplementation(async (path: string) => {
      const url = String(path);
      if (url.includes('/summary')) return summary;
      if (!url.includes('sort=board')) throw new Error(`missing board sort: ${url}`);
      if (!url.includes('cursor=')) return { items: todos, nextCursor: 'b2' };
      return { items: [{ ...todos[0], localId: 8, title: 'Next' }], nextCursor: null };
    });

    await publishCompleteDashboardWidgetSnapshot(7);
    expect(publish).toHaveBeenCalledTimes(1);
    expect(publish.mock.calls[0][0].items).toHaveLength(2);
  });

  it('latest complete refresh wins when an older refresh finishes later', async () => {
    const { publish } = installMobile(undefined);
    getUserMock.mockReturnValue({ id: 7 });
    getDashboardTodoSortMock.mockReturnValue('activity');

    let releaseActivityTodos: () => void = () => undefined;
    const activityTodosGate = new Promise<void>((resolve) => {
      releaseActivityTodos = resolve;
    });
    let activityTodosStarted = false;

    apiFetchMock.mockImplementation(async (path: string) => {
      const url = String(path);
      if (url.includes('/summary')) {
        return { assignedCount: 1, totalAssignedStoryPoints: 1, wipCount: 1 };
      }
      if (url.includes('sort=board')) {
        return { items: [{ ...todos[0], localId: 20, title: 'Board-order' }], nextCursor: null };
      }
      // Activity (default) todos page: hold so refresh A stays in flight.
      activityTodosStarted = true;
      await activityTodosGate;
      return { items: [{ ...todos[0], localId: 10, title: 'Activity-order' }], nextCursor: null };
    });

    const refreshA = publishCompleteDashboardWidgetSnapshot(7);
    await vi.waitFor(() => expect(activityTodosStarted).toBe(true));

    getDashboardTodoSortMock.mockReturnValue('board');
    await publishCompleteDashboardWidgetSnapshot(7);

    expect(publish).toHaveBeenCalledTimes(1);
    expect(publish.mock.calls[0][0].items.map((item: { localId: number; title: string }) => ({
      localId: item.localId,
      title: item.title,
    }))).toEqual([{ localId: 20, title: 'Board-order' }]);

    releaseActivityTodos();
    await refreshA;

    expect(publish).toHaveBeenCalledTimes(1);
    expect(publish.mock.calls[0][0].items.map((item: { localId: number; title: string }) => ({
      localId: item.localId,
      title: item.title,
    }))).toEqual([{ localId: 20, title: 'Board-order' }]);
  });
});
