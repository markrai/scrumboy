import { apiFetch } from '../api.js';
import { current } from './state.js';
import { getUser } from './selectors.js';
import { Board, Project, Todo, User, ProjectView, MobileTab, RouteName, DashboardSummary, DashboardTodo, TodoStatus } from '../types.js';
import type { BoardMember } from './state.js';

const DEFAULT_LANE_META = (): Record<TodoStatus, { hasMore: boolean; nextCursor: string | null; loading: boolean; totalCount?: number }> => ({});

/** True after the user changes dashboard sort (not server hydrate). Skips applying stored preference so a fast local change is not overwritten when the GET returns. */
let dashboardTodoSortUserTouched = false;

const VALID_ROUTES = new Set<RouteName>(['projects', 'dashboard', 'boardBySlug', 'reset-password', 'notfound']);
const VALID_PROJECT_VIEWS = new Set<ProjectView>(['list', 'grid']);

export function setRoute(name: RouteName): void {
  if (!VALID_ROUTES.has(name)) {
    throw new Error(`Invalid route: ${name}`);
  }
  current.route = name;
}

export function setProjectId(id: number | null): void {
  current.projectId = id;
}

export function setSlug(slug: string | null): void {
  current.slug = slug;
}

export function setBoard(board: Board | null): void {
  current.board = board;
}

export function setTag(tag: string): void {
  current.tag = tag;
}

export function setSearch(search: string): void {
  current.search = search;
}

export function setOpenTodoSegment(segment: string | null): void {
  current.openTodoSegment = segment;
}

export function setEditingTodo(todo: Todo | null): void {
  current.editingTodo = todo;
}

export function setMobileTab(tab: MobileTab): void {
  current.mobileTab = tab;
}

export function setAvailableTags(tags: string[]): void {
  current.availableTags = tags;
}

export function setAvailableTagsMap(map: Record<string, string>): void {
  current.availableTagsMap = map;
}

export function setAutocompleteSuggestion(suggestion: string | null): void {
  current.autocompleteSuggestion = suggestion;
}

export function setTagColors(colors: Record<string, string>): void {
  current.tagColors = colors;
}

export function setProjectView(view: ProjectView): void {
  if (!VALID_PROJECT_VIEWS.has(view)) {
    throw new Error(`Invalid project view: ${view}`);
  }
  current.projectView = view;
}

export function setUser(user: User | null): void {
  current.user = user;
}

export function setProjects(projects: Project[] | null): void {
  current.projects = projects;
}

export function setSettingsProjectId(id: number | null): void {
  current.settingsProjectId = id;
}

export function setAuthStatusAvailable(available: boolean): void {
  current.authStatusAvailable = available;
}

export function setAuthStatusChecked(checked: boolean | undefined): void {
  current._authStatusChecked = checked;
}

export function setBootstrapAvailable(available: boolean | undefined): void {
  current._bootstrapAvailable = available;
}

export function setOidcEnabled(enabled: boolean): void {
  current._oidcEnabled = enabled;
}

export function setLocalAuthEnabled(enabled: boolean): void {
  current._localAuthEnabled = enabled;
}

export function setWallEnabled(enabled: boolean): void {
  current._wallEnabled = enabled;
}

export function setProjectsTab(tab: string | undefined): void {
  current.projectsTab = tab;
}

export function setSettingsActiveTab(tab: string | undefined): void {
  current.settingsActiveTab = tab;
}

export function setBackupImportBtn(btn: HTMLElement | null | undefined): void {
  current.backupImportBtn = btn;
}

export function setBackupData(data: unknown): void {
  current.backupData = data;
}

export function setBackupPreview(preview: unknown): void {
  current.backupPreview = preview;
}

export function setBoardMembers(members: BoardMember[]): void {
  current.boardMembers = members;
}

export function setBoardLaneMeta(meta: Record<TodoStatus, { hasMore: boolean; nextCursor: string | null; loading: boolean; totalCount?: number }>): void {
  current.boardLaneMeta = meta;
}

export function setLaneLoading(status: TodoStatus, loading: boolean): void {
  current.boardLaneMeta = { ...current.boardLaneMeta, [status]: { ...current.boardLaneMeta[status], loading } };
}

export function appendLaneTodos(status: TodoStatus, items: Todo[], nextCursor: string | null, hasMore: boolean): void {
  const board = current.board;
  if (!board) return;
  const existing = board.columns[status] || [];
  current.board = {
    ...board,
    columns: { ...board.columns, [status]: [...existing, ...items] },
  };
  const prev = current.boardLaneMeta[status];
  current.boardLaneMeta = {
    ...current.boardLaneMeta,
    [status]: { hasMore, nextCursor, loading: false, totalCount: prev?.totalCount },
  };
}

export function setDashboardSummary(summary: DashboardSummary | null): void {
  current.dashboardSummary = summary;
}

export function setDashboardTodos(todos: DashboardTodo[]): void {
  current.dashboardTodos = todos;
}

export function appendDashboardTodos(todos: DashboardTodo[]): void {
  current.dashboardTodos = [...(current.dashboardTodos ?? []), ...todos];
}

export function setDashboardNextCursor(cursor: string | null): void {
  current.dashboardNextCursor = cursor;
}

export function setDashboardLoading(loading: boolean): void {
  current.dashboardLoading = loading;
}

export function setDashboardTodoSort(
  sort: 'activity' | 'board',
  opts?: { skipRemote?: boolean },
): void {
  const next = sort === 'board' ? 'board' : 'activity';
  if (current.dashboardTodoSort === next) {
    return;
  }
  if (!opts?.skipRemote) {
    dashboardTodoSortUserTouched = true;
  }
  current.dashboardTodoSort = next;
  try {
    localStorage.setItem('scrumboy.dashboardTodoSort', next);
  } catch {
    /* ignore */
  }
  if (opts?.skipRemote || !getUser()) {
    return;
  }
  void apiFetch('/api/user/preferences', {
    method: 'PUT',
    body: JSON.stringify({ key: 'dashboardTodoSort', value: next }),
  }).catch(() => {
    /* ignore */
  });
}

/** Apply stored preference after login only if the user has not already changed sort this session. */
export function hydrateDashboardTodoSortFromServer(sort: 'activity' | 'board'): void {
  if (dashboardTodoSortUserTouched) {
    return;
  }
  setDashboardTodoSort(sort, { skipRemote: true });
}

export function resetDashboard(): void {
  current.dashboardSummary = null;
  current.dashboardTodos = [];
  current.dashboardNextCursor = null;
  current.dashboardLoading = false;
}

export function resetUserScopedState(): void {
  // Clear user-scoped data when user changes (e.g., after logout/login)
  // Keep global fields (route, slug, tag, search, mobileTab) and user field (updated by router)
  current.projects = null;
  current.board = null;
  current.projectId = null;
  current.editingTodo = null;
  current.availableTags = [];
  current.availableTagsMap = {};
  current.autocompleteSuggestion = null;
  current.openTodoSegment = null;
  current.settingsProjectId = null;
  current.tagColors = {};
  current.backupData = undefined;
  current.backupPreview = undefined;
  current.backupImportBtn = undefined;
  current.boardMembers = [];
  current.dashboardSummary = null;
  current.dashboardTodos = [];
  current.dashboardNextCursor = null;
  current.dashboardLoading = false;
  current.boardLaneMeta = DEFAULT_LANE_META();
  dashboardTodoSortUserTouched = false;
}
