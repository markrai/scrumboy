let _current = {
    route: null,
    projectId: null,
    slug: null,
    board: null,
    tag: "",
    editingTodo: null,
    mobileTab: "IN_PROGRESS",
    availableTags: [],
    availableTagsMap: {},
    autocompleteSuggestion: null,
    tagColors: {},
    projectView: (localStorage.getItem("projectView") || "list"),
    user: null,
    projects: null,
    settingsProjectId: null,
    authStatusAvailable: false,
};
// DEPRECATED: Direct access to current is deprecated. Use selectors/mutations instead.
// This export will be removed after circular dependency cleanup in a future phase.
export { _current as current };
