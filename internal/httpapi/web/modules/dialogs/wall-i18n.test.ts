// @vitest-environment happy-dom
//
// Wall-only i18n behavior: an open wall must relocalize in place on
// I18N_LOCALE_CHANGED without refetching the wall doc, reopening the dialog,
// rebuilding the note DOM, or losing viewport / selection / edit state. The
// locale listener is bound exactly once per open (scoped to the wall's
// AbortController) and removed on close.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initWallTestI18n } from "./wall-test-harness.js";
import enCatalog from "../i18n/locales/en.json";
import deCatalog from "../i18n/locales/de.json";

const en = enCatalog as Record<string, string>;
const de = deCatalog as Record<string, string>;

const apiFetchMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const onMock = vi.hoisted(() => vi.fn());
const offMock = vi.hoisted(() => vi.fn());
const openTodoDialogMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));

const wallDialogEl = vi.hoisted(() => document.createElement("dialog") as HTMLDialogElement);
const wallSurfaceEl = vi.hoisted(() => document.createElement("div"));
const closeWallBtnEl = vi.hoisted(() => document.createElement("button"));
const wallTrashEl = vi.hoisted(() => document.createElement("img"));

vi.mock("../api.js", () => ({ apiFetch: apiFetchMock }));
vi.mock("../utils.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../utils.js")>();
  return { ...actual, confirmDelete: confirmDeleteMock, showToast: showToastMock };
});
vi.mock("../events.js", () => ({ on: onMock, off: offMock }));
vi.mock("../state/selectors.js", () => ({ getUser: () => ({ id: 1 }) }));
vi.mock("../dom/elements.js", () => ({
  wallDialog: wallDialogEl,
  wallSurface: wallSurfaceEl,
  closeWallBtn: closeWallBtnEl,
  wallTrash: wallTrashEl,
}));
vi.mock("./todo.js", () => ({ openTodoDialog: openTodoDialogMock }));

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) { this.open = true; },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) { this.open = false; this.dispatchEvent(new Event("close")); },
  });
}

const SHORTCUT_KEYS = [
  "createNote", "changeColor", "editNote", "deleteNote", "drawLines",
  "multiSelect", "canvasMode", "panCanvas", "zoom",
] as const;

// Build a wall shell matching index.html (with data-i18n-* attributes) so the
// hydrator can relocalize static chrome in place.
function setupShell(): void {
  document.body.innerHTML = "";
  wallDialogEl.innerHTML = "";

  const shortcuts = document.createElement("div");
  shortcuts.className = "wall-shortcuts";
  shortcuts.setAttribute("aria-hidden", "true");
  for (const key of SHORTCUT_KEYS) {
    const label = document.createElement("span");
    label.setAttribute("data-i18n-text", `wall.shortcuts.${key}.label`);
    label.textContent = en[`wall.shortcuts.${key}.label`];
    const hint = document.createElement("i");
    hint.setAttribute("data-i18n-text", `wall.shortcuts.${key}.hint`);
    hint.textContent = en[`wall.shortcuts.${key}.hint`];
    shortcuts.append(label, document.createTextNode(" "), hint, document.createElement("br"));
  }
  wallDialogEl.appendChild(shortcuts);

  wallSurfaceEl.innerHTML = "";
  wallSurfaceEl.id = "wallSurface";
  wallSurfaceEl.className = "wall-surface";
  wallSurfaceEl.setAttribute("aria-label", en["wall.shell.surfaceLabel"]);
  wallSurfaceEl.setAttribute("data-i18n-aria-label", "wall.shell.surfaceLabel");
  wallDialogEl.appendChild(wallSurfaceEl);

  const modeBtn = document.createElement("button");
  modeBtn.id = "wallModeToggleBtn";
  modeBtn.setAttribute("aria-label", en["wall.mode.select.label"]);
  modeBtn.setAttribute("title", en["wall.mode.select.title"]);
  modeBtn.setAttribute("data-i18n-aria-label", "wall.mode.select.label");
  modeBtn.setAttribute("data-i18n-title", "wall.mode.select.title");
  wallDialogEl.appendChild(modeBtn);

  const fitBtn = document.createElement("button");
  fitBtn.id = "wallFitViewBtn";
  fitBtn.setAttribute("aria-label", en["wall.shell.fitView.label"]);
  fitBtn.setAttribute("title", en["wall.shell.fitView.title"]);
  fitBtn.setAttribute("data-i18n-aria-label", "wall.shell.fitView.label");
  fitBtn.setAttribute("data-i18n-title", "wall.shell.fitView.title");
  wallDialogEl.appendChild(fitBtn);

  closeWallBtnEl.id = "closeWallBtn";
  closeWallBtnEl.setAttribute("aria-label", en["common.close"]);
  closeWallBtnEl.setAttribute("data-i18n-aria-label", "common.close");
  wallDialogEl.appendChild(closeWallBtnEl);

  wallTrashEl.id = "wallTrash";
  wallTrashEl.setAttribute("alt", en["wall.shell.trashAlt"]);
  wallDialogEl.appendChild(wallTrashEl);

  document.body.appendChild(wallDialogEl);
}

