// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  dispatchPointer,
  flushPromises,
  flushRaf,
  initWallTestI18n,
  installDialogPolyfill,
  makeNote,
} from "./wall-test-harness.js";
import { renderEdges } from "./wall-rendering.js";
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

function edgeDoc() {
  return {
    notes: [
      makeNote({ id: "n1", x: 20, y: 20, width: 160, height: 100, text: "one" }),
      makeNote({ id: "n2", x: 260, y: 20, width: 160, height: 100, text: "two" }),
    ],
    edges: [],
    stories: [
      { localId: 7, x: 80, y: 300, version: 1, todo: { id: 70, localId: 7, title: "Seven", status: "BACKLOG", tags: [] as string[] } },
      { localId: 8, x: 420, y: 300, version: 1, todo: { id: 80, localId: 8, title: "Eight", status: "BACKLOG", tags: [] as string[] } },
    ],
    version: 1,
  };
}

function dragEdgeDoc() {
  const doc = edgeDoc();
  return {
    ...doc,
    edges: [
      { id: "e1", from: "n1", to: "story:7" },
      { id: "e2", from: "story:7", to: "story:8" },
      { id: "e3", from: "n1", to: "n2" },
    ],
  };
}

async function openWall(role = "maintainer", doc = edgeDoc()) {
  apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (typeof url === "string" && url.endsWith("/wall") && !init?.method) {
      return doc;
    }
    if (typeof url === "string" && init?.method === "POST" && url.endsWith("/wall/edges")) {
      const body = init.body ? JSON.parse(String(init.body)) : {};
      return { id: "e1", from: body.from, to: body.to };
    }
    return {};
  });
  (document as Document & { elementFromPoint?: (x: number, y: number) => Element | null }).elementFromPoint =
    () => null;

  const mod = await import("./wall.js");
  await mod.openWallDialog({ projectId: 1, slug: "alpha", role });
  await flushPromises();
}

