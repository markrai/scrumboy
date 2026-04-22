import { apiFetch } from '../api.js';
import { current } from './state.js';
import { getUser } from './selectors.js';
const DEFAULT_LANE_META = () => ({});
/** True after the user changes dashboard sort (not server hydrate). Skips applying stored preference so a fast local change is not overwritten when the GET returns. */
let dashboardTodoSortUserTouched = false;
const VALID_ROUTES = new Set(['projects', 'dashboard', 'boardBySlug', 'reset-password', 'notfound']);
const VALID_PROJECT_VIEWS = new Set(['list', 'grid']);
export function setRoute(name) {
    if (!VALID_ROUTES.has(name)) {
        throw new Error(`Invalid route: ${name}`);
    }
    current.route = name;
}
export function setProjectId(id) {
    current.projectId = id;
}
export function setSlug(slug) {
    current.slug = slug;
}
export function setBoard(board) {
    current.board = board;
}
export function setTag(tag) {
    current.tag = tag;
}
export function setSearch(search) {
    current.search = search;
}
export function setOpenTodoSegment(segment) {
    current.openTodoSegment = segment;
}
export function setEditingTodo(todo) {
    current.editingTodo = todo;
}
export function setMobileTab(tab) {
    current.mobileTab = tab;
}
export function setAvailableTags(tags) {
    current.availableTags = tags;
}
export function setAvailableTagsMap(map) {
    current.availableTagsMap = map;
}
export function setAutocompleteSuggestion(suggestion) {
    current.autocompleteSuggestion = suggestion;
}
export function setTagColors(colors) {
    current.tagColors = colors;
}
export function setProjectView(view) {
    if (!VALID_PROJECT_VIEWS.has(view)) {
        throw new Error(`Invalid project view: ${view}`);
    }
    current.projectView = view;
}
export function setUser(user) {
    current.user = user;
}
export function setProjects(projects) {
    current.projects = projects;
}
export function setSettingsProjectId(id) {
    current.settingsProjectId = id;
}
export function setAuthStatusAvailable(available) {
    current.authStatusAvailable = available;
}
export function setAuthStatusChecked(checked) {
    current._authStatusChecked = checked;
}
export function setBootstrapAvailable(available) {
    current._bootstrapAvailable = available;
}
export function setOidcEnabled(enabled) {
    current._oidcEnabled = enabled;
}
export function setLocalAuthEnabled(enabled) {
    current._localAuthEnabled = enabled;
}
export function setWallEnabled(enabled) {
    current._wallEnabled = enabled;
}
export function setProjectsTab(tab) {
    current.projectsTab = tab;
}
export function setSettingsActiveTab(tab) {
    current.settingsActiveTab = tab;
}
export function setBackupImportBtn(btn) {
    current.backupImportBtn = btn;
}
export function setBackupData(data) {
    current.backupData = data;
}
export function setBackupPreview(preview) {
    current.backupPreview = preview;
}
export function setBoardMembers(members) {
    current.boardMembers = members;
}
export function setBoardLaneMeta(meta) {
    current.boardLaneMeta = meta;
}
export function setLaneLoading(status, loading) {
    current.boardLaneMeta = { ...current.boardLaneMeta, [status]: { ...current.boardLaneMeta[status], loading } };
}
export function appendLaneTodos(status, items, nextCursor, hasMore) {
    const board = current.board;
    if (!board)
        return;
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
export function setDashboardSummary(summary) {
    current.dashboardSummary = summary;
}
export function setDashboardTodos(todos) {
    current.dashboardTodos = todos;
}
export function appendDashboardTodos(todos) {
    current.dashboardTodos = [...(current.dashboardTodos ?? []), ...todos];
}
export function setDashboardNextCursor(cursor) {
    current.dashboardNextCursor = cursor;
}
export function setDashboardLoading(loading) {
    current.dashboardLoading = loading;
}
export function setDashboardTodoSort(sort, opts) {
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
    }
    catch {
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
export function hydrateDashboardTodoSortFromServer(sort) {
    if (dashboardTodoSortUserTouched) {
        return;
    }
    setDashboardTodoSort(sort, { skipRemote: true });
}
export function resetDashboard() {
    current.dashboardSummary = null;
    current.dashboardTodos = [];
    current.dashboardNextCursor = null;
    current.dashboardLoading = false;
}
export function resetUserScopedState() {
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
