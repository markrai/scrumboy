import { apiFetch } from './api.js';
import {
  mapDashboardWidgetSnapshot,
} from './dashboard-widget-snapshot.js';
import { DASHBOARD_WIDGET_CAPABILITY } from './platform/dashboard-widget.js';
import type { DashboardWidgetSnapshotPayload } from './platform/dashboard-widget.js';
import { getAppRuntime } from './platform/runtime.js';
import { getDashboardTodoSort, getUser } from './state/selectors.js';
import type { DashboardSummary, DashboardTodo, DashboardTodosResponse } from './types.js';

const DASHBOARD_TODOS_PAGE_SIZE = 20;

/** Monotonic epoch for complete widget refreshes; only the latest may publish. */
let completeRefreshGeneration = 0;

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

export async function fetchCompleteDashboardWidgetData(): Promise<{
  summary: DashboardSummary;
  todos: DashboardTodo[];
} | null> {
  const user = getUser();
  if (!user) return null;
  const userId = user.id;
  const sort = getDashboardTodoSort() === 'board' ? 'board' : 'activity';
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  try {
    const [summary, todos] = await Promise.all([
      apiFetch<DashboardSummary>(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
      fetchAllAssignedDashboardTodos(sort, userId),
    ]);
    if (getUser()?.id !== userId) return null;
    return { summary, todos };
  } catch {
    return null;
  }
}

export async function publishCompleteDashboardWidgetSnapshot(userId?: number | null): Promise<void> {
  if (!capability()) return;
  const resolvedUserId = userId ?? getUser()?.id ?? null;
  if (resolvedUserId == null || resolvedUserId <= 0) return;
  const generation = ++completeRefreshGeneration;
  const complete = await fetchCompleteDashboardWidgetData();
  if (!complete) return;
  // Latest-request-wins: a newer complete refresh started while this one was in flight.
  if (generation !== completeRefreshGeneration) return;
  if (getUser()?.id !== resolvedUserId) return;
  await publishDashboardWidgetSnapshot(complete.summary, complete.todos, resolvedUserId);
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
    await publishCompleteDashboardWidgetSnapshot(user.id);
  })();
}

async function fetchAllAssignedDashboardTodos(
  sort: 'activity' | 'board',
  userId: number,
): Promise<DashboardTodo[]> {
  const todos: DashboardTodo[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  for (;;) {
    if (getUser()?.id !== userId) {
      throw new Error('widget_dashboard_user_changed');
    }
    const resp = await apiFetch<DashboardTodosResponse>(`/api/dashboard/todos?${todosQuery(sort, cursor)}`);
    const page = resp.items || [];
    const next = resp.nextCursor ? String(resp.nextCursor) : '';
    if (page.length === 0 && next) {
      throw new Error('widget_dashboard_empty_page');
    }
    todos.push(...page);
    if (!next) return todos;
    if (seenCursors.has(next) || next === cursor) {
      throw new Error('widget_dashboard_cursor_repeat');
    }
    seenCursors.add(next);
    cursor = next;
  }
}

function todosQuery(sort: 'activity' | 'board', cursor?: string): string {
  let q = `limit=${DASHBOARD_TODOS_PAGE_SIZE}`;
  if (sort === 'board') q += '&sort=board';
  if (cursor) q += `&cursor=${encodeURIComponent(cursor)}`;
  return q;
}
