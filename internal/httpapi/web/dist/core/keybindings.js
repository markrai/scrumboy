/**
 * Centralized keyboard shortcuts: single document listener, context-aware,
 * persisted overrides, capture mode for settings UI.
 */
import { apiFetch } from "../api.js";
import { navigate } from "../router.js";
import { showToast } from "../utils.js";
import { settingsDialog, todoDialog } from "../dom/elements.js";
import { setProjectsTab } from "../state/mutations.js";
import { getAuthStatusAvailable, getBoard, getProjectsTab, getRoute, getUser } from "../state/selectors.js";
export const KEYBINDINGS_STORAGE_KEY = "scrumboy.keybindings";
const DASHBOARD_PROJECT_IDS = [
    "dashboardProject1",
    "dashboardProject2",
    "dashboardProject3",
    "dashboardProject4",
    "dashboardProject5",
    "dashboardProject6",
    "dashboardProject7",
    "dashboardProject8",
    "dashboardProject9",
];
const PROJECTS_LIST_IDS = [
    "projectsList1",
    "projectsList2",
    "projectsList3",
    "projectsList4",
    "projectsList5",
    "projectsList6",
    "projectsList7",
    "projectsList8",
    "projectsList9",
];
/** Default canonical chord per action (lowercase modifiers, code-based letters/digits). */
export const DEFAULT_KEY_CHORDS = {
    newTodo: "n",
    boardSearch: "s",
    openSettings: "shift+s",
    createProject: "n",
    boardEscapeBack: "escape",
    dashboardProject1: "1",
    dashboardProject2: "2",
    dashboardProject3: "3",
    dashboardProject4: "4",
    dashboardProject5: "5",
    dashboardProject6: "6",
    dashboardProject7: "7",
    dashboardProject8: "8",
    dashboardProject9: "9",
    projectsList1: "1",
    projectsList2: "2",
    projectsList3: "3",
    projectsList4: "4",
    projectsList5: "5",
    projectsList6: "6",
    projectsList7: "7",
    projectsList8: "8",
    projectsList9: "9",
    cycleMainNavTabs: "tab",
};
export const KEY_ACTION_LIST = [
    { id: "newTodo", label: "New Todo", contexts: ["board"] },
    { id: "boardSearch", label: "Search todos", contexts: ["board"] },
    { id: "openSettings", label: "Open Settings", contexts: ["board", "dashboard", "projects", "unknown"] },
    {
        id: "cycleMainNavTabs",
        label: "Cycle Dashboard / Projects / Temporary",
        contexts: ["dashboard", "projects"],
    },
    { id: "createProject", label: "Create project", contexts: ["projects"] },
    { id: "boardEscapeBack", label: "Back to projects (Esc)", contexts: ["board"] },
    ...DASHBOARD_PROJECT_IDS.map((id, i) => ({
        id,
        label: `Jump to project ${i + 1} (dashboard)`,
        contexts: ["dashboard"],
    })),
    ...PROJECTS_LIST_IDS.map((id, i) => ({
        id,
        label: `Open project ${i + 1} (projects list)`,
        contexts: ["projects"],
    })),
];
let deps = null;
let listenerAttached = false;
let captureListening = false;
/**
 * When true, the single global keydown handler in this module returns immediately
 * (no chord match, no preventDefault). Settings UI must set this while recording a shortcut.
 */
export function setKeybindingsCaptureListening(active) {
    captureListening = active;
}
export function isKeybindingsCaptureListening() {
    return captureListening;
}
/**
 * When true, the global shortcut handler must not run any matching or side effects.
 * Settings → Customization sets `captureListening` while recording; `onGlobalKeydown` exits first
 * (before chord parsing, `tryExec`, `preventDefault`, or `executeAction`). Listener is registered
 * with `capture: true` on `document` so this gate runs in the capture phase as well.
 */
