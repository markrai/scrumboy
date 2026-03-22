// Theme management
import { apiFetch } from './api.js';
import { getUser } from './state/selectors.js';

const THEME_STORAGE_KEY = 'scrumboy_theme';
const THEME_SYSTEM = 'system';
const THEME_DARK = 'dark';
const THEME_LIGHT = 'light';

let systemThemeListener: ((e: MediaQueryListEvent) => void) | null = null;
let cachedTheme: string | null = null;

function getStoredTheme(): string {
  // Use cached theme if available (loaded from backend)
  if (cachedTheme !== null) {
    return cachedTheme;
  }
  // Fall back to localStorage for anonymous users or before backend load
  return localStorage.getItem(THEME_STORAGE_KEY) || THEME_SYSTEM;
}

function setStoredTheme(theme: string): void {
  cachedTheme = theme;
  localStorage.setItem(THEME_STORAGE_KEY, theme);
}

async function saveThemeToBackend(theme: string): Promise<void> {
  if (getUser()) {
    try {
      await apiFetch("/api/user/preferences", {
        method: "PUT",
        body: JSON.stringify({ key: "theme", value: theme }),
      });
    } catch (err) {
      // Ignore errors saving preferences
    }
  }
}

export async function loadUserTheme(): Promise<void> {
  if (getUser()) {
    try {
      const resp = await apiFetch<{ value: string }>("/api/user/preferences?key=theme");
      if (resp && resp.value) {
        cachedTheme = resp.value;
        applyTheme(cachedTheme);
      }
    } catch (err) {
      // Ignore errors loading preferences (might not exist yet)
    }
  }
}

function getSystemTheme(): string {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? THEME_DARK : THEME_LIGHT;
}

function getEffectiveTheme(): string {
  const stored = getStoredTheme();
  return stored === THEME_SYSTEM ? getSystemTheme() : stored;
}

function applyTheme(theme: string): void {
  const effective = theme === THEME_SYSTEM ? getSystemTheme() : theme;
  document.documentElement.setAttribute('data-theme', effective === THEME_LIGHT ? 'light' : '');
  // Remove attribute for dark (default), set 'light' for light mode
}

function handleThemeChange(theme: string): void {
  setStoredTheme(theme);
  applyTheme(theme);
  
  // Save to backend
  saveThemeToBackend(theme);
  
  // Remove old listener if it exists
  if (systemThemeListener) {
    window.matchMedia('(prefers-color-scheme: dark)').removeEventListener('change', systemThemeListener);
    systemThemeListener = null;
  }
  
  // Re-register system preference listener if switching to system mode
  if (theme === THEME_SYSTEM) {
    systemThemeListener = (e: MediaQueryListEvent) => {
      applyTheme(THEME_SYSTEM);
    };
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
  }
}

function initTheme(): void {
  const stored = getStoredTheme();
  applyTheme(stored);
  
  // Listen for system preference changes (only if theme is 'system')
  if (stored === THEME_SYSTEM) {
    systemThemeListener = (e: MediaQueryListEvent) => {
      applyTheme(THEME_SYSTEM);
    };
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', systemThemeListener);
  }
}

export { THEME_STORAGE_KEY, THEME_SYSTEM, THEME_DARK, THEME_LIGHT, getStoredTheme, setStoredTheme, getSystemTheme, getEffectiveTheme, applyTheme, handleThemeChange, initTheme };
