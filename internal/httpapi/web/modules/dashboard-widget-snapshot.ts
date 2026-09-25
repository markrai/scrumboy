import type { DashboardSummary, DashboardTodo } from './types.js';
import type { DashboardWidgetSnapshotItem, DashboardWidgetSnapshotPayload } from './platform/dashboard-widget.js';

export const DASHBOARD_WIDGET_SCHEMA_VERSION = 1;
export const DASHBOARD_WIDGET_MAX_TITLE_CHARS = 120;
export const DASHBOARD_WIDGET_MAX_NAME_CHARS = 80;
const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;

export type { DashboardWidgetSnapshotItem, DashboardWidgetSnapshotPayload };

export interface DashboardWidgetSnapshotInput {
  userId: number;
  fetchedAtMs: number;
  summary: DashboardSummary;
  todos: DashboardTodo[];
}

export function mapDashboardWidgetSnapshot(input: DashboardWidgetSnapshotInput): DashboardWidgetSnapshotPayload | null {
  if (!Number.isFinite(input.userId) || input.userId <= 0 || !Number.isFinite(input.fetchedAtMs) || input.fetchedAtMs <= 0) {
    return null;
  }
  const items: DashboardWidgetSnapshotItem[] = [];
  for (const todo of input.todos) {
    const item = mapItem(todo, input.summary);
    if (item) items.push(item);
  }
  return {
    userId: input.userId,
    fetchedAtMs: input.fetchedAtMs,
    assignedCount: Math.max(0, input.summary.assignedCount || 0),
    wipCount: Math.max(0, input.summary.wipCount ?? 0),
    items,
  };
}

function mapItem(todo: DashboardTodo, summary: DashboardSummary): DashboardWidgetSnapshotItem | null {
  const localId = todo.localId;
  const title = clip(todo.title, DASHBOARD_WIDGET_MAX_TITLE_CHARS);
  const projectName = clip(todo.projectName, DASHBOARD_WIDGET_MAX_NAME_CHARS);
  const projectSlug = clip(todo.projectSlug, DASHBOARD_WIDGET_MAX_NAME_CHARS);
  const statusName = clip(todo.statusName, DASHBOARD_WIDGET_MAX_NAME_CHARS);
  if (!Number.isFinite(localId) || localId <= 0 || !title || !projectName || !projectSlug || !statusName) {
    return null;
  }
  const item: DashboardWidgetSnapshotItem = {
    localId,
    title,
    projectName,
    projectSlug,
    statusName,
  };
  const statusColor = sanitizeHex(todo.statusColor);
  if (statusColor) item.statusColor = statusColor;
  if (todo.estimationPoints != null && Number.isFinite(todo.estimationPoints) && todo.estimationPoints >= 0) {
    item.estimationPoints = todo.estimationPoints;
  }
  const sprintName = clip(sprintNameForTodo(todo, summary), DASHBOARD_WIDGET_MAX_NAME_CHARS);
  if (sprintName) item.sprintName = sprintName;
  return item;
}

function sprintNameForTodo(todo: DashboardTodo, summary: DashboardSummary): string {
  if (todo.sprintId == null) return '';
  const project = (summary.projects ?? []).find((entry) => entry.projectId === todo.projectId);
  const section = (project?.sprintSections ?? []).find((entry) => entry.id === todo.sprintId);
  return section?.name || '';
}

function sanitizeHex(color: string | undefined): string | undefined {
  if (!color || !HEX_COLOR_RE.test(color.trim())) return undefined;
  return color.trim();
}

function clip(value: string | undefined | null, maxChars: number): string {
  if (!value) return '';
  const trimmed = String(value).trim();
  return trimmed.length <= maxChars ? trimmed : trimmed.slice(0, maxChars);
}
