import { current } from './state.js';
export function getRoute() {
    return current.route;
}
export function getProjectId() {
    return current.projectId;
}
export function getSlug() {
    return current.slug;
}
export function getBoard() {
    return current.board;
}
export function getTag() {
    return current.tag;
}
export function getSearch() {
    return current.search;
}
/** Sprint filter from URL: null = "All" (omit param), "scheduled" = in-sprint, "unscheduled" = backlog, or numeric string = specific sprint. */
export function getSprintIdFromUrl() {
    const v = typeof window !== "undefined" ? new URL(window.location.href).searchParams.get("sprintId") : null;
    return v === "" ? null : (v || null);
}
export function getOpenTodoSegment() {
    return current.openTodoSegment;
}
export function getEditingTodo() {
    return current.editingTodo;
}
export function getMobileTab() {
    return current.mobileTab;
}
export function getAvailableTags() {
    return current.availableTags;
}
export function getAvailableTagsMap() {
    return current.availableTagsMap;
}
export function getAutocompleteSuggestion() {
    return current.autocompleteSuggestion;
}
export function getTagColors() {
    return current.tagColors;
}
export function getProjectView() {
    return current.projectView;
}
export function getUser() {
    return current.user;
}
export function getProjects() {
    return current.projects;
}
export function getSettingsProjectId() {
    return current.settingsProjectId;
}
export function getAuthStatusAvailable() {
    return current.authStatusAvailable;
}
export function getAuthStatusChecked() {
    return current._authStatusChecked;
}
export function getBootstrapAvailable() {
    return current._bootstrapAvailable;
}
export function getProjectsTab() {
    return current.projectsTab;
}
export function getSettingsActiveTab() {
    return current.settingsActiveTab;
}
export function getBackupImportBtn() {
    return current.backupImportBtn;
}
export function getBackupData() {
    return current.backupData;
}
export function getBackupPreview() {
    return current.backupPreview;
}
export function getBoardMembers() {
    return current.boardMembers ?? [];
}
export function getDashboardSummary() {
    return current.dashboardSummary;
}
export function getDashboardTodos() {
    return current.dashboardTodos ?? [];
}
export function getDashboardNextCursor() {
    return current.dashboardNextCursor ?? null;
}
export function getDashboardLoading() {
    return !!current.dashboardLoading;
}
export function getDashboardTodoSort() {
    return current.dashboardTodoSort === 'board' ? 'board' : 'activity';
}
export function getBoardLaneMeta() {
    return current.boardLaneMeta ?? {
        BACKLOG: { hasMore: false, nextCursor: null, loading: false },
        NOT_STARTED: { hasMore: false, nextCursor: null, loading: false },
        IN_PROGRESS: { hasMore: false, nextCursor: null, loading: false },
        TESTING: { hasMore: false, nextCursor: null, loading: false },
        DONE: { hasMore: false, nextCursor: null, loading: false },
    };
}
/** Display count for a lane: total in lane when paged (totalCount), else columns length. */
export function getLaneDisplayCount(status) {
    const board = current.board;
    const meta = current.boardLaneMeta?.[status];
    if (meta?.totalCount !== undefined && meta.totalCount !== null)
        return meta.totalCount;
    return (board?.columns[status] ?? []).length;
}