function getNoteEl(id: string): HTMLElement {
  const el = wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="${id}"]`);
  if (!el) throw new Error(`missing note ${id}`);
  return el;
}

function getStoryEl(localId: number): HTMLElement {
  const el = wallSurfaceEl.querySelector<HTMLElement>(`.wall-story[data-story-local-id="${localId}"]`);
  if (!el) throw new Error(`missing story ${localId}`);
  return el;
}

function edgePosts(): Array<{ from: string; to: string }> {
  return apiFetchMock.mock.calls
    .filter(([url, init]: [string, RequestInit | undefined]) =>
      url.endsWith("/wall/edges") && init?.method === "POST",
    )
    .map(([, init]: [string, RequestInit | undefined]) => JSON.parse(String(init?.body)));
}

function storyPatchPosts(localId: number): number {
  return apiFetchMock.mock.calls.filter(([url, init]: [string, RequestInit | undefined]) =>
    url.endsWith(`/wall/stories/${localId}`) && init?.method === "PATCH",
  ).length;
}

function setDropTarget(el: Element | null): void {
  (document as Document & { elementFromPoint?: (x: number, y: number) => Element | null }).elementFromPoint =
    () => el;
}

function shiftDrag(fromEl: HTMLElement, toEl: Element | null): void {
  setDropTarget(toEl);
  dispatchPointer(fromEl, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 50, clientY: 50 });
  dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 300, clientY: 300 });
  dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 300, clientY: 300 });
}

function previewLine(): Element | null {
  return wallSurfaceEl.querySelector(".wall-edge-preview");
}

function trackStoryLayout(localId: number, w: number, h: number): void {
  const el = getStoryEl(localId);
  Object.defineProperty(el, "offsetLeft", { configurable: true, get: () => parseInt(el.style.left || "0", 10) });
  Object.defineProperty(el, "offsetTop", { configurable: true, get: () => parseInt(el.style.top || "0", 10) });
  Object.defineProperty(el, "offsetWidth", { configurable: true, value: w });
  Object.defineProperty(el, "offsetHeight", { configurable: true, value: h });
}

function edgeLine(edgeId: string): SVGLineElement {
  const line = wallSurfaceEl.querySelector(`.wall-edge-group[data-edge-id="${edgeId}"] .wall-edge-line`);
  if (!line) throw new Error(`missing edge ${edgeId}`);
  return line as SVGLineElement;
}

function lineXY(edgeId: string): [string | null, string | null, string | null, string | null] {
  const line = edgeLine(edgeId);
  return [line.getAttribute("x1"), line.getAttribute("y1"), line.getAttribute("x2"), line.getAttribute("y2")];
}

describe("wall edge gesture across endpoint types", () => {
  beforeEach(async () => {
    vi.resetModules();
    await initWallTestI18n({ en: enCatalog as Record<string, string> });
    installDialogPolyfill();
    document.body.innerHTML = "";
    wallDialogEl.innerHTML = "";
    wallSurfaceEl.innerHTML = "";
    wallDialogEl.appendChild(wallSurfaceEl);
    document.body.appendChild(wallDialogEl);
    document.body.appendChild(closeWallBtnEl);
    document.body.appendChild(wallTrashEl);
    localStorage.clear();
    apiFetchMock.mockReset();
    confirmDeleteMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
    openTodoDialogMock.mockReset();
    openTodoDialogMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    if ((wallDialogEl as HTMLDialogElement).open) {
      (wallDialogEl as HTMLDialogElement).close();
    }
    vi.useRealTimers();
  });

  it("note to note still posts the same edge request", async () => {
    await openWall();
    shiftDrag(getNoteEl("n1"), getNoteEl("n2"));
    await flushPromises();

    expect(edgePosts()).toEqual([{ from: "n1", to: "n2" }]);
  });

  it("note to story posts the note id and canonical story endpoint", async () => {
    await openWall();
    shiftDrag(getNoteEl("n1"), getStoryEl(7));
    await flushPromises();

    expect(edgePosts()).toEqual([{ from: "n1", to: "story:7" }]);
  });

  it("story to note posts the canonical story endpoint and note id", async () => {
    await openWall();
    shiftDrag(getStoryEl(7), getNoteEl("n2"));
    await flushPromises();

    expect(edgePosts()).toEqual([{ from: "story:7", to: "n2" }]);
  });

  it("story to story posts both canonical story endpoints", async () => {
    await openWall();
    shiftDrag(getStoryEl(7), getStoryEl(8));
    await flushPromises();

    expect(edgePosts()).toEqual([{ from: "story:7", to: "story:8" }]);
  });

  it("story to itself does not post", async () => {
    await openWall();
    shiftDrag(getStoryEl(7), getStoryEl(7));
    await flushPromises();

    expect(edgePosts()).toEqual([]);
  });

  it("shift story drag does not open the todo", async () => {
    await openWall();
    shiftDrag(getStoryEl(7), getNoteEl("n2"));
    await flushPromises();

    expect(openTodoDialogMock).not.toHaveBeenCalled();
  });

  it("shift story drag does not move the story", async () => {
    await openWall();
    shiftDrag(getStoryEl(7), getNoteEl("n2"));
    await flushPromises();

    expect(storyPatchPosts(7)).toBe(0);
  });

  it("plain story click still opens the todo", async () => {
    await openWall();
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    await flushPromises();

    await vi.waitFor(() => {
      expect(openTodoDialogMock).toHaveBeenCalledWith(expect.objectContaining({
        mode: "edit",
        todo: expect.objectContaining({ localId: 7 }),
      }));
    });
  });

  it("plain story drag still moves the story", async () => {
    await openWall();
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    await flushPromises();

    expect(storyPatchPosts(7)).toBeGreaterThan(0);
    expect(edgePosts()).toEqual([]);
  });

  it("viewer shift story drag posts no edge", async () => {
    await openWall("viewer");
    shiftDrag(getStoryEl(7), getNoteEl("n1"));
    await flushPromises();

    expect(edgePosts()).toEqual([]);
  });

  it("dropping on empty canvas cancels without posting", async () => {
    await openWall();
    shiftDrag(getNoteEl("n1"), wallSurfaceEl);
    await flushPromises();

    expect(edgePosts()).toEqual([]);
  });

  it("pointercancel removes the preview without posting or moving state", async () => {
    await openWall();
    // A valid different endpoint sits under the cursor, but cancellation
    // must never hit-test or create an edge.
    setDropTarget(getNoteEl("n2"));
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 300, clientY: 300 });
    expect(previewLine()).not.toBeNull();
    dispatchPointer(document, "pointercancel", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 300, clientY: 300 });
    await flushPromises();

    expect(previewLine()).toBeNull();
    expect(edgePosts()).toEqual([]);
    expect(storyPatchPosts(7)).toBe(0);
    expect(openTodoDialogMock).not.toHaveBeenCalled();
  });

  it("removes the preview line after successful and cancelled drops", async () => {
    await openWall();
    const n1 = getNoteEl("n1");
    setDropTarget(getNoteEl("n2"));
    dispatchPointer(n1, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 50, clientY: 50 });
    expect(previewLine()).not.toBeNull();
    dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 300, clientY: 300 });
    await flushPromises();
    expect(previewLine()).toBeNull();

    setDropTarget(wallSurfaceEl);
    dispatchPointer(n1, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 50, clientY: 50 });
    expect(previewLine()).not.toBeNull();
    dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", shiftKey: true, clientX: 10, clientY: 500 });
    await flushPromises();
    expect(previewLine()).toBeNull();
  });

  it("local story drag moves incident note to story edges during the drag", async () => {
    await openWall("maintainer", dragEdgeDoc());
    trackStoryLayout(7, 280, 200);
    trackStoryLayout(8, 280, 200);
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    await flushRaf();

    // Painted to (110,90); rendered 280x200 box centers at (250,190).
    // No pointerup yet, so no PATCH has committed.
    expect(lineXY("e1").slice(2)).toEqual(["250", "190"]);
    expect(storyPatchPosts(7)).toBe(0);
    expect(openTodoDialogMock).not.toHaveBeenCalled();
  });

  it("local story drag moves incident story to story edges", async () => {
    await openWall("maintainer", dragEdgeDoc());
    trackStoryLayout(7, 280, 200);
    trackStoryLayout(8, 280, 200);
    // Re-resolve initial geometry now that layout is observable.
    renderEdges(wallSurfaceEl, dragEdgeDoc().edges, 1);
    expect(lineXY("e2").slice(2)).toEqual(["560", "400"]);
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    await flushRaf();

    expect(lineXY("e2").slice(0, 2)).toEqual(["250", "190"]);
    expect(lineXY("e2").slice(2)).toEqual(["560", "400"]);
  });

  it("local story drag leaves unrelated edges unchanged", async () => {
    await openWall("maintainer", dragEdgeDoc());
    trackStoryLayout(7, 280, 200);
    trackStoryLayout(8, 280, 200);
    const before = lineXY("e3");
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    await flushRaf();

    expect(lineXY("e3")).toEqual(before);
  });

  it("pointerup leaves the edge at the final story center", async () => {
    await openWall("maintainer", dragEdgeDoc());
    trackStoryLayout(7, 280, 200);
    trackStoryLayout(8, 280, 200);
    const story = getStoryEl(7);
    dispatchPointer(story, "pointerdown", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 90, clientY: 310 });
    dispatchPointer(document, "pointermove", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    await flushRaf();
    dispatchPointer(document, "pointerup", { button: 0, pointerId: 1, pointerType: "mouse", clientX: 200, clientY: 400 });
    // The pointerup flush paints the final position synchronously, before
    // the async PATCH commit and its element replacement below.
    expect(lineXY("e1").slice(2)).toEqual(["250", "190"]);
    expect(lineXY("e2").slice(0, 2)).toEqual(["250", "190"]);
    await flushPromises();

    expect(storyPatchPosts(7)).toBeGreaterThan(0);
  });

  it("unpin removes the story and its incident local edges immediately", async () => {
    await openWall("maintainer", dragEdgeDoc());
    expect(wallSurfaceEl.querySelectorAll(".wall-edge-group").length).toBe(3);
    const story = getStoryEl(7);
    story.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2 }));
    await flushPromises();
    wallDialogEl.querySelector<HTMLButtonElement>('[data-action="remove"]')?.click();
    await flushPromises();

    expect(apiFetchMock).toHaveBeenCalledWith("/api/board/alpha/wall/stories/7", { method: "DELETE" });
    expect(wallSurfaceEl.querySelector('.wall-story[data-story-local-id="7"]')).toBeNull();
    expect(wallSurfaceEl.querySelector('.wall-story[data-story-local-id="8"]')).not.toBeNull();
    const groups = wallSurfaceEl.querySelectorAll(".wall-edge-group");
    expect(groups.length).toBe(1);
    expect((groups[0] as SVGGElement).dataset.edgeId).toBe("e3");
  });
});
