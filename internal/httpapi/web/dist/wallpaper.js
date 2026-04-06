// Wallpaper (app shell background) — JSON preference + optional image file on server
import { apiFetch, apiFetchForm } from './api.js';
import { getUser } from './state/selectors.js';
import { HEX_COLOR_RE } from './utils.js';
const WALLPAPER_STORAGE_KEY = 'scrumboy_wallpaper';
export const WALLPAPER_PREF_KEY = 'wallpaper';
const SHELL_ID = 'wallpaperShell';
/** Shipped optional asset; if missing, we show no wallpaper (off). */
export const BUILTIN_DEFAULT_WALLPAPER_URL = '/wallpapers/default.jpg';
let cachedWallpaperJSON = null;
function defaultOff() {
    return { v: 1, mode: 'off' };
}
function defaultBuiltin() {
    return { v: 1, mode: 'builtin' };
}
export function parseWallpaperState(raw) {
    const s = (raw || '').trim();
    if (!s)
        return defaultBuiltin();
    try {
        const o = JSON.parse(s);
        if (!o || typeof o !== 'object')
            return defaultOff();
        const mode = o.mode;
        if (mode === 'builtin') {
            return { v: 1, mode: 'builtin' };
        }
        if (mode === 'color' && o.hex && HEX_COLOR_RE.test(o.hex)) {
            return { v: 1, mode: 'color', hex: o.hex };
        }
        if (mode === 'image' && o.rev != null && Number.isFinite(o.rev) && o.rev > 0) {
            return { v: 1, mode: 'image', rev: Number(o.rev) };
        }
        if (mode === 'off') {
            return defaultOff();
        }
        return defaultOff();
    }
    catch {
        return defaultOff();
    }
}
function serializeWallpaperState(st) {
    return JSON.stringify(st);
}
/** Effective wallpaper state (localStorage cache; server merged via loadUserWallpaper in full mode). */
export function getStoredWallpaperState() {
    if (cachedWallpaperJSON !== null) {
        return parseWallpaperState(cachedWallpaperJSON);
    }
    return parseWallpaperState(localStorage.getItem(WALLPAPER_STORAGE_KEY));
}
function setStoredWallpaperState(st) {
    const json = serializeWallpaperState(st);
    cachedWallpaperJSON = json;
    localStorage.setItem(WALLPAPER_STORAGE_KEY, json);
}
async function saveWallpaperPreferenceToBackend(json) {
    if (!getUser())
        return;
    try {
        await apiFetch('/api/user/preferences', {
            method: 'PUT',
            body: JSON.stringify({ key: WALLPAPER_PREF_KEY, value: json }),
        });
    }
    catch {
        // ignore
    }
}
export async function loadUserWallpaper() {
    if (!getUser())
        return;
    try {
        const resp = await apiFetch(`/api/user/preferences?key=${encodeURIComponent(WALLPAPER_PREF_KEY)}`);
        if (resp && resp.value) {
            const st = parseWallpaperState(resp.value);
            setStoredWallpaperState(st);
            if (st.mode === 'image' && st.rev) {
                const ok = await verifyWallpaperImageOnServer(st.rev);
                if (!ok) {
                    const off = defaultOff();
                    setStoredWallpaperState(off);
                    applyWallpaperState(off);
                    await saveWallpaperPreferenceToBackend(serializeWallpaperState(off));
                    return;
                }
            }
            applyWallpaperState(st);
        }
    }
    catch {
        // ignore
    }
}
async function verifyWallpaperImageOnServer(rev) {
    try {
        const res = await fetch(`/api/user/wallpaper/image?rev=${encodeURIComponent(String(rev))}`, {
            method: 'GET',
            credentials: 'same-origin',
        });
        return res.ok;
    }
    catch {
        return false;
    }
}
/** Clear wallpaper visuals only; does not read or write preferences (anonymous deployment). */
function applyWallpaperVisualOff() {
    const shell = document.getElementById(SHELL_ID);
    if (!shell)
        return;
    const imgEl = shell.querySelector('.wallpaper-shell__image');
    if (!imgEl)
        return;
    document.documentElement.removeAttribute('data-wallpaper-active');
    document.documentElement.removeAttribute('data-wallpaper-source');
    shell.classList.remove('wallpaper-shell--visible');
    imgEl.style.backgroundImage = '';
    imgEl.style.backgroundColor = '';
}
function ensureShell() {
    let el = document.getElementById(SHELL_ID);
    if (el)
        return el;
    el = document.createElement('div');
    el.id = SHELL_ID;
    el.className = 'wallpaper-shell';
    el.setAttribute('aria-hidden', 'true');
    const img = document.createElement('div');
    img.className = 'wallpaper-shell__image';
    const overlay = document.createElement('div');
    overlay.className = 'wallpaper-shell__overlay';
    el.appendChild(img);
    el.appendChild(overlay);
    document.body.prepend(el);
    return el;
}
function applyBuiltinWallpaper(shell, imgEl) {
    const url = BUILTIN_DEFAULT_WALLPAPER_URL;
    const probe = new Image();
    probe.onload = () => {
        document.documentElement.setAttribute('data-wallpaper-active', '');
        shell.classList.add('wallpaper-shell--visible');
        document.documentElement.setAttribute('data-wallpaper-source', 'image');
        imgEl.style.backgroundColor = '';
        imgEl.style.backgroundImage = `url("${url}")`;
        setStoredWallpaperState(defaultBuiltin());
    };
    probe.onerror = () => {
        setStoredWallpaperState(defaultOff());
        applyWallpaperState(defaultOff());
    };
    probe.src = url;
}
export function applyWallpaperState(st) {
    const shell = ensureShell();
    const imgEl = shell.querySelector('.wallpaper-shell__image');
    if (!imgEl)
        return;
    if (st.mode === 'off') {
        document.documentElement.removeAttribute('data-wallpaper-active');
        document.documentElement.removeAttribute('data-wallpaper-source');
        shell.classList.remove('wallpaper-shell--visible');
        imgEl.style.backgroundImage = '';
        imgEl.style.backgroundColor = '';
        return;
    }
    if (st.mode === 'builtin') {
        applyBuiltinWallpaper(shell, imgEl);
        return;
    }
    document.documentElement.setAttribute('data-wallpaper-active', '');
    shell.classList.add('wallpaper-shell--visible');
    if (st.mode === 'color' && st.hex) {
        document.documentElement.setAttribute('data-wallpaper-source', 'color');
        imgEl.style.backgroundImage = '';
        imgEl.style.backgroundColor = st.hex;
        return;
    }
    if (st.mode === 'image' && st.rev) {
        document.documentElement.setAttribute('data-wallpaper-source', 'image');
        imgEl.style.backgroundColor = '';
        const url = `/api/user/wallpaper/image?rev=${encodeURIComponent(String(st.rev))}`;
        imgEl.style.backgroundImage = `url("${url}")`;
        return;
    }
    applyWallpaperState(defaultOff());
}
/** Off — clears server file when logged in via preference PUT + delete path is optional */
export async function setWallpaperOff() {
    const st = defaultOff();
    setStoredWallpaperState(st);
    applyWallpaperState(st);
    await saveWallpaperPreferenceToBackend(serializeWallpaperState(st));
}
export async function setWallpaperColor(hex) {
    const h = hex.trim();
    if (!HEX_COLOR_RE.test(h))
        return;
    const st = { v: 1, mode: 'color', hex: h };
    setStoredWallpaperState(st);
    applyWallpaperState(st);
    await saveWallpaperPreferenceToBackend(serializeWallpaperState(st));
}
export async function uploadWallpaperImage(blob) {
    const form = new FormData();
    form.append('file', blob, 'wallpaper.jpg');
    const out = await apiFetchForm('/api/user/wallpaper/image', form);
    if (!out || typeof out.rev !== 'number') {
        throw new Error('Invalid server response');
    }
    const st = { v: 1, mode: 'image', rev: out.rev };
    setStoredWallpaperState(st);
    applyWallpaperState(st);
}
/**
 * Call once after `/api/auth/status` is known (from the router). Avoids applying full-mode
 * localStorage prefs during anonymous deployment and prevents a flash before auth resolves.
 *
 * - **Full mode:** applies stored preferences (may start builtin image probe).
 * - **Anonymous mode:** hides wallpaper only; does not read or overwrite `localStorage`.
 */
export function applyWallpaperForAuthContext(fullMode) {
    if (!fullMode) {
        applyWallpaperVisualOff();
        return;
    }
    ensureShell();
    applyWallpaperState(getStoredWallpaperState());
}
