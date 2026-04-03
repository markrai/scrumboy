let _current = {
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
    projectView: (localStorage.getItem("projectView") || "list"),
    user: null,
    projects: null,
    settingsProjectId: null,
    authStatusAvailable: false,
    boardMembers: [],
    dashboardSummary: null,
    dashboardTodos: [],
    dashboardNextCursor: null,
    dashboardLoading: false,
    dashboardTodoSort: (() => {
        try {
            if (typeof localStorage !== 'undefined' && localStorage.getItem('scrumboy.dashboardTodoSort') === 'board') {
                return 'board';
            }
        }
        catch {
            /* ignore */
        }
        return 'activity';
    })(),
    boardLaneMeta: { BACKLOG: { hasMore: false, nextCursor: null, loading: false }, NOT_STARTED: { hasMore: false, nextCursor: null, loading: false }, IN_PROGRESS: { hasMore: false, nextCursor: null, loading: false }, TESTING: { hasMore: false, nextCursor: null, loading: false }, DONE: { hasMore: false, nextCursor: null, loading: false } },
};
// DEPRECATED: Direct access to current is deprecated. Use selectors/mutations instead.
// This export will be removed after circular dependency cleanup in a future phase.
export { _current as current };
