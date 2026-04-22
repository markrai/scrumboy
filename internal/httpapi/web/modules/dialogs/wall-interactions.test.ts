// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());
const onMock = vi.hoisted(() => vi.fn());
const offMock = vi.hoisted(() => vi.fn());

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
}));

vi.mock("../dom/elements.js", () => ({
  wallDialog: wallDialogEl,
  wallSurface: wallSurfaceEl,
  closeWallBtn: closeWallBtnEl,
  wallTrash: wallTrashEl,
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

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

describe("wall interactions", () => {
  beforeEach(() => {
    vi.resetModules();
    installDialogPolyfill();
    setupDom();
    apiFetchMock.mockReset();
    confirmDeleteMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
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

  it("deletes a note on right-click when confirmation is accepted", async () => {
    confirmDeleteMock.mockResolvedValue(true);
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    const ev = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 });
    noteEl.dispatchEvent(ev);
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
  });

  it("does not delete a note on right-click when confirmation is cancelled", async () => {
    confirmDeleteMock.mockResolvedValue(false);
    const mod = await import("./wall.js");
    await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
    await flushPromises();

    const noteEl = wallSurfaceEl.querySelector(".wall-note");
    if (!(noteEl instanceof HTMLElement)) throw new Error("missing wall note");
    noteEl.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: 30, clientY: 30 }));
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/board/alpha/wall/notes/n1", { method: "DELETE" });
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
});
