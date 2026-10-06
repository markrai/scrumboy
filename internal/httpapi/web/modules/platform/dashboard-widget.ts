export const DASHBOARD_WIDGET_CAPABILITY = 'dashboard-widget' as const;

export interface DashboardWidgetSnapshotItem {
  localId: number;
  title: string;
  projectName: string;
  projectSlug: string;
  statusName: string;
  statusColor?: string;
  estimationPoints?: number;
  sprintName?: string;
}

export interface DashboardWidgetSnapshotPayload {
  userId: number;
  fetchedAtMs: number;
  assignedCount: number;
  wipCount: number;
  items: DashboardWidgetSnapshotItem[];
}

export interface DashboardWidgetCapability {
  setCurrentUser(userId: number): Promise<void>;
  publish(snapshot: DashboardWidgetSnapshotPayload): Promise<void>;
  clear(): Promise<void>;
}
