// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initWallTestI18n } from "./wall-test-harness.js";
import enCatalog from "../i18n/locales/en.json";

const apiFetchMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());
const onMock = vi.hoisted(() => vi.fn());
const offMock = vi.hoisted(() => vi.fn());
const openTodoDialogMock = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));

const wallDialogEl = vi.hoisted(() => document.createElement("dialog"));
const wallSurfaceEl = vi.hoisted(() => document.createElement("div"));
const closeWallBtnEl = vi.hoisted(() => document.createElement("button"));
const wallTrashEl = vi.hoisted(() => document.createElement("div"));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../utils.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../utils.js")>();
  return {
    ...actual,
    confirmDelete: confirmDeleteMock,
    showToast: vi.fn(),
  };
});

vi.mock("../events.js", () => ({
  on: onMock,
  off: offMock,
}));

vi.mock("../state/selectors.js", () => ({
  getUser: () => ({ id: 1 }),
  getBoard: () => ({
    project: { estimationMode: "MODIFIED_FIBONACCI" },
    priorityOrder: [],
  }),
  getBoardMembers: () => [],
  getTagColors: () => ({}),
}));

vi.mock("../dom/elements.js", () => ({
  wallDialog: wallDialogEl,
  wallSurface: wallSurfaceEl,
  closeWallBtn: closeWallBtnEl,
  wallTrash: wallTrashEl,
}));

vi.mock("./todo.js", () => ({
  openTodoDialog: openTodoDialogMock,
}));

function installDialogPolyfill(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true;
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false;
      this.dispatchEvent(new Event("close"));
    },
  });
}

function setupDom(): void {
  document.body.innerHTML = "";
  wallDialogEl.innerHTML = "";
  wallSurfaceEl.innerHTML = "";
  wallDialogEl.appendChild(wallSurfaceEl);
  document.body.appendChild(wallDialogEl);
  document.body.appendChild(closeWallBtnEl);
  document.body.appendChild(wallTrashEl);
}

function createWallDoc() {
  return {
    notes: [
      {
        id: "n1",
        x: 20,
        y: 20,
        width: 160,
        height: 100,
        color: "#FFFFFF",
        text: "Hello",
        version: 1,
      },
    ],
    edges: [],
    version: 1,
  };
}

function createMultiNoteWallDoc() {
  return {
    notes: [
      { id: "n1", x: 20, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "first", version: 1 },
      { id: "n2", x: 220, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "second", version: 1 },
      { id: "n3", x: 420, y: 20, width: 160, height: 100, color: "#FFFFFF", text: "third", version: 1 },
    ],
    edges: [],
    version: 1,
  };
}

function createStoryWallDoc() {
  return {
    notes: [],
    edges: [],
    stories: [{
      localId: 7,
      x: 80,
      y: 90,
      version: 1,
      todo: { id: 70, localId: 7, title: "Pinned canonical", status: "BACKLOG", tags: ["wall"] },
    }],
    version: 1,
  };
}

function dispatchPointer(target: EventTarget, type: string, extra: Record<string, unknown> = {}): void {
  const ev = new Event(type, { bubbles: true, cancelable: true }) as Event & Record<string, unknown>;
  Object.assign(ev, {
    clientX: 30,
    clientY: 30,
    button: 0,
    shiftKey: false,
    ctrlKey: false,
    metaKey: false,
    ...extra,
  });
  target.dispatchEvent(ev);
}

