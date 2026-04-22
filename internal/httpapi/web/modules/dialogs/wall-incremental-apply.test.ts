// @vitest-environment happy-dom
//
// Phase 3 characterization tests for the incremental single-note apply
// path in `refetchDoc`. The fast path bypasses a full `renderSurface()`
// rebuild when the incoming doc differs from the current doc only in
// per-note field values, and falls back to a full rebuild for any
// structural change (add/remove note or edge, reorder edge endpoints).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  dispatchPointer,
  flushPromises,
  installDialogPolyfill,
  makeNote,
  makeWallDoc,
  setupWallDom,
  type TestEdge,
  type TestNote,
} from "./wall-test-harness.js";

const apiFetchMock = vi.hoisted(() => vi.fn());
const onMock = vi.hoisted(() => vi.fn());
const offMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());

const wallDialogEl = vi.hoisted(() => document.createElement("dialog") as HTMLDialogElement);
const wallSurfaceEl = vi.hoisted(() => document.createElement("div"));
const closeWallBtnEl = vi.hoisted(() => document.createElement("button"));
const wallTrashEl = vi.hoisted(() => document.createElement("div"));

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

import {
  __diffWallDocForTest,
  __getWallRenderCounters,
  __resetWallRenderCounters,
  openWallDialog,
} from "./wall.js";

function defaultNote(overrides: Partial<TestNote> = {}): TestNote {
  return makeNote({
    id: "n1",
    x: 20,
    y: 20,
    width: 160,
    height: 100,
    color: "#B0E0E6",
    text: "Hello",
    version: 1,
    ...overrides,
  });
}

/** Install an apiFetch mock whose GET /wall response can be swapped between fetches. */
function mountFetchSwitch(initialNotes: TestNote[], initialEdges: TestEdge[] = []) {
  let currentNotes = initialNotes;
  let currentEdges = initialEdges;
  apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (typeof url === "string" && url.endsWith("/wall") && !init?.method) {
      return makeWallDoc(currentNotes, currentEdges);
    }
    return {};
  });
  return {
    setDoc(notes: TestNote[], edges: TestEdge[] = []) {
      currentNotes = notes;
      currentEdges = edges;
    },
  };
}

async function openWallWith(notes: TestNote[], edges: TestEdge[] = []) {
  const ctrl = mountFetchSwitch(notes, edges);
  await openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
  await flushPromises();
  return ctrl;
}

describe("Phase 3 · diffWallDoc pure helper", () => {
  const baseNote = defaultNote();

  it("returns noop when both docs are identical", () => {
    const a = { notes: [{ ...baseNote }], edges: [], version: 1 };
    const b = { notes: [{ ...baseNote }], edges: [], version: 1 };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "noop" });
  });

  it("returns incremental with only the changed note when a single field differs", () => {
    const a = { notes: [{ ...baseNote }], edges: [], version: 1 };
    const b = {
      notes: [{ ...baseNote, text: "Edited", version: 2 }],
      edges: [],
      version: 2,
    };
    const diff = __diffWallDocForTest(a, b);
    expect(diff.kind).toBe("incremental");
    if (diff.kind !== "incremental") throw new Error("unreachable");
    expect(diff.changedNotes).toHaveLength(1);
    expect(diff.changedNotes[0]!.text).toBe("Edited");
  });

  it("returns incremental touching only the notes that actually moved", () => {
    const n1 = defaultNote({ id: "n1", x: 10 });
    const n2 = defaultNote({ id: "n2", x: 200 });
    const a = { notes: [{ ...n1 }, { ...n2 }], edges: [], version: 1 };
    const b = {
      notes: [{ ...n1 }, { ...n2, x: 250, version: 2 }],
      edges: [],
      version: 2,
    };
    const diff = __diffWallDocForTest(a, b);
    expect(diff.kind).toBe("incremental");
    if (diff.kind !== "incremental") throw new Error("unreachable");
    expect(diff.changedNotes.map((n) => n.id)).toEqual(["n2"]);
  });

  it("returns full when a note is added", () => {
    const a = { notes: [{ ...baseNote }], edges: [], version: 1 };
    const b = {
      notes: [{ ...baseNote }, defaultNote({ id: "n2" })],
      edges: [],
      version: 2,
    };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "full" });
  });

  it("returns full when a note is removed", () => {
    const a = {
      notes: [{ ...baseNote }, defaultNote({ id: "n2" })],
      edges: [],
      version: 1,
    };
    const b = { notes: [{ ...baseNote }], edges: [], version: 2 };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "full" });
  });

  it("returns full when a note id changes (replace)", () => {
    const a = { notes: [{ ...baseNote }], edges: [], version: 1 };
    const b = { notes: [defaultNote({ id: "other" })], edges: [], version: 2 };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "full" });
  });

  it("returns full when an edge is added", () => {
    const a = {
      notes: [defaultNote({ id: "n1" }), defaultNote({ id: "n2" })],
      edges: [],
      version: 1,
    };
    const b = {
      notes: [defaultNote({ id: "n1" }), defaultNote({ id: "n2" })],
      edges: [{ id: "e1", from: "n1", to: "n2" }],
      version: 2,
    };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "full" });
  });

  it("returns full when an edge id is preserved but endpoints change", () => {
    const notes = [
      defaultNote({ id: "n1" }),
      defaultNote({ id: "n2" }),
      defaultNote({ id: "n3" }),
    ];
    const a = {
      notes,
      edges: [{ id: "e1", from: "n1", to: "n2" }],
      version: 1,
    };
    const b = {
      notes,
      edges: [{ id: "e1", from: "n1", to: "n3" }],
      version: 2,
    };
    expect(__diffWallDocForTest(a, b)).toEqual({ kind: "full" });
  });
});

