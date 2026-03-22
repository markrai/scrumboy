import { current } from './state.js';
export function getRoute() {
    return current.route;
}
export function getProjectId() {
    return current.projectId;
}
export function getSlug() {
    return current.slug;
}
export function getBoard() {
    return current.board;
}
export function getTag() {
    return current.tag;
}
export function getEditingTodo() {
    return current.editingTodo;
}
export function getMobileTab() {
    return current.mobileTab;
}
export function getAvailableTags() {
    return current.availableTags;
}
export function getAvailableTagsMap() {
    return current.availableTagsMap;
}
export function getAutocompleteSuggestion() {
    return current.autocompleteSuggestion;
}
export function getTagColors() {
    return current.tagColors;
}
export function getProjectView() {
    return current.projectView;
}
export function getUser() {
    return current.user;
}
export function getProjects() {
    return current.projects;
}
export function getSettingsProjectId() {
    return current.settingsProjectId;
}
export function getAuthStatusAvailable() {
    return current.authStatusAvailable;
}
export function getAuthStatusChecked() {
    return current._authStatusChecked;
}
export function getBootstrapAvailable() {
    return current._bootstrapAvailable;
}
export function getProjectsTab() {
    return current.projectsTab;
}
export function getSettingsActiveTab() {
    return current.settingsActiveTab;
}
export function getBackupImportBtn() {
    return current.backupImportBtn;
}
export function getBackupData() {
    return current.backupData;
}
export function getBackupPreview() {
    return current.backupPreview;
}