function dispatchKey(key: string, target: EventTarget = window): void {
  target.dispatchEvent(
    new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
  );
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

describe("wall interactions", () => {
  beforeEach(async () => {
    vi.resetModules();
    await initWallTestI18n({ en: enCatalog as Record<string, string> });
    installDialogPolyfill();
    setupDom();
    localStorage.clear();
    apiFetchMock.mockReset();
    confirmDeleteMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
    openTodoDialogMock.mockReset();
    openTodoDialogMock.mockResolvedValue(undefined);
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) {
        return createWallDoc();
      }
      if (init?.method === "DELETE") {
        return {};
      }
      if (init?.method === "PATCH") {
        const body = init.body ? JSON.parse(String(init.body)) : {};
        return {
          ...createWallDoc().notes[0],
          color: body.color ?? "#FFFFFF",
          version: 2,
        };
      }
      if (url.includes("/transient")) {
        return {};
      }
      return {};
    });
  });

  afterEach(() => {
    if (wallDialogEl.open) {
      wallDialogEl.close();
    }
    vi.useRealTimers();
  });

  it("opens the note context menu on right-click (no immediate confirm)", async () => {
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    expect(menu?.querySelector('[data-action="create-todo"]')?.textContent).toBe("Create Todo from Note");
    expect(menu?.querySelector('[data-action="delete"]')?.textContent).toBe("Delete");
    expect(confirmDeleteMock).not.toHaveBeenCalled();
  });

  it("opens and removes a canonical story from its Wall context menu", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createStoryWallDoc();
      return {};
    });
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    const story = wallSurfaceEl.querySelector<HTMLElement>(".wall-story");
    expect(story?.textContent).toContain("Pinned canonical");

    story?.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2 }));
    await flushPromises();
    wallDialogEl.querySelector<HTMLButtonElement>('[data-action="open"]')?.click();
    await vi.waitFor(() => {
      expect(openTodoDialogMock).toHaveBeenCalledWith(expect.objectContaining({
        mode: "edit",
        todo: expect.objectContaining({ localId: 7, title: "Pinned canonical" }),
      }));
    });

    story?.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2 }));
    await flushPromises();
    wallDialogEl.querySelector<HTMLButtonElement>('[data-action="remove"]')?.click();
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/stories/7", { method: "DELETE" });
  });

  it("keeps viewer story menus read-only", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createStoryWallDoc();
      return {};
    });
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "viewer" });
    wallSurfaceEl.querySelector<HTMLElement>(".wall-story")?.dispatchEvent(
      new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2 }),
    );
    await flushPromises();
    expect(wallDialogEl.querySelector('[data-action="open"]')).toBeTruthy();
    expect(wallDialogEl.querySelector('[data-action="remove"]')).toBeNull();
  });

  it("Ctrl+right-click creates a Todo and pins the returned canonical local ID at the captured point", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.endsWith("/wall") && !init?.method) return { notes: [], edges: [], stories: [], version: 0 };
      if (url.endsWith("/wall/stories") && init?.method === "POST") {
        return { localId: 21, x: 110, y: 125, version: 1 };
      }
      return {};
    });
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    wallSurfaceEl.dispatchEvent(new MouseEvent("contextmenu", {
      bubbles: true,
      cancelable: true,
      ctrlKey: true,
      clientX: 110,
      clientY: 125,
    }));
    await flushPromises();
    const options = openTodoDialogMock.mock.calls.at(-1)?.[0];
    expect(options).toEqual(expect.objectContaining({ mode: "create", role: "maintainer" }));
    await options.onCreated({ id: 210, localId: 21, title: "Created", status: "BACKLOG" });
    await flushPromises();
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/stories", {
      method: "POST",
      body: JSON.stringify({ localId: 21, x: 110, y: 125 }),
    });
  });

  it("deletes a note after choosing Delete in the context menu and confirming", async () => {
    confirmDeleteMock.mockResolvedValue(true);
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const deleteBtn = wallDialogEl.querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]');
    if (!deleteBtn) throw new Error("missing delete menu item");
    deleteBtn.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("does not delete a note when confirmation is cancelled after Delete menu click", async () => {
    confirmDeleteMock.mockResolvedValue(false);
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const deleteBtn = wallDialogEl.querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]');
    if (!deleteBtn) throw new Error("missing delete menu item");
    deleteBtn.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
  });

  it("opens the todo dialog seeded with the note text when Create Todo is chosen", async () => {
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const createBtn = wallDialogEl.querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="create-todo"]');
    if (!createBtn) throw new Error("missing create-todo menu item");
    createBtn.click();
    // Dynamic import + two awaited then-chains inside wall.ts; flush a few
    // extra microtask turns so vitest's module resolver settles.
    await flushPromises(20);

    expect(openTodoDialogMock).toHaveBeenCalledTimes(1);
    const call = openTodoDialogMock.mock.calls[0][0];
    expect(call).toMatchObject({ mode: "create", role: "maintainer", initialTitle: "Hello" });
    expect(confirmDeleteMock).not.toHaveBeenCalled();
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
    expect(wallDialogEl.open).toBe(true);
  });

  it("dismisses the context menu without action when user clicks outside", async () => {
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeTruthy();

    document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true }));
    await flushPromises();

    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
    expect(confirmDeleteMock).not.toHaveBeenCalled();
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
  });

  it("multi-select right-click on a selected note shows group menu (no Create Todo, 'Delete N notes')", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(true);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    expect(menu!.querySelector('[data-action="create-todo"]')).toBeNull();
    const delBtn = menu!.querySelector<HTMLButtonElement>('[data-action="delete"]');
    expect(delBtn?.textContent).toBe("Delete 2 notes");

    delBtn!.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete 2 notes?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n2", { method: "DELETE" });
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n3", { method: "DELETE" });
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(0);
  });

  it("multi-select right-click on a selected note with confirm=false keeps selection intact", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(false);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();
    wallDialogEl
      .querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]')!
      .click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete 2 notes?");
    const deleteCalls = apiFetchMock.mock.calls.filter(
      ([, init]) => (init as RequestInit | undefined)?.method === "DELETE",
    );
    expect(deleteCalls).toHaveLength(0);
    // Selection must still be active so the user can retry.
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(2);
  });

  it("multi-select right-click on an UNSELECTED note shows the full single-note menu and preserves selection", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(true);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    const n3 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n3"]');
    if (!n3) throw new Error("missing n3");
    n3.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 430, clientY: 30 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    expect(menu!.querySelector('[data-action="create-todo"]')).toBeTruthy();
    const delBtn = menu!.querySelector<HTMLButtonElement>('[data-action="delete"]');
    expect(delBtn?.textContent).toBe("Delete");

    delBtn!.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n3", { method: "DELETE" });
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n2", { method: "DELETE" });
    // Selection must still include the original two notes.
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(2);
  });

  it("size-1 selection on the right-clicked note is NOT a group (matches drag-to-trash)", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(true);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1"]);

    const n1 = wallSurfaceEl.querySelector<HTMLElement>('.wall-note[data-note-id="n1"]');
    if (!n1) throw new Error("missing n1");
    n1.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu!.querySelector('[data-action="create-todo"]')).toBeTruthy();
    expect(menu!.querySelector('[data-action="delete"]')?.textContent).toBe("Delete");
  });

  it("keeps left-click as the color-cycle path", async () => {
    vi.useFakeTimers();
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 32, clientY: 32 });
    dispatchPointer(document, "pointerup", { button: 0, clientX: 32, clientY: 32 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    const patchCall = apiFetchMock.mock.calls.find(([url, init]) =>
      String(url).includes("/wall/notes/n1") && init?.method === "PATCH"
    );
    expect(patchCall).toBeTruthy();
    const patchBody = patchCall?.[1]?.body ? JSON.parse(String(patchCall[1].body)) : {};
    expect(typeof patchBody.color).toBe("string");
    expect(patchBody.color).not.toBe("#FFFFFF");
  });

  it("Delete with one selected note prompts single-note confirm and deletes on confirm", async () => {
    confirmDeleteMock.mockResolvedValue(true);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1"]);

    dispatchKey("Delete");
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(wallSurfaceEl.querySelector('.wall-note[data-note-id="n1"]')).toBeNull();
  });

  it("Delete with two selected notes prompts group confirm, deletes all, and clears selection", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(true);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    dispatchKey("Delete");
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete 2 notes?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n2", { method: "DELETE" });
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(0);
  });

  it("Delete with two selected notes and confirm=false keeps selection and skips DELETE", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.includes("/wall") && !init?.method) return createMultiNoteWallDoc();
      if (init?.method === "DELETE") return {};
      return {};
    });
    confirmDeleteMock.mockResolvedValue(false);

    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1", "n2"]);

    dispatchKey("Delete");
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete 2 notes?");
    const deleteCalls = apiFetchMock.mock.calls.filter(
      ([, init]) => (init as RequestInit | undefined)?.method === "DELETE",
    );
    expect(deleteCalls).toHaveLength(0);
    expect(wallSurfaceEl.querySelectorAll(".wall-note--selected")).toHaveLength(2);
  });

  it("does not delete when Delete is pressed while editing a note", async () => {
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const selection = await import("./wall-selection.js");
    selection.setSelection(["n1"]);

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, button: 0, clientX: 32, clientY: 32 }));
    await flushPromises();

    const editor = noteEl.querySelector<HTMLTextAreaElement>("textarea.wall-note__editor");
    if (!editor) throw new Error("missing note editor");
    editor.focus();

    dispatchKey("Delete", editor);
    await flushPromises();

    expect(confirmDeleteMock).not.toHaveBeenCalled();
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
  });
});
