// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const wallDialogEl = vi.hoisted(() => document.createElement("dialog") as HTMLDialogElement);

vi.mock("../dom/elements.js", () => ({
  wallDialog: wallDialogEl,
}));

function setupDom(): void {
  document.body.innerHTML = "";
  wallDialogEl.innerHTML = "";
  document.body.appendChild(wallDialogEl);
  // happy-dom returns zeroed rects for detached-ish layout, which is fine for
  // position-clamping logic; tests only need deterministic viewport math.
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1024 });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 768 });
}

async function flushPromises(count = 4): Promise<void> {
  for (let i = 0; i < count; i += 1) await Promise.resolve();
}

describe("openWallNoteContextMenu", () => {
  beforeEach(() => {
    vi.resetModules();
    setupDom();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders Create Todo and Delete items inside the wall dialog", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    void openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    const items = menu!.querySelectorAll<HTMLButtonElement>('.context-menu__item');
    expect(items).toHaveLength(2);
    expect(items[0].dataset.action).toBe("create-todo");
    expect(items[0].textContent).toBe("Create Todo from Note");
    expect(items[1].dataset.action).toBe("delete");
    expect(items[1].textContent).toBe("Delete");
    ac.abort();
  });

  it("resolves with 'create-todo' when the first item is clicked and removes the DOM", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    wallDialogEl
      .querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="create-todo"]')!
      .click();
    const result = await p;
    expect(result).toBe("create-todo");
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with 'delete' when the Delete item is clicked", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    wallDialogEl
      .querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]')!
      .click();
    const result = await p;
    expect(result).toBe("delete");
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with null when user clicks outside the menu", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true }));
    const result = await p;
    expect(result).toBeNull();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with null when Escape is pressed", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    const result = await p;
    expect(result).toBeNull();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with null when the caller's AbortSignal fires (wall teardown)", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    ac.abort();
    const result = await p;
    expect(result).toBeNull();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with null synchronously when the signal is already aborted", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    ac.abort();
    const result = await openWallNoteContextMenu(100, 100, ac.signal);
    expect(result).toBeNull();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("resolves with null on scroll (defensive dismissal)", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal);
    await flushPromises();

    window.dispatchEvent(new Event("scroll"));
    const result = await p;
    expect(result).toBeNull();
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("hides the Create Todo item when showCreateTodo is false and only renders Delete", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    void openWallNoteContextMenu(100, 100, ac.signal, { showCreateTodo: false });
    await flushPromises();

    const menu = wallDialogEl.querySelector(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    expect(menu!.querySelector('[data-action="create-todo"]')).toBeNull();
    const items = menu!.querySelectorAll<HTMLButtonElement>(".context-menu__item");
    expect(items).toHaveLength(1);
    expect(items[0].dataset.action).toBe("delete");
    ac.abort();
  });

  it("uses the overridden deleteLabel for the Delete button", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    void openWallNoteContextMenu(100, 100, ac.signal, {
      showCreateTodo: false,
      deleteLabel: "Delete 3 notes",
    });
    await flushPromises();

    const deleteBtn = wallDialogEl.querySelector<HTMLButtonElement>(
      '.wall-note-context-menu [data-action="delete"]',
    );
    expect(deleteBtn).toBeTruthy();
    expect(deleteBtn!.textContent).toBe("Delete 3 notes");
    ac.abort();
  });

  it("resolves with 'delete' when the group-mode Delete button is clicked", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    const p = openWallNoteContextMenu(100, 100, ac.signal, {
      showCreateTodo: false,
      deleteLabel: "Delete 2 notes",
    });
    await flushPromises();

    wallDialogEl
      .querySelector<HTMLButtonElement>('.wall-note-context-menu [data-action="delete"]')!
      .click();
    const result = await p;
    expect(result).toBe("delete");
    expect(wallDialogEl.querySelector(".wall-note-context-menu")).toBeNull();
  });

  it("clamps the menu's left/top to stay inside the viewport", async () => {
    const { openWallNoteContextMenu } = await import("./wall-note-context-menu.js");
    const ac = new AbortController();
    void openWallNoteContextMenu(100000, 100000, ac.signal);
    await flushPromises();

    const menu = wallDialogEl.querySelector<HTMLElement>(".wall-note-context-menu");
    expect(menu).toBeTruthy();
    const left = parseInt(menu!.style.left, 10);
    const top = parseInt(menu!.style.top, 10);
    expect(Number.isFinite(left)).toBe(true);
    expect(Number.isFinite(top)).toBe(true);
    expect(left).toBeLessThanOrEqual(window.innerWidth);
    expect(top).toBeLessThanOrEqual(window.innerHeight);
    ac.abort();
  });
});
