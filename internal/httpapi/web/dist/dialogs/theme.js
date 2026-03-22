// Theme management
const THEME_STORAGE_KEY = 'scrumboy_theme';
const THEME_SYSTEM = 'system';
const THEME_DARK = 'dark';
const THEME_LIGHT = 'light';
let systemThemeListener = null;
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
    const stored = getStoredTheme();
    return stored === THEME_SYSTEM ? getSystemTheme() : stored;
}
function applyTheme(theme) {
    const effective = theme === THEME_SYSTEM ? getSystemTheme() : theme;
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
        systemThemeListener = (e) => {
            applyTheme(THEME_SYSTEM);
        };
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
    }
}
function initTheme() {
    const stored = getStoredTheme();
    applyTheme(stored);
    // Listen for system preference changes (only if theme is 'system')
    if (stored === THEME_SYSTEM) {
        systemThemeListener = (e) => {
            applyTheme(THEME_SYSTEM);
        };
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
    }
}
export { THEME_STORAGE_KEY, THEME_SYSTEM, THEME_DARK, THEME_LIGHT, getStoredTheme, setStoredTheme, getSystemTheme, getEffectiveTheme, applyTheme, handleThemeChange, initTheme };
