"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.current = void 0;
var _current = {
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
exports.current = _current;
