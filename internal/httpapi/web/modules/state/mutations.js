"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.setRoute = setRoute;
exports.setProjectId = setProjectId;
exports.setSlug = setSlug;
exports.setBoard = setBoard;
exports.setTag = setTag;
exports.setEditingTodo = setEditingTodo;
exports.setMobileTab = setMobileTab;
exports.setAvailableTags = setAvailableTags;
exports.setAvailableTagsMap = setAvailableTagsMap;
exports.setAutocompleteSuggestion = setAutocompleteSuggestion;
exports.setTagColors = setTagColors;
exports.setProjectView = setProjectView;
exports.setUser = setUser;
exports.setProjects = setProjects;
exports.setSettingsProjectId = setSettingsProjectId;
exports.setAuthStatusAvailable = setAuthStatusAvailable;
exports.setAuthStatusChecked = setAuthStatusChecked;
exports.setBootstrapAvailable = setBootstrapAvailable;
exports.setProjectsTab = setProjectsTab;
exports.setSettingsActiveTab = setSettingsActiveTab;
exports.setBackupImportBtn = setBackupImportBtn;
exports.setBackupData = setBackupData;
exports.setBackupPreview = setBackupPreview;
var state_js_1 = require("./state.js");
var VALID_ROUTES = new Set(['projects', 'boardBySlug', 'notfound']);
var VALID_MOBILE_TABS = new Set(['IN_PROGRESS', 'NOT_STARTED', 'DONE', 'BACKLOG']);
var VALID_PROJECT_VIEWS = new Set(['list', 'grid']);
function setRoute(name) {
    if (!VALID_ROUTES.has(name)) {
        throw new Error("Invalid route: ".concat(name));
    }
    state_js_1.current.route = name;
}
function setProjectId(id) {
    state_js_1.current.projectId = id;
}
function setSlug(slug) {
    state_js_1.current.slug = slug;
}
function setBoard(board) {
    state_js_1.current.board = board;
}
function setTag(tag) {
    state_js_1.current.tag = tag;
}
function setEditingTodo(todo) {
    state_js_1.current.editingTodo = todo;
}
function setMobileTab(tab) {
    if (!VALID_MOBILE_TABS.has(tab)) {
        throw new Error("Invalid mobile tab: ".concat(tab));
    }
    state_js_1.current.mobileTab = tab;
}
function setAvailableTags(tags) {
    state_js_1.current.availableTags = tags;
}
function setAvailableTagsMap(map) {
    state_js_1.current.availableTagsMap = map;
}
function setAutocompleteSuggestion(suggestion) {
    state_js_1.current.autocompleteSuggestion = suggestion;
}
function setTagColors(colors) {
    state_js_1.current.tagColors = colors;
}
function setProjectView(view) {
    if (!VALID_PROJECT_VIEWS.has(view)) {
        throw new Error("Invalid project view: ".concat(view));
    }
    state_js_1.current.projectView = view;
}
function setUser(user) {
    state_js_1.current.user = user;
}
function setProjects(projects) {
    state_js_1.current.projects = projects;
}
function setSettingsProjectId(id) {
    state_js_1.current.settingsProjectId = id;
}
function setAuthStatusAvailable(available) {
    state_js_1.current.authStatusAvailable = available;
}
function setAuthStatusChecked(checked) {
    state_js_1.current._authStatusChecked = checked;
}
function setBootstrapAvailable(available) {
    state_js_1.current._bootstrapAvailable = available;
}
function setProjectsTab(tab) {
    state_js_1.current.projectsTab = tab;
}
function setSettingsActiveTab(tab) {
    state_js_1.current.settingsActiveTab = tab;
}
function setBackupImportBtn(btn) {
    state_js_1.current.backupImportBtn = btn;
}
function setBackupData(data) {
    state_js_1.current.backupData = data;
}
function setBackupPreview(preview) {
    state_js_1.current.backupPreview = preview;
}
