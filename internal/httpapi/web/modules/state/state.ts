import { Board, Project, Todo, User, ProjectView, MobileTab, RouteName, DashboardSummary, DashboardTodo, TodoStatus } from '../types.js';

export interface BoardMember {
  userId: number;
  name: string;
  email: string;
  image?: string;
  role: string;
}

export interface State {
  route: RouteName | null;
  projectId: number | null;
  slug: string | null;
  board: Board | null;
  tag: string;
  search: string;
  openTodoSegment: string | null;
  editingTodo: Todo | null;
  mobileTab: MobileTab;
  availableTags: string[];
  availableTagsMap: Record<string, string>;
  autocompleteSuggestion: string | null;
  tagColors: Record<string, string>;
  projectView: ProjectView;
  user: User | null;
  projects: Project[] | null;
  settingsProjectId: number | null;
  authStatusAvailable: boolean;
  // Internal properties
  _authStatusChecked?: boolean;
  _bootstrapAvailable?: boolean;
  projectsTab?: string;
  settingsActiveTab?: string;
  // DOM objects require "lib": ["DOM"] in tsconfig.json
  backupImportBtn?: HTMLElement | null;
  backupData?: unknown;
  backupPreview?: unknown;
  boardMembers: BoardMember[];
  dashboardSummary: DashboardSummary | null;
  dashboardTodos: DashboardTodo[];
  dashboardNextCursor: string | null;
  dashboardLoading: boolean;
  /** Assigned-todo ordering on the dashboard: activity (updated_at) or board (per project lane + rank). */
  dashboardTodoSort: 'activity' | 'board';
  boardLaneMeta: Record<TodoStatus, { hasMore: boolean; nextCursor: string | null; loading: boolean; totalCount?: number }>;
}


let _current: State = {
  route: null,
  projectId: null,
  slug: null,
  board: null,
  tag: "",
  search: "",
  openTodoSegment: null,
  editingTodo: null,
  mobileTab: "BACKLOG",
  availableTags: [],
  availableTagsMap: {},
  autocompleteSuggestion: null,
  tagColors: {},
  projectView: (localStorage.getItem("projectView") || "list") as ProjectView,
  user: null,
  projects: null,
  settingsProjectId: null,
  authStatusAvailable: false,
  boardMembers: [],
  dashboardSummary: null,
  dashboardTodos: [],
  dashboardNextCursor: null,
  dashboardLoading: false,
  dashboardTodoSort: ((): 'activity' | 'board' => {
    try {
      if (typeof localStorage !== 'undefined' && localStorage.getItem('scrumboy.dashboardTodoSort') === 'board') {
        return 'board';
      }
    } catch {
      /* ignore */
    }
    return 'activity';
  })(),
  boardLaneMeta: { BACKLOG: { hasMore: false, nextCursor: null, loading: false }, NOT_STARTED: { hasMore: false, nextCursor: null, loading: false }, IN_PROGRESS: { hasMore: false, nextCursor: null, loading: false }, TESTING: { hasMore: false, nextCursor: null, loading: false }, DONE: { hasMore: false, nextCursor: null, loading: false } },
};

// DEPRECATED: Direct access to current is deprecated. Use selectors/mutations instead.
// This export will be removed after circular dependency cleanup in a future phase.
export { _current as current };
