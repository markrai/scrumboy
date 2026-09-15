import { apiFetch } from './api.js';
import {
  mapDashboardWidgetSnapshot,
} from './dashboard-widget-snapshot.js';
import { DASHBOARD_WIDGET_CAPABILITY } from './platform/dashboard-widget.js';
import type { DashboardWidgetSnapshotPayload } from './platform/dashboard-widget.js';
import { getAppRuntime } from './platform/runtime.js';
import { getDashboardTodoSort, getUser } from './state/selectors.js';
import type { DashboardSummary, DashboardTodo, DashboardTodosResponse } from './types.js';

function capability() {
  return getAppRuntime().capability(DASHBOARD_WIDGET_CAPABILITY);
}

export async function setDashboardWidgetCurrentUser(userId: number | null): Promise<void> {
  const cap = capability();
  if (!cap) return;
  try {
    if (userId == null || userId <= 0) {
      await cap.clear();
      return;
    }
    await cap.setCurrentUser(userId);
  } catch {
    // Widget identity must not block authentication.
  }
}

export async function publishDashboardWidgetSnapshot(
  summary: DashboardSummary,
  todos: DashboardTodo[],
  userId?: number | null,
): Promise<void> {
  const cap = capability();
  if (!cap) return;
  const resolvedUserId = userId ?? getUser()?.id ?? null;
  if (resolvedUserId == null || resolvedUserId <= 0) return;
  const payload: DashboardWidgetSnapshotPayload | null = mapDashboardWidgetSnapshot({
    userId: resolvedUserId,
    fetchedAtMs: Date.now(),
    summary,
    todos,
  });
  if (!payload) return;
  try {
    await cap.publish(payload);
  } catch {
    // Widget publication must not fail the in-app Dashboard.
  }
}

export function hydrateDashboardWidgetFromNetwork(options?: { skipIfDashboardRoute?: boolean }): void {
  const cap = capability();
  if (!cap) return;
  const user = getUser();
  if (!user) {
    void setDashboardWidgetCurrentUser(null);
    return;
  }
  void (async () => {
    await setDashboardWidgetCurrentUser(user.id);
    if (options?.skipIfDashboardRoute && typeof window !== 'undefined' && window.location.pathname === '/dashboard') {
      return;
    }
    await fetchAndPublishDashboardWidget();
  })();
}

async function fetchAndPublishDashboardWidget(): Promise<void> {
  const user = getUser();
  if (!user) return;
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    let todosQuery = 'limit=20';
    if (getDashboardTodoSort() === 'board') todosQuery += '&sort=board';
    const [summary, todosResp] = await Promise.all([
      apiFetch<DashboardSummary>(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
      apiFetch<DashboardTodosResponse>(`/api/dashboard/todos?${todosQuery}`),
    ]);
    if (getUser()?.id !== user.id) return;
    await publishDashboardWidgetSnapshot(summary, todosResp.items || [], user.id);
  } catch {
    // Best-effort first population; Dashboard visit remains the authoritative refresh.
  }
}
