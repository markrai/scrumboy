"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.getRoute = getRoute;
exports.getProjectId = getProjectId;
exports.getSlug = getSlug;
exports.getBoard = getBoard;
exports.getTag = getTag;
exports.getEditingTodo = getEditingTodo;
exports.getMobileTab = getMobileTab;
exports.getAvailableTags = getAvailableTags;
exports.getAvailableTagsMap = getAvailableTagsMap;
exports.getAutocompleteSuggestion = getAutocompleteSuggestion;
exports.getTagColors = getTagColors;
exports.getProjectView = getProjectView;
exports.getUser = getUser;
exports.getProjects = getProjects;
exports.getSettingsProjectId = getSettingsProjectId;
exports.getAuthStatusAvailable = getAuthStatusAvailable;
exports.getAuthStatusChecked = getAuthStatusChecked;
exports.getBootstrapAvailable = getBootstrapAvailable;
exports.getProjectsTab = getProjectsTab;
exports.getSettingsActiveTab = getSettingsActiveTab;
exports.getBackupImportBtn = getBackupImportBtn;
exports.getBackupData = getBackupData;
exports.getBackupPreview = getBackupPreview;
var state_js_1 = require("./state.js");
function getRoute() {
    return state_js_1.current.route;
}
function getProjectId() {
    return state_js_1.current.projectId;
}
function getSlug() {
    return state_js_1.current.slug;
}
function getBoard() {
    return state_js_1.current.board;
}
function getTag() {
    return state_js_1.current.tag;
}
function getEditingTodo() {
    return state_js_1.current.editingTodo;
}
function getMobileTab() {
    return state_js_1.current.mobileTab;
}
function getAvailableTags() {
    return state_js_1.current.availableTags;
}
function getAvailableTagsMap() {
    return state_js_1.current.availableTagsMap;
}
function getAutocompleteSuggestion() {
    return state_js_1.current.autocompleteSuggestion;
}
function getTagColors() {
    return state_js_1.current.tagColors;
}
function getProjectView() {
    return state_js_1.current.projectView;
}
function getUser() {
    return state_js_1.current.user;
}
function getProjects() {
    return state_js_1.current.projects;
}
function getSettingsProjectId() {
    return state_js_1.current.settingsProjectId;
}
function getAuthStatusAvailable() {
    return state_js_1.current.authStatusAvailable;
}
function getAuthStatusChecked() {
    return state_js_1.current._authStatusChecked;
}
function getBootstrapAvailable() {
    return state_js_1.current._bootstrapAvailable;
}
function getProjectsTab() {
    return state_js_1.current.projectsTab;
}
function getSettingsActiveTab() {
    return state_js_1.current.settingsActiveTab;
}
function getBackupImportBtn() {
    return state_js_1.current.backupImportBtn;
}
function getBackupData() {
    return state_js_1.current.backupData;
}
function getBackupPreview() {
    return state_js_1.current.backupPreview;
}
