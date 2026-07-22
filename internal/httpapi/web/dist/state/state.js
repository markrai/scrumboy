let _current = {
    route: null,
    projectId: null,
    slug: null,
    board: null,
    tag: "",
    search: "",
    openTodoSegment: null,
    editingTodo: null,
    mobileTab: "backlog",
    availableTags: [],
    availableTagsMap: {},
    autocompleteSuggestion: null,
    tagColors: {},
    projectView: (localStorage.getItem("projectView") || "list"),
    user: null,
    projects: null,
    settingsProjectId: null,
    authStatusAvailable: false,
    emailNotifyPreference: { userId: null, status: 'idle', value: null },
    boardMembers: [],
    trelloImportBtn: null,
    trelloImportData: null,
    trelloImportPreview: null,
    trelloImportResult: null,
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
    boardLaneMeta: {
        backlog: { hasMore: false, nextCursor: null, loading: false },
        not_started: { hasMore: false, nextCursor: null, loading: false },
        doing: { hasMore: false, nextCursor: null, loading: false },
        testing: { hasMore: false, nextCursor: null, loading: false },
        done: { hasMore: false, nextCursor: null, loading: false },
    },
};
// DEPRECATED: Direct access to current is deprecated. Use selectors/mutations instead.
// This export will be removed after circular dependency cleanup in a future phase.
export { _current as current };