function isGlobalShortcutDispatchSuspendedForSettingsCapture() {
    return captureListening;
}
function loadStoredMap() {
    try {
        const raw = localStorage.getItem(KEYBINDINGS_STORAGE_KEY);
        if (!raw)
            return {};
        const parsed = JSON.parse(raw);
        if (!parsed || typeof parsed !== "object")
            return {};
        const out = {};
        for (const id of Object.keys(DEFAULT_KEY_CHORDS)) {
            const v = parsed[id];
            if (typeof v === "string" && v.trim())
                out[id] = v.trim().toLowerCase();
        }
        return out;
    }
    catch {
        return {};
    }
}
let storedOverrides = loadStoredMap();
export function reloadKeybindingsFromStorage() {
    storedOverrides = loadStoredMap();
}
function resolvedChord(actionId) {
    const o = storedOverrides[actionId];
    if (o && o.length > 0)
        return o;
    return DEFAULT_KEY_CHORDS[actionId];
}
/** Resolved chord for UI display / execution. */
export function getResolvedChordForAction(actionId) {
    return resolvedChord(actionId);
}
/**
 * Save override; validates duplicate use in overlapping contexts.
 * Returns false if duplicate; shows toast.
 */
export function saveKeybindingOverride(actionId, chord) {
    const normalized = normalizeChordString(chord);
    if (!normalized) {
        showToast("Invalid key");
        return false;
    }
    const next = { ...storedOverrides, [actionId]: normalized };
    if (hasConflict(actionId, next)) {
        showToast("That key is already used");
        return false;
    }
    storedOverrides = next;
    try {
        const obj = {};
        for (const id of Object.keys(DEFAULT_KEY_CHORDS)) {
            const v = next[id];
            if (v)
                obj[id] = v;
        }
        localStorage.setItem(KEYBINDINGS_STORAGE_KEY, JSON.stringify(obj));
    }
    catch {
        showToast("Could not save keybindings");
        return false;
    }
    return true;
}
function contextsForAction(id) {
    const meta = KEY_ACTION_LIST.find((m) => m.id === id);
    return meta ? meta.contexts : [];
}
function hasConflict(changedId, map) {
    const chord = map[changedId];
    if (!chord)
        return false;
    for (const id of Object.keys(DEFAULT_KEY_CHORDS)) {
        if (id === changedId)
            continue;
        const other = map[id] ?? DEFAULT_KEY_CHORDS[id];
        if (other !== chord)
            continue;
        const a = new Set(contextsForAction(changedId));
        const b = new Set(contextsForAction(id));
        for (const c of a) {
            if (b.has(c))
                return true;
        }
    }
    return false;
}
/** Normalize user/storage string to canonical chord. */
export function normalizeChordString(s) {
    if (!s || typeof s !== "string")
        return null;
    const t = s.trim().toLowerCase();
    if (!t)
        return null;
    const parts = t.split("+").map((p) => p.trim()).filter(Boolean);
    if (parts.length === 0)
        return null;
    const mods = new Set();
    let key = null;
    for (const p of parts) {
        if (p === "ctrl" || p === "control")
            mods.add("ctrl");
        else if (p === "alt")
            mods.add("alt");
        else if (p === "meta" || p === "cmd" || p === "win")
            mods.add("meta");
        else if (p === "shift")
            mods.add("shift");
        else if (!key)
            key = p;
        else
            return null;
    }
    if (!key)
        return null;
    const order = ["ctrl", "alt", "meta", "shift"];
    const orderedMods = order.filter((m) => mods.has(m));
    return `${orderedMods.join("+")}${orderedMods.length ? "+" : ""}${key}`;
}
export function formatChordForDisplay(chord) {
    const n = normalizeChordString(chord);
    if (!n)
        return chord;
    const parts = n.split("+");
    const out = [];
    for (const p of parts) {
        if (p === "ctrl")
            out.push("Ctrl");
        else if (p === "alt")
            out.push("Alt");
        else if (p === "meta")
            out.push("Meta");
        else if (p === "shift")
            out.push("Shift");
        else if (p === "escape")
            out.push("Esc");
        else if (p === "tab")
            out.push("Tab");
        else if (p === " ")
            out.push("Space");
        else
            out.push(p.length === 1 ? p.toUpperCase() : p);
    }
    return out.join("+");
}
/**
 * Canonical chord from a keydown event (code-based letters/digits; Shift+1 => shift+1).
 */
