import { current } from './state.js';
const VALID_ROUTES = new Set(['projects', 'boardBySlug', 'notfound']);
const VALID_MOBILE_TABS = new Set(['IN_PROGRESS', 'NOT_STARTED', 'DONE', 'BACKLOG']);
const VALID_PROJECT_VIEWS = new Set(['list', 'grid']);
export function setRoute(name) {
    if (!VALID_ROUTES.has(name)) {
        throw new Error(`Invalid route: ${name}`);
    }
    current.route = name;
}
export function setProjectId(id) {
    current.projectId = id;
}
export function setSlug(slug) {
    current.slug = slug;
}
export function setBoard(board) {
    current.board = board;
}
export function setTag(tag) {
    current.tag = tag;
}
export function setEditingTodo(todo) {
    current.editingTodo = todo;
}
export function setMobileTab(tab) {
    if (!VALID_MOBILE_TABS.has(tab)) {
        throw new Error(`Invalid mobile tab: ${tab}`);
    }
    current.mobileTab = tab;
}
export function setAvailableTags(tags) {
    current.availableTags = tags;
}
export function setAvailableTagsMap(map) {
    current.availableTagsMap = map;
}
export function setAutocompleteSuggestion(suggestion) {
    current.autocompleteSuggestion = suggestion;
}
export function setTagColors(colors) {
    current.tagColors = colors;
}
export function setProjectView(view) {
    if (!VALID_PROJECT_VIEWS.has(view)) {
        throw new Error(`Invalid project view: ${view}`);
    }
    current.projectView = view;
}
export function setUser(user) {
    current.user = user;
}
export function setProjects(projects) {
    current.projects = projects;
}
export function setSettingsProjectId(id) {
    current.settingsProjectId = id;
}
export function setAuthStatusAvailable(available) {
    current.authStatusAvailable = available;
}
export function setAuthStatusChecked(checked) {
    current._authStatusChecked = checked;
}
export function setBootstrapAvailable(available) {
    current._bootstrapAvailable = available;
}
export function setProjectsTab(tab) {
    current.projectsTab = tab;
}
export function setSettingsActiveTab(tab) {
    current.settingsActiveTab = tab;
}
export function setBackupImportBtn(btn) {
    current.backupImportBtn = btn;
}
export function setBackupData(data) {
    current.backupData = data;
}
export function setBackupPreview(preview) {
    current.backupPreview = preview;
}
