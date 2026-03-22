"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.THEME_LIGHT = exports.THEME_DARK = exports.THEME_SYSTEM = exports.THEME_STORAGE_KEY = void 0;
exports.getStoredTheme = getStoredTheme;
exports.setStoredTheme = setStoredTheme;
exports.getSystemTheme = getSystemTheme;
exports.getEffectiveTheme = getEffectiveTheme;
exports.applyTheme = applyTheme;
exports.handleThemeChange = handleThemeChange;
exports.initTheme = initTheme;
// Theme management
var THEME_STORAGE_KEY = 'scrumboy_theme';
exports.THEME_STORAGE_KEY = THEME_STORAGE_KEY;
var THEME_SYSTEM = 'system';
exports.THEME_SYSTEM = THEME_SYSTEM;
var THEME_DARK = 'dark';
exports.THEME_DARK = THEME_DARK;
var THEME_LIGHT = 'light';
exports.THEME_LIGHT = THEME_LIGHT;
var systemThemeListener = null;
function getStoredTheme() {
    return localStorage.getItem(THEME_STORAGE_KEY) || THEME_SYSTEM;
}
function setStoredTheme(theme) {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
}
function getSystemTheme() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? THEME_DARK : THEME_LIGHT;
}
function getEffectiveTheme() {
    var stored = getStoredTheme();
    return stored === THEME_SYSTEM ? getSystemTheme() : stored;
}
function applyTheme(theme) {
    var effective = theme === THEME_SYSTEM ? getSystemTheme() : theme;
    document.documentElement.setAttribute('data-theme', effective === THEME_LIGHT ? 'light' : '');
    // Remove attribute for dark (default), set 'light' for light mode
}
function handleThemeChange(theme) {
    setStoredTheme(theme);
    applyTheme(theme);
    // Remove old listener if it exists
    if (systemThemeListener) {
        window.matchMedia('(prefers-color-scheme: dark)').removeEventListener('change', systemThemeListener);
        systemThemeListener = null;
    }
    // Re-register system preference listener if switching to system mode
    if (theme === THEME_SYSTEM) {
        systemThemeListener = function (e) {
            applyTheme(THEME_SYSTEM);
        };
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
    }
}
function initTheme() {
    var stored = getStoredTheme();
    applyTheme(stored);
    // Listen for system preference changes (only if theme is 'system')
    if (stored === THEME_SYSTEM) {
        systemThemeListener = function (e) {
            applyTheme(THEME_SYSTEM);
        };
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
    }
}
