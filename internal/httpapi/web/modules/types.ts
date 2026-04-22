// Domain and API types
// Types reflect current runtime reality, not desired future shape

// Union types for string literals
export type TodoStatus = string;
export type ProjectView = 'list' | 'grid';
export type RouteName = 'projects' | 'dashboard' | 'boardBySlug' | 'reset-password' | 'notfound';
export type MobileTab = string;
export type Theme = 'system' | 'dark' | 'light';

// Core domain types
export interface Todo {
  id: number;
  localId: number;
  title: string;
  body?: string;
  status: TodoStatus;
  tags?: string[];
  estimationPoints?: number | null;
  assigneeUserId?: number | null;
  sprintId?: number | null;
  createdAt?: string;
  updatedAt?: string;
}

export interface Tag {
  name: string;
  color?: string;
  count: number;
}

export interface Project {
  id: number;
  name: string;
  image?: string;
  dominantColor: string;
  defaultSprintWeeks?: number;
  estimationMode?: string;
  expiresAt?: string; // ISO date string for temporary boards
  creatorUserId?: number; // NULL for anonymous temp boards
  slug?: string;
  /** Current user's role in this project; present only in list response. Use to gate Delete/Rename. */
  role?: string;
}

export interface Board {
  project: Project;
  tags: Tag[];
  columnOrder?: Array<{ key: string; name: string; color?: string; isDone: boolean; position?: number }>;
  columns: Record<string, Todo[]>;
  columnsMeta?: Record<string, { hasMore: boolean; nextCursor: string | null; totalCount?: number }>;
}

export interface LanePageResponse {
  items: Todo[];
  nextCursor?: string | null;
  hasMore: boolean;
}

export interface User {
  id: number;
  name?: string;
  email?: string;
  image?: string | null;
  isBootstrap?: boolean;
  systemRole?: string;
  twoFactorEnabled?: boolean;
}

export interface ActiveSprintInfo {
  id: number;
  name: string;
  startAt: number;
  endAt: number;
}

export interface SprintSectionInfo {
  id?: number | null;
  name: string;
  state?: string;
  startAt?: number;
  endAt?: number;
}

export interface DashboardProject {
  projectId: number;
  projectName: string;
  projectSlug: string;
  activeSprint: ActiveSprintInfo | null;
  sprintSections?: SprintSectionInfo[];
}

export interface AssignedSplit {
  sprintStories: number;
  sprintPoints: number;
  backlogStories: number;
  backlogPoints: number;
}

export interface SprintCompletion {
  totalStories: number;
  doneStories: number;
  totalPoints: number;
  donePoints: number;
}

export interface WeeklyThroughputPoint {
  weekStart: string;
  stories: number;
  points: number;
}

export interface OldestWip {
  localId: number;
  title: string;
  ageDays: number;
  projectName: string;
  projectSlug: string;
}

export interface DashboardSummary {
  assignedCount: number;
  totalAssignedStoryPoints: number;
  pointsCompletedThisWeek?: number;
  storiesCompletedThisWeek?: number;
  projects?: DashboardProject[];
  assignedSplit?: AssignedSplit | null;
  sprintCompletion?: SprintCompletion | null;
  sprintCompletionAllUsers?: SprintCompletion | null;
  wipCount?: number;
  wipInProgressCount?: number;
  wipTestingCount?: number;
  weeklyThroughput?: WeeklyThroughputPoint[];
  avgLeadTimeDays?: number | null;
  oldestWip?: OldestWip | null;
}

export interface DashboardTodo {
  id: number;
  localId: number;
  title: string;
  projectId: number;
  projectName: string;
  projectSlug: string;
  projectImage?: string;
  projectDominantColor: string;
  estimationPoints?: number | null;
  sprintId?: number | null;
  status: TodoStatus;
  statusName: string;
  statusColor: string;
  updatedAt: string;
}

export interface DashboardTodosResponse {
  items: DashboardTodo[];
  nextCursor?: string;
}

// API-specific response shapes
export interface AuthStatusResponse {
  user?: User | null;
  bootstrapAvailable?: boolean;
  mode?: 'anonymous' | 'full';
  oidcEnabled?: boolean;
  localAuthEnabled?: boolean;
  wallEnabled?: boolean;
}

export interface BoardResponse extends Board {
  // Board response matches Board interface
}

export interface WorkflowLaneDraft {
  key: string;
  name: string;
  color: string;
  position: number;
  isDone: boolean;
}

export interface CreateProjectPayload {
  name: string;
  workflow?: WorkflowLaneDraft[];
}

export interface ProjectsResponse extends Array<Project> {
  // Projects response is an array of Project
}

export interface BackupPreviewResponse {
  projects: number;
  todos: number;
  tags: number;
  links?: number;
  willDelete?: number;
  willUpdate?: number;
  willCreate?: number;
  warnings?: string[];
}

export interface BackupImportResponse {
  // Response shape TBD based on actual API
  // Using unknown for now to avoid assumptions
  [key: string]: unknown;
}

export interface TagResponse extends Array<Tag> {
  // Tag response is an array of Tag
}

export interface BacklogSizePoint {
  date: string;
  incompleteCount?: number;
  totalScope?: number;
  incompletePoints?: number;
  totalScopePoints?: number;
  newTodosCount?: number;
}

export interface RealBurndownPoint {
  date: string;
  remainingWork?: number;
  initialScope: number;
  remainingPoints?: number;
  initialScopePoints?: number;
}