export function chordFromKeyboardEvent(ev) {
    if (["Control", "Alt", "Shift", "Meta"].includes(ev.key))
        return null;
    const mods = [];
    if (ev.ctrlKey)
        mods.push("ctrl");
    if (ev.altKey)
        mods.push("alt");
    if (ev.metaKey)
        mods.push("meta");
    if (ev.shiftKey)
        mods.push("shift");
    mods.sort((a, b) => {
        const order = ["ctrl", "alt", "meta", "shift"];
        return order.indexOf(a) - order.indexOf(b);
    });
    let base = null;
    if (ev.code === "Escape")
        base = "escape";
    else if (ev.code === "Tab")
        base = "tab";
    else if (ev.code === "Space")
        base = "space";
    else if (ev.code.startsWith("Digit"))
        base = ev.code.slice(5).toLowerCase();
    else if (ev.code.startsWith("Key"))
        base = ev.code.slice(3).toLowerCase();
    else if (ev.key.length === 1)
        base = ev.key.toLowerCase();
    if (base === null)
        return null;
    const prefix = mods.length ? `${mods.join("+")}+` : "";
    return `${prefix}${base}`;
}
export function isTypingInTextField() {
    const el = document.activeElement;
    if (!el || !(el instanceof HTMLElement))
        return false;
    if (el.isContentEditable)
        return true;
    if (el.tagName === "TEXTAREA")
        return true;
    if (el.tagName !== "INPUT")
        return false;
    const type = el.type?.toLowerCase() ?? "text";
    const textLike = new Set([
        "text",
        "search",
        "email",
        "password",
        "url",
        "tel",
        "number",
        "date",
        "time",
        "datetime-local",
        "month",
        "week",
    ]);
    return textLike.has(type) || type === "";
}
export function isInteractiveElementFocused() {
    const el = document.activeElement;
    if (!el || !(el instanceof HTMLElement))
        return false;
    const tag = el.tagName;
    if (tag === "SELECT" || tag === "BUTTON")
        return true;
    if (tag === "INPUT") {
        const type = el.type?.toLowerCase() ?? "";
        if (type === "checkbox" || type === "radio" || type === "range" || type === "color" || type === "file")
            return true;
        return false;
    }
    if (el.getAttribute("role") === "listbox" || el.getAttribute("role") === "combobox")
        return true;
    if (el.closest("[data-keybinding-interactive]"))
        return true;
    return false;
}
export function getTopOpenDialog() {
    const all = Array.from(document.querySelectorAll("dialog[open]"));
    if (all.length === 0)
        return null;
    return all[all.length - 1];
}
export function isModalOpen() {
    return getTopOpenDialog() !== null;
}
export function getCurrentView() {
    const r = getRoute();
    if (r === "boardBySlug")
        return "board";
    if (r === "dashboard")
        return "dashboard";
    if (r === "projects")
        return "projects";
    return "unknown";
}
function chordMatchesAction(chord, actionId) {
    if (!chord)
        return false;
    const want = resolvedChord(actionId);
    return chord === want;
}
function shouldBlockForFocus() {
    if (isTypingInTextField())
        return true;
    if (isInteractiveElementFocused())
        return true;
    return false;
}
/** One clickable per project in list/grid order; grid has two `[data-open]` per project (dedupe by slug). */
function getProjectsListJumpElements() {
    const root = document.getElementById("projectList");
    if (!root)
        return [];
    const raw = Array.from(root.querySelectorAll("[data-open]"));
    const seen = new Set();
    const out = [];
    for (const el of raw) {
        const slug = el.getAttribute("data-open");
        if (!slug || seen.has(slug))
            continue;
        seen.add(slug);
        out.push(el);
    }
    return out;
}
export function executeAction(actionId) {
    const view = getCurrentView();
    switch (actionId) {
        case "newTodo": {
            if (view !== "board")
                return;
            const btn = document.getElementById("newTodoBtn");
            if (btn && btn.offsetParent !== null)
                btn.click();
            return;
        }
        case "boardSearch": {
            if (view !== "board")
                return;
            const input = document.getElementById("searchInput");
            if (input) {
                input.focus();
                input.select?.();
            }
            return;
        }
        case "openSettings": {
            if (deps)
                void Promise.resolve(deps.openSettings());
            return;
        }
        case "cycleMainNavTabs": {
            if (view !== "dashboard" && view !== "projects")
                return;
            const route = getRoute();
            const tab = getProjectsTab();
            let idx;
            if (route === "dashboard") {
                idx = 0;
            }
            else if (route === "projects") {
                idx = tab === "temporary" ? 2 : 1;
            }
            else {
                return;
            }
            const next = (idx + 1) % 3;
            const persistProjectsTab = (value) => {
                setProjectsTab(value);
                localStorage.setItem("projectsTab", value);
                if (getUser()) {
                    void apiFetch("/api/user/preferences", {
                        method: "PUT",
                        body: JSON.stringify({ key: "projectsTab", value }),
                    }).catch(() => { });
                }
            };
            if (next === 0) {
                navigate("/dashboard");
                return;
            }
            if (next === 1) {
                persistProjectsTab("projects");
                navigate("/");
                return;
            }
            persistProjectsTab("temporary");
            navigate("/");
            return;
        }
        case "createProject": {
            if (view !== "projects")
                return;
            const nameInput = document.getElementById("projectName");
            if (nameInput && nameInput.offsetParent !== null) {
                nameInput.focus();
                return;
            }
            const submitBtn = document.querySelector("#createProjectForm button[type='submit']");
            submitBtn?.focus();
            return;
        }
        case "boardEscapeBack": {
            if (view !== "board")
                return;
            const membersDialog = document.getElementById("membersDialog");
            const hasMembersDialogOpen = membersDialog && membersDialog.open;
            if (getBoard() &&
                getAuthStatusAvailable() &&
                !todoDialog.open &&
                !settingsDialog.open &&
                !hasMembersDialogOpen) {
                navigate("/");
            }
            return;
        }
        case "dashboardProject1":
        case "dashboardProject2":
        case "dashboardProject3":
        case "dashboardProject4":
        case "dashboardProject5":
        case "dashboardProject6":
        case "dashboardProject7":
        case "dashboardProject8":
        case "dashboardProject9": {
            if (view !== "dashboard")
                return;
            const idx = DASHBOARD_PROJECT_IDS.indexOf(actionId);
            const tabs = document.querySelectorAll(".dashboard-project-group > .dashboard-project-group__tab[data-open-board]");
            if (tabs.length === 0) {
                showToast("No projects available");
                return;
            }
            const el = tabs[idx];
            if (!el)
                return;
            el.click();
            return;
        }
        case "projectsList1":
        case "projectsList2":
        case "projectsList3":
        case "projectsList4":
        case "projectsList5":
        case "projectsList6":
        case "projectsList7":
        case "projectsList8":
        case "projectsList9": {
            if (view !== "projects")
                return;
            const idx = PROJECTS_LIST_IDS.indexOf(actionId);
            const els = getProjectsListJumpElements();
            if (els.length === 0) {
                showToast("No projects available");
                return;
            }
            const el = els[idx];
            if (!el)
                return;
            el.click();
            return;
        }
        default:
            return;
    }
}
function onGlobalKeydown(ev) {
    if (isGlobalShortcutDispatchSuspendedForSettingsCapture()) {
        return;
    }
    const chord = chordFromKeyboardEvent(ev);
    if (!chord)
        return;
    if (isModalOpen()) {
        return;
    }
    if (ev.repeat)
        return;
    const view = getCurrentView();
    const tabCyclesMainNav = chord === "tab" && (view === "dashboard" || view === "projects");
    if (tabCyclesMainNav) {
        if (isTypingInTextField())
            return;
    }
    else if (shouldBlockForFocus()) {
        return;
    }
    const tryExec = (id) => {
        const meta = KEY_ACTION_LIST.find((m) => m.id === id);
        if (!meta)
            return false;
        if (!meta.contexts.includes(view))
            return false;
        if (!chordMatchesAction(chord, id))
            return false;
        ev.preventDefault();
        executeAction(id);
        return true;
    };
    if (tryExec("boardEscapeBack"))
        return;
    if (view === "board") {
        if (tryExec("newTodo"))
            return;
        if (tryExec("boardSearch"))
            return;
    }
    if (tryExec("openSettings"))
        return;
    if (view === "dashboard" || view === "projects") {
        if (tryExec("cycleMainNavTabs"))
            return;
    }
    if (view === "projects") {
        if (tryExec("createProject"))
            return;
        for (const id of PROJECTS_LIST_IDS) {
            if (tryExec(id))
                return;
        }
    }
    if (view === "dashboard") {
        for (const id of DASHBOARD_PROJECT_IDS) {
            if (tryExec(id))
                return;
        }
    }
}
export function initKeybindings(d) {
    if (listenerAttached)
        return;
    deps = d;
    listenerAttached = true;
    // capture: true — runs in capture phase; paired with isGlobalShortcutDispatchSuspendedForSettingsCapture() at top of handler.
    document.addEventListener("keydown", onGlobalKeydown, { capture: true });
}
/** Extensibility hook; built-in shortcuts use KEY_ACTION_LIST and persisted overrides. */
export function registerKeybinding(_binding) {
    void _binding;
}
