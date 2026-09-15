// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  hydrateDashboardWidgetFromNetwork,
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
});