describe("Phase 3 · refetchDoc fast path counters", () => {
  beforeEach(() => {
    vi.resetModules();
    installDialogPolyfill();
    setupWallDom({ wallDialogEl, wallSurfaceEl, closeWallBtnEl, wallTrashEl });
    apiFetchMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
    showToastMock.mockReset();
    confirmDeleteMock.mockReset();
    __resetWallRenderCounters();
  });

  afterEach(() => {
    if (wallDialogEl.open) wallDialogEl.close();
  });

  function fireRefreshNeeded() {
    // startRealtime subscribes to "wall:refresh_needed" via the mocked `on`.
    // We replay the registered handler synchronously; the real bus replays
    // SSE events through the same path.
    const call = onMock.mock.calls.find(([name]) => name === "wall:refresh_needed");
    if (!call) throw new Error("wall:refresh_needed handler not registered");
    const handler = call[1] as () => void;
    handler();
  }

  it("open → initial fetch takes the full-rebuild path (doc was empty)", async () => {
    // The dialog mounts with an empty doc (1 placeholder rebuild), then
    // the first GET /wall hits a note-count mismatch (0 → 1) and falls
    // back to another full rebuild. Neither step should hit the fast path.
    await openWallWith([defaultNote({ id: "n1", text: "A" })]);
    const counters = __getWallRenderCounters();
    expect(counters.fullRebuilds).toBeGreaterThanOrEqual(1);
    expect(counters.incrementalPatches).toBe(0);
  });

  it("text-only refresh → incremental patch, no DOM wipe (no blink)", async () => {
    vi.useFakeTimers();
    const ctrl = await openWallWith([defaultNote({ id: "n1", text: "A" })]);
    const noteEl = wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="n1"]`);
    expect(noteEl).toBeTruthy();
    const preCounters = __getWallRenderCounters();

    ctrl.setDoc([defaultNote({ id: "n1", text: "B", version: 2 })]);
    fireRefreshNeeded();
    // Phase 2 debounce: advance past the trailing window, then flush.
    vi.advanceTimersByTime(200);
    await flushPromises();

    const post = __getWallRenderCounters();
    expect(post.incrementalPatches).toBe(preCounters.incrementalPatches + 1);
    expect(post.fullRebuilds).toBe(preCounters.fullRebuilds);
    // Same DOM node survived the apply (no innerHTML wipe).
    const afterEl = wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="n1"]`);
    expect(afterEl).toBe(noteEl);
    const display = afterEl?.querySelector<HTMLElement>(".wall-note__display");
    expect(display?.textContent).toBe("B");
    vi.useRealTimers();
  });

  it("identical doc refresh → noop (incremental counter ticks, no DOM change)", async () => {
    vi.useFakeTimers();
    const initial = defaultNote({ id: "n1", text: "A" });
    const ctrl = await openWallWith([initial]);
    const preCounters = __getWallRenderCounters();

    ctrl.setDoc([{ ...initial }]);
    fireRefreshNeeded();
    vi.advanceTimersByTime(200);
    await flushPromises();

    const post = __getWallRenderCounters();
    expect(post.incrementalPatches).toBe(preCounters.incrementalPatches + 1);
    expect(post.fullRebuilds).toBe(preCounters.fullRebuilds);
    vi.useRealTimers();
  });

  it("note-added refresh → full rebuild fallback", async () => {
    vi.useFakeTimers();
    const ctrl = await openWallWith([defaultNote({ id: "n1" })]);
    const preCounters = __getWallRenderCounters();

    ctrl.setDoc([defaultNote({ id: "n1" }), defaultNote({ id: "n2", x: 300 })]);
    fireRefreshNeeded();
    vi.advanceTimersByTime(200);
    await flushPromises();

    const post = __getWallRenderCounters();
    expect(post.fullRebuilds).toBe(preCounters.fullRebuilds + 1);
    expect(post.incrementalPatches).toBe(preCounters.incrementalPatches);
    expect(wallSurfaceEl.querySelectorAll(".wall-note")).toHaveLength(2);
    vi.useRealTimers();
  });

  it("note-removed refresh → full rebuild fallback", async () => {
    vi.useFakeTimers();
    const ctrl = await openWallWith([
      defaultNote({ id: "n1" }),
      defaultNote({ id: "n2", x: 300 }),
    ]);
    const preCounters = __getWallRenderCounters();

    ctrl.setDoc([defaultNote({ id: "n1" })]);
    fireRefreshNeeded();
    vi.advanceTimersByTime(200);
    await flushPromises();

    const post = __getWallRenderCounters();
    expect(post.fullRebuilds).toBe(preCounters.fullRebuilds + 1);
    expect(post.incrementalPatches).toBe(preCounters.incrementalPatches);
    expect(wallSurfaceEl.querySelectorAll(".wall-note")).toHaveLength(1);
    vi.useRealTimers();
  });

  it("does not refetch while a drag is active; deferred refresh still fast-paths on drop", async () => {
    vi.useFakeTimers();
    const ctrl = await openWallWith([defaultNote({ id: "n1", text: "A" })]);
    const noteEl = wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="n1"]`)!;
    wallSurfaceEl.getBoundingClientRect = () => ({
      left: 0,
      top: 0,
      right: 800,
      bottom: 600,
      width: 800,
      height: 600,
      x: 0,
      y: 0,
      toJSON() { /* noop */ },
    }) as DOMRect;
    noteEl.getBoundingClientRect = () => ({
      left: 20,
      top: 20,
      right: 180,
      bottom: 120,
      width: 160,
      height: 100,
      x: 20,
      y: 20,
      toJSON() { /* noop */ },
    }) as DOMRect;

    // Start a drag so isDragActive() is true.
    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 50, clientY: 50 });
    dispatchPointer(document, "pointermove", { button: 0, clientX: 120, clientY: 120 });
    dispatchPointer(document, "pointermove", { button: 0, clientX: 125, clientY: 125 });

    const preCounters = __getWallRenderCounters();
    ctrl.setDoc([defaultNote({ id: "n1", text: "B", version: 2 })]);
    fireRefreshNeeded();
    vi.advanceTimersByTime(200);
    await flushPromises();

    // Drag still holds, so the debounced refresh must be deferred. No DOM
    // change so far.
    expect(__getWallRenderCounters()).toEqual(preCounters);

    // Release the drag; the re-armed debounce should fire and the fast
    // path should apply the text update.
    dispatchPointer(document, "pointerup", { button: 0, clientX: 125, clientY: 125 });
    vi.advanceTimersByTime(200);
    await flushPromises();

    const post = __getWallRenderCounters();
    expect(post.incrementalPatches).toBe(preCounters.incrementalPatches + 1);
    expect(post.fullRebuilds).toBe(preCounters.fullRebuilds);
    vi.useRealTimers();
  });
});
