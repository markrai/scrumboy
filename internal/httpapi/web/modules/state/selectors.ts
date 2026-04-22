import { current } from './state.js';
import { Board, Project, Todo, User, ProjectView, MobileTab, RouteName, DashboardSummary, DashboardTodo, TodoStatus } from '../types.js';
import type { BoardMember } from './state.js';

export function getRoute(): RouteName | null {
  return current.route;
}

export function getProjectId(): number | null {
  return current.projectId;
}

export function getSlug(): string | null {
  return current.slug;
}

export function getBoard(): Board | null {
  return current.board;
}

export function getTag(): string {
  return current.tag;
}

export function getSearch(): string {
  return current.search;
}

/** Sprint filter from URL: null = "All" (omit param), "scheduled" = in-sprint, "unscheduled" = backlog, or numeric string = specific sprint. */
export function getSprintIdFromUrl(): string | null {
  const v = typeof window !== "undefined" ? new URL(window.location.href).searchParams.get("sprintId") : null;
  return v === "" ? null : (v || null);
}

export function getOpenTodoSegment(): string | null {
  return current.openTodoSegment;
}

export function getEditingTodo(): Todo | null {
  return current.editingTodo;
}

export function getMobileTab(): MobileTab {
  return current.mobileTab;
}

export function getAvailableTags(): string[] {
  return current.availableTags;
}

export function getAvailableTagsMap(): Record<string, string> {
  return current.availableTagsMap;
}

export function getAutocompleteSuggestion(): string | null {
  return current.autocompleteSuggestion;
}

export function getTagColors(): Record<string, string> {
  return current.tagColors;
}

export function getProjectView(): ProjectView {
  return current.projectView;
}

export function getUser(): User | null {
  return current.user;
}

export function getProjects(): Project[] | null {
  return current.projects;
}

export function getSettingsProjectId(): number | null {
  return current.settingsProjectId;
}

export function getAuthStatusAvailable(): boolean {
  return current.authStatusAvailable;
}

export function getAuthStatusChecked(): boolean | undefined {
  return current._authStatusChecked;
}

export function getBootstrapAvailable(): boolean | undefined {
  return current._bootstrapAvailable;
}

export function getOidcEnabled(): boolean {
  return !!current._oidcEnabled;
}

export function getLocalAuthEnabled(): boolean {
  return current._localAuthEnabled !== false;
}

export function getWallEnabled(): boolean {
  return !!current._wallEnabled;
}

export function getProjectsTab(): string | undefined {
  return current.projectsTab;
}

export function getSettingsActiveTab(): string | undefined {
  return current.settingsActiveTab;
}

export function getBackupImportBtn(): HTMLElement | null | undefined {
  return current.backupImportBtn;
}

export function getBackupData(): unknown {
  return current.backupData;
}

export function getBackupPreview(): unknown {
  return current.backupPreview;
}

export function getBoardMembers(): BoardMember[] {
  return current.boardMembers ?? [];
}

export function getDashboardSummary(): DashboardSummary | null {
  return current.dashboardSummary;
}

export function getDashboardTodos(): DashboardTodo[] {
  return current.dashboardTodos ?? [];
}

export function getDashboardNextCursor(): string | null {
  return current.dashboardNextCursor ?? null;
}

export function getDashboardLoading(): boolean {
  return !!current.dashboardLoading;
}

export function getDashboardTodoSort(): 'activity' | 'board' {
  return current.dashboardTodoSort === 'board' ? 'board' : 'activity';
}

export function getBoardLaneMeta(): Record<TodoStatus, { hasMore: boolean; nextCursor: string | null; loading: boolean; totalCount?: number }> {
  return current.boardLaneMeta ?? {
    backlog: { hasMore: false, nextCursor: null, loading: false },
    not_started: { hasMore: false, nextCursor: null, loading: false },
    doing: { hasMore: false, nextCursor: null, loading: false },
    testing: { hasMore: false, nextCursor: null, loading: false },
    done: { hasMore: false, nextCursor: null, loading: false },
  };
}

/** Display count for a lane: total in lane when paged (totalCount), else columns length. */
export function getLaneDisplayCount(status: TodoStatus): number {
  const board = current.board;
  const meta = current.boardLaneMeta?.[status];
  if (meta?.totalCount !== undefined && meta.totalCount !== null) return meta.totalCount;
  return (board?.columns[status] ?? []).length;
}