function singleNoteDoc() {
  return {
    notes: [{ id: "n1", x: 20, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "Hello", version: 1 }],
    edges: [],
    version: 1,
  };
}

function multiNoteDoc() {
  return {
    notes: [
      { id: "n1", x: 20, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "first", version: 1 },
      { id: "n2", x: 220, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "second", version: 1 },
    ],
    edges: [],
    version: 1,
  };
}

function emptyDoc() {
  return { notes: [], edges: [], version: 1 };
}

function routeWall(docFactory: () => unknown): void {
  apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (typeof url === "string" && url.includes("/wall") && !init?.method) return docFactory();
    return {};
  });
}

async function flushPromises(count = 10): Promise<void> {
  for (let i = 0; i < count; i += 1) await Promise.resolve();
}

async function importI18n() {
  return import("../i18n/index.js");
}

async function openWall(role = "maintainer") {
  const mod = await import("./wall.js");
  await mod.openWallDialog({ projectId: 1, slug: "alpha", role: role as never });
  await flushPromises();
  return mod;
}

describe("wall i18n", () => {
  beforeEach(async () => {
    vi.resetModules();
    await initWallTestI18n({ en, de }, "en");
    installDialogPolyfill();
    setupShell();
    apiFetchMock.mockReset();
    confirmDeleteMock.mockReset();
    showToastMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
    openTodoDialogMock.mockReset();
    openTodoDialogMock.mockResolvedValue(undefined);
    localStorage.clear();
    routeWall(singleNoteDoc);
  });

  afterEach(() => {
    if (wallDialogEl.open) wallDialogEl.close();
    vi.useRealTimers();
    // Restore spies (console.warn, document.addEventListener) even when an
    // assertion above failed, so no test leaks suppression into the next one.
    vi.restoreAllMocks();
  });

  // ---- Listener lifecycle ------------------------------------------------

  it("binds exactly one locale listener per open and never stacks across cycles", async () => {
    const addSpy = vi.spyOn(document, "addEventListener");
    const localeAdds = () =>
      addSpy.mock.calls.filter(([type]) => type === "scrumboy:i18n-locale-changed").length;

    await openWall();
    expect(localeAdds()).toBe(1);

    // Already-mounted re-open is a no-op and must not add a second listener.
    await openWall();
    expect(localeAdds()).toBe(1);

    wallDialogEl.close();
    await flushPromises();
    await openWall();
    expect(localeAdds()).toBe(2); // one per real open, not stacked

    addSpy.mockRestore();
  });

  it("does not mutate the torn-down wall when locale changes after close", async () => {
    await openWall();
    wallDialogEl.close();
    await flushPromises();

    // Sentinel value that a stale listener would overwrite.
    const modeBtn = document.getElementById("wallModeToggleBtn")!;
    modeBtn.setAttribute("aria-label", "SENTINEL");
    wallSurfaceEl.setAttribute("aria-label", "SENTINEL");

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    expect(modeBtn.getAttribute("aria-label")).toBe("SENTINEL");
    expect(wallSurfaceEl.getAttribute("aria-label")).toBe("SENTINEL");
  });

  // ---- No refetch / reopen ----------------------------------------------

  it("does not issue a second GET /wall on locale change while open", async () => {
    await openWall();
    const getWallCalls = () =>
      apiFetchMock.mock.calls.filter(
        ([url, init]) => typeof url === "string" && url.includes("/wall") && !(init as RequestInit | undefined)?.method,
      ).length;
    expect(getWallCalls()).toBe(1);

    apiFetchMock.mockClear();
    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(wallDialogEl.open).toBe(true);
    // Note text (user data) is untouched.
    const display = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"] .wall-note__display');
    expect(display?.textContent).toBe("Hello");
  });

  // ---- Static shell relocalization --------------------------------------

  it("relocalizes shell shortcuts and button chrome in place", async () => {
    await openWall();
    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    const labelSpan = wallDialogEl.querySelector('[data-i18n-text="wall.shortcuts.createNote.label"]');
    expect(labelSpan?.textContent).toBe(de["wall.shortcuts.createNote.label"]);
    expect(wallSurfaceEl.getAttribute("aria-label")).toBe(de["wall.shell.surfaceLabel"]);

    const fitBtn = document.getElementById("wallFitViewBtn")!;
    expect(fitBtn.getAttribute("aria-label")).toBe(de["wall.shell.fitView.label"]);
    expect(fitBtn.getAttribute("title")).toBe(de["wall.shell.fitView.title"]);
    expect(document.getElementById("closeWallBtn")!.getAttribute("aria-label")).toBe(de["common.close"]);
    // Trash alt is hydrator-unsupported; the wall sync helper handles it.
    expect(wallTrashEl.getAttribute("alt")).toBe(de["wall.shell.trashAlt"]);
  });

  // ---- Mode button (stateful) -------------------------------------------

  it("relocalizes the mode button for Select and Pan while preserving the mode", async () => {
    await openWall();
    const modeBtn = document.getElementById("wallModeToggleBtn")! as HTMLButtonElement;
    expect(modeBtn.getAttribute("aria-label")).toBe(en["wall.mode.select.label"]);

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();
    expect(modeBtn.getAttribute("aria-label")).toBe(de["wall.mode.select.label"]);
    expect(modeBtn.getAttribute("title")).toBe(de["wall.mode.select.title"]);
    expect(modeBtn.getAttribute("aria-pressed")).toBe("false");

    // Switch to Pan, then relocalize again: mode is preserved, copy follows.
    modeBtn.click();
    await flushPromises();
    expect(modeBtn.getAttribute("aria-pressed")).toBe("true");

    await i18n.setLocale("en");
    await flushPromises();
    expect(modeBtn.getAttribute("aria-label")).toBe(en["wall.mode.pan.label"]);
    expect(modeBtn.getAttribute("title")).toBe(en["wall.mode.pan.title"]);
    expect(modeBtn.getAttribute("aria-pressed")).toBe("true");
  });

  // ---- Empty-state relocalization ---------------------------------------

  it("relocalizes the editable empty state in place without refetch", async () => {
    routeWall(emptyDoc);
    await openWall("maintainer");
    expect(wallSurfaceEl.querySelector(".wall-empty")?.textContent).toContain(en["wall.empty.title"]);

    apiFetchMock.mockClear();
    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    const empty = wallSurfaceEl.querySelector(".wall-empty");
    expect(empty?.textContent).toContain(de["wall.empty.title"]);
    expect(empty?.textContent).toContain(de["wall.empty.hintEditable"]);
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("relocalizes the read-only empty state in place", async () => {
    routeWall(emptyDoc);
    await openWall("viewer");

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    const empty = wallSurfaceEl.querySelector(".wall-empty");
    expect(empty?.textContent).toContain(de["wall.empty.hintReadonly"]);
  });

  // ---- Note / editor ARIA -----------------------------------------------

  it("relocalizes resize-handle ARIA on existing notes", async () => {
    await openWall();
    const handle = wallSurfaceEl.querySelector(".wall-note__resize-handle");
    expect(handle?.getAttribute("aria-label")).toBe(en["wall.note.resize"]);

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();
    expect(handle?.getAttribute("aria-label")).toBe(de["wall.note.resize"]);
  });

  it("relocalizes an open editor's ARIA without replacing the textarea or its typed text", async () => {
    await openWall();
    const noteEl = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!noteEl) throw new Error("missing note");
    noteEl.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, clientX: 30, clientY: 30 }));
    await flushPromises();

    const editor = noteEl.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    if (!editor) throw new Error("editor not opened");
    editor.value = "draft typing not yet saved";
    expect(editor.getAttribute("aria-label")).toBe(en["wall.note.edit"]);

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    const editorAfter = noteEl.querySelector<HTMLTextAreaElement>(".wall-note__editor");
    expect(editorAfter).toBe(editor); // same node, not replaced
    expect(editorAfter!.value).toBe("draft typing not yet saved");
    expect(editorAfter!.getAttribute("aria-label")).toBe(de["wall.note.edit"]);
  });

  // ---- Context menu ------------------------------------------------------

  it("relocalizes an open context menu's labels in place, preserving position and selection", async () => {
    routeWall(multiNoteDoc);
    await openWall();
    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 40, clientY: 50 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector<HTMLElement>(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    const delBtn = menu!.querySelector<HTMLButtonElement>('[data-action="delete"]')!;
    expect(delBtn.textContent).toBe("Delete 2 notes");
    const left = menu!.style.left;
    const top = menu!.style.top;

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    const menuAfter = wallDialogEl.querySelector<HTMLElement>(".wall-note-context-menu");
    expect(menuAfter).toBe(menu); // not reopened
    expect(menuAfter!.style.left).toBe(left);
    expect(menuAfter!.style.top).toBe(top);
    expect(delBtn.textContent).toBe(de["wall.menu.deleteCount"].replace("{count}", "2"));
    // Selected ids preserved.
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(2);
  });

  it("generates single-note context-menu labels via the active locale", async () => {
    const i18n = await importI18n();
    await i18n.setLocale("de");
    await openWall();

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu!.querySelector('[data-action="create-todo"]')?.textContent).toBe(de["wall.menu.createTodoFromNote"]);
    expect(menu!.querySelector('[data-action="delete"]')?.textContent).toBe(de["common.delete"]);
  });

  // ---- Viewport / selection preservation --------------------------------

  it("preserves viewport, selection, and mode across a locale change", async () => {
    routeWall(multiNoteDoc);
    await openWall();
    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1"]);

    const modeBtn = document.getElementById("wallModeToggleBtn")! as HTMLButtonElement;
    modeBtn.click(); // -> Pan
    await flushPromises();

    const content = wallSurfaceEl.querySelector<HTMLElement>(".wall-content");
    const beforeTransform = content?.style.transform ?? "";
    const beforeSelected = Array.from(wallSurfaceEl.querySelectorAll(".wall-note--selected")).map(
      (el) => (el as HTMLElement).dataset.noteId,
    );

    const i18n = await importI18n();
    await i18n.setLocale("de");
    await flushPromises();

    expect(content?.style.transform ?? "").toBe(beforeTransform);
    const afterSelected = Array.from(wallSurfaceEl.querySelectorAll(".wall-note--selected")).map(
      (el) => (el as HTMLElement).dataset.noteId,
    );
    expect(afterSelected).toEqual(beforeSelected);
    expect(modeBtn.getAttribute("aria-pressed")).toBe("true");
  });

  // ---- Confirms / toasts in a non-English locale ------------------------

  it("uses localized delete confirm text in German", async () => {
    confirmDeleteMock.mockResolvedValue(false);
    const i18n = await importI18n();
    await i18n.setLocale("de");
    await openWall();

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();
    wallDialogEl.querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]')!.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith(de["wall.confirm.deleteNote"]);
  });

  it("uses a localized toast for the failed note update path", async () => {
    const i18n = await importI18n();
    await i18n.setLocale("de");
    const patchError = Object.assign(new Error("boom"), { status: 500 });
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (typeof url === "string" && url.includes("/wall") && !init?.method) return singleNoteDoc();
      if (init?.method === "PATCH") throw patchError;
      return {};
    });
    await openWall();

    // The expected PATCH failure logs a warning; suppress only that output
    // and forward anything unexpected to the real console.
    const originalWarn = console.warn;
    const warnSpy = vi.spyOn(console, "warn").mockImplementation((...args: any[]) => {
      if (args[0] !== "wall patch failed") originalWarn(...args);
    });
    try {
      // Single left-click on a note arms the color-cycle timer, which fires a
      // PATCH; that PATCH rejects here, exercising the failure toast path.
      vi.useFakeTimers();
      const noteEl = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
      if (!noteEl) throw new Error("missing note");
      noteEl.dispatchEvent(Object.assign(new Event("pointerdown", { bubbles: true, cancelable: true }), { button: 0, clientX: 32, clientY: 32, pointerId: 1, pointerType: "mouse" }));
      document.dispatchEvent(Object.assign(new Event("pointerup", { bubbles: true, cancelable: true }), { button: 0, clientX: 32, clientY: 32, pointerId: 1, pointerType: "mouse" }));
      vi.advanceTimersByTime(500);
      await flushPromises();
      vi.useRealTimers();
      await flushPromises();

      expect(warnSpy).toHaveBeenCalledWith("wall patch failed", patchError);
      expect(showToastMock).toHaveBeenCalledWith(de["wall.toast.updateNoteFailed"]);
    } finally {
      warnSpy.mockRestore();
    }
  });

  it("shows the localized missing-wall toast on a 404 refetch", async () => {
    const i18n = await importI18n();
    await i18n.setLocale("de");
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (typeof url === "string" && url.includes("/wall") && !init?.method) {
        throw Object.assign(new Error("not found"), { status: 404 });
      }
      return {};
    });

    await openWall();

    expect(showToastMock).toHaveBeenCalledWith(de["wall.toast.missingWall"]);
    expect(wallDialogEl.open).toBe(false);
  });
});
