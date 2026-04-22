// @vitest-environment happy-dom
//
// Gesture × modality regression matrix for the wall feature.
//
// Each `it(...)` name follows the format
//   "gesture × modality → expected API"
// so the table of scenarios is scannable at a glance.
//
// Timing-sensitive scenarios use `vi.useFakeTimers()` rather than real
// timers so the suite is deterministic; DOM update scenarios use the
// `flushRaf()` helper from `wall-test-harness` to advance one rAF tick.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  dispatchMouse,
  dispatchPointer,
  flushPromises,
  flushRaf,
  installDialogPolyfill,
  makeNote,
  makeWallDoc,
  rect,
  setupWallDom,
  type TestNote,
} from "./wall-test-harness.js";

const apiFetchMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());
const onMock = vi.hoisted(() => vi.fn());
const offMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());

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

// Default notes doc used by most scenarios.
function defaultNotes(): TestNote[] {
  return [
    makeNote({
      id: "n1",
      x: 20,
      y: 20,
      width: 160,
      height: 100,
      color: "#B0E0E6",
      text: "Hello",
      version: 1,
    }),
  ];
}

/** Find the most recent PATCH call to /wall/notes/{id}. */
function findLastPatch(id: string): { url: string; body: Record<string, unknown> } | null {
  for (let i = apiFetchMock.mock.calls.length - 1; i >= 0; i -= 1) {
    const [url, init] = apiFetchMock.mock.calls[i];
    if (
      typeof url === "string" &&
      url.includes(`/wall/notes/${id}`) &&
      (init as RequestInit | undefined)?.method === "PATCH"
    ) {
      return {
        url,
        body: (init as RequestInit).body
          ? JSON.parse(String((init as RequestInit).body))
          : {},
      };
    }
  }
  return null;
}

function patchCalls(): Array<{ url: string; body: Record<string, unknown> }> {
  const out: Array<{ url: string; body: Record<string, unknown> }> = [];
  for (const [url, init] of apiFetchMock.mock.calls) {
    if (
      typeof url === "string" &&
      url.includes("/wall/notes/") &&
      (init as RequestInit | undefined)?.method === "PATCH"
    ) {
      out.push({
        url,
        body: (init as RequestInit).body
          ? JSON.parse(String((init as RequestInit).body))
          : {},
      });
    }
  }
  return out;
}

async function openWall(notes: TestNote[] = defaultNotes()) {
  apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    if (typeof url === "string" && url.endsWith("/wall") && !init?.method) {
      return makeWallDoc(notes);
    }
    if (init?.method === "DELETE") return {};
    if (init?.method === "PATCH") {
      const body = init.body ? JSON.parse(String(init.body)) : {};
      const target = notes[0];
      return {
        ...target,
        ...body,
        version: (target.version ?? 1) + 1,
      };
    }
    if (init?.method === "POST" && typeof url === "string" && url.endsWith("/wall/notes")) {
      const body = init.body ? JSON.parse(String(init.body)) : {};
      return { id: "created-1", version: 1, text: "", ...body };
    }
    if (init?.method === "POST" && typeof url === "string" && url.endsWith("/wall/edges")) {
      const body = init.body ? JSON.parse(String(init.body)) : {};
      return { id: "e1", from: body.from, to: body.to };
    }
    return {};
  });

  const mod = await import("./wall.js");
  await mod.openWallDialog({ projectId: 1, slug: "alpha", role: "maintainer" });
  await flushPromises();
  return mod;
}

function getNoteEl(id: string): HTMLElement {
  const el = wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="${id}"]`);
  if (!el) throw new Error(`missing note element for ${id}`);
  return el;
}

describe("wall gesture × modality regression matrix", () => {
  beforeEach(() => {
    vi.resetModules();
    installDialogPolyfill();
    setupWallDom({ wallDialogEl, wallSurfaceEl, closeWallBtnEl, wallTrashEl });
    apiFetchMock.mockReset();
    confirmDeleteMock.mockReset();
    onMock.mockReset();
    offMock.mockReset();
    showToastMock.mockReset();
  });

  afterEach(() => {
    if (wallDialogEl.open) wallDialogEl.close();
    vi.useRealTimers();
  });

  it("create note × right-click empty canvas → POST /wall/notes", async () => {
    await openWall([]);

    // Canvas is empty; the surface itself receives the contextmenu.
    wallSurfaceEl.getBoundingClientRect = () => rect(0, 0, 800, 600);
    dispatchMouse(wallSurfaceEl, "contextmenu", { button: 2, clientX: 200, clientY: 150 });
    await flushPromises();

    const call = apiFetchMock.mock.calls.find(
      ([url, init]) =>
        typeof url === "string" &&
        url === "/api/board/alpha/wall/notes" &&
        (init as RequestInit | undefined)?.method === "POST",
    );
    expect(call).toBeTruthy();
    const body = JSON.parse(String((call?.[1] as RequestInit).body));
    expect(body.x).toBe(200);
    expect(body.y).toBe(150);
  });

  it("color cycle × single-click on note → PATCH with color", async () => {
    vi.useFakeTimers();
    await openWall();

    const noteEl = getNoteEl("n1");
    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 32, clientY: 32 });
    dispatchPointer(document, "pointerup", { button: 0, clientX: 32, clientY: 32 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    const patch = findLastPatch("n1");
    expect(patch?.body.color).toBeDefined();
    expect(typeof patch?.body.color).toBe("string");
  });

  it("color cycle cancelled by dblclick × two fast clicks → no color PATCH, edit mode entered", async () => {
    vi.useFakeTimers();
    await openWall();

    const noteEl = getNoteEl("n1");
    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 32, clientY: 32 });
    dispatchPointer(document, "pointerup", { button: 0, clientX: 32, clientY: 32 });
    dispatchMouse(noteEl, "dblclick", { button: 0, clientX: 32, clientY: 32 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    const colorPatches = patchCalls().filter((p) => "color" in p.body);
    expect(colorPatches).toHaveLength(0);
    expect(noteEl.querySelector("textarea.wall-note__editor")).toBeTruthy();
  });

  it("color cycle cancelled by drag × pointerdown+move → no color PATCH, x/y PATCH", async () => {
    vi.useFakeTimers();
    await openWall();

    const noteEl = getNoteEl("n1");
    wallSurfaceEl.getBoundingClientRect = () => rect(0, 0, 800, 600);
    noteEl.getBoundingClientRect = () => rect(20, 20, 160, 100);

    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 50, clientY: 50 });
    // First pointermove promotes arm→drag; controller attaches its own listeners.
    dispatchPointer(document, "pointermove", { button: 0, clientX: 120, clientY: 120 });
    // Second pointermove reaches the drag controller's move handler and
    // actually updates the note's left/top via rAF.
    dispatchPointer(document, "pointermove", { button: 0, clientX: 130, clientY: 130 });
    await flushRaf();
    dispatchPointer(document, "pointerup", { button: 0, clientX: 130, clientY: 130 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    const colorPatches = patchCalls().filter((p) => "color" in p.body);
    expect(colorPatches).toHaveLength(0);
    const posPatches = patchCalls().filter((p) => "x" in p.body && "y" in p.body);
    expect(posPatches.length).toBeGreaterThan(0);
  });

  it("drag transient coalescing × repeated rAF ticks → throttled POSTs within DRAG_TRANSIENT_COALESCE_MS window (Phase 1)", async () => {
    vi.useFakeTimers();
    await openWall();

    const noteEl = getNoteEl("n1");
    wallSurfaceEl.getBoundingClientRect = () => rect(0, 0, 800, 600);
    noteEl.getBoundingClientRect = () => rect(20, 20, 160, 100);

    const transientPostsAt = () =>
      apiFetchMock.mock.calls.filter(
        ([url, init]) =>
          typeof url === "string" &&
          url.endsWith("/wall/transient") &&
          (init as RequestInit | undefined)?.method === "POST",
      ).length;

    dispatchPointer(noteEl, "pointerdown", { button: 0, clientX: 50, clientY: 50 });
    // First pointermove promotes arm→drag; second reaches the drag
    // controller's move handler and triggers the rAF + initial group flush.
    dispatchPointer(document, "pointermove", { button: 0, clientX: 120, clientY: 120 });
    dispatchPointer(document, "pointermove", { button: 0, clientX: 125, clientY: 125 });
    await flushRaf();
    const afterFirstFrame = transientPostsAt();
    expect(afterFirstFrame).toBeGreaterThanOrEqual(1);

    // Several rapid rAF ticks within the coalescing window: the group-timer
    // is armed exactly once; no new POSTs fire until the window elapses.
    dispatchPointer(document, "pointermove", { button: 0, clientX: 140, clientY: 140 });
    await flushRaf();
    dispatchPointer(document, "pointermove", { button: 0, clientX: 160, clientY: 160 });
    await flushRaf();
    dispatchPointer(document, "pointermove", { button: 0, clientX: 180, clientY: 180 });
    await flushRaf();
    expect(transientPostsAt()).toBe(afterFirstFrame);

    // Drop: final position flush is posted explicitly via scheduleTransient +
    // flushTransient, independent of the group timer (which was cleared in
    // onUp). This proves the drop path still posts the final coalesced state.
    dispatchPointer(document, "pointerup", { button: 0, clientX: 180, clientY: 180 });
    vi.advanceTimersByTime(500);
    await flushPromises();
    expect(transientPostsAt()).toBeGreaterThanOrEqual(afterFirstFrame + 1);
  });

  it("right-click delete on note × confirm=true → DELETE /wall/notes/{id}", async () => {
    confirmDeleteMock.mockResolvedValue(true);
    await openWall();
    const noteEl = getNoteEl("n1");

    dispatchMouse(noteEl, "contextmenu", { button: 2, clientX: 30, clientY: 30 });
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this note?");
    expect(apiFetchMock).toHaveBeenCalledWith(
      "/api/board/alpha/wall/notes/n1",
      { method: "DELETE" },
    );
  });

  it("right-click delete on note × confirm=false → no DELETE, no color PATCH (3.14.x regression)", async () => {
    vi.useFakeTimers();
    confirmDeleteMock.mockResolvedValue(false);
    await openWall();
    const noteEl = getNoteEl("n1");

    dispatchMouse(noteEl, "contextmenu", { button: 2, clientX: 30, clientY: 30 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    expect(
      apiFetchMock.mock.calls.some(
        ([url, init]) =>
          typeof url === "string" &&
          url.includes("/wall/notes/n1") &&
          (init as RequestInit | undefined)?.method === "DELETE",
      ),
    ).toBe(false);
    const colorPatches = patchCalls().filter((p) => "color" in p.body);
    expect(colorPatches).toHaveLength(0);
  });

  it("right-click delete on note × full pointer sequence → no color PATCH even after DOUBLE_TAP_MS", async () => {
    // Real browsers fire pointerdown → contextmenu → pointerup around a
    // right-click. Without the primary-button guard in the pointerdown
    // handler, pointerdown arms armNoteInteraction which then schedules
    // a color-cycle timer on pointerup, racing the delete confirm. This
    // characterization replays the full sequence so the bug cannot hide
    // again behind a bare contextmenu dispatch.
    vi.useFakeTimers();
    confirmDeleteMock.mockResolvedValue(false);
    await openWall();
    const noteEl = getNoteEl("n1");

    dispatchPointer(noteEl, "pointerdown", { button: 2, clientX: 30, clientY: 30 });
    dispatchMouse(noteEl, "contextmenu", { button: 2, clientX: 30, clientY: 30 });
    dispatchPointer(document, "pointerup", { button: 2, clientX: 30, clientY: 30 });
    vi.advanceTimersByTime(500);
    await flushPromises();

    const colorPatches = patchCalls().filter((p) => "color" in p.body);
    expect(colorPatches).toHaveLength(0);
  });

  it("marquee select × empty canvas drag → no API calls, selection populated", async () => {
    await openWall();

    wallSurfaceEl.getBoundingClientRect = () => rect(0, 0, 800, 600);
    dispatchPointer(wallSurfaceEl, "pointerdown", { button: 0, clientX: 5, clientY: 5 });
    dispatchPointer(document, "pointermove", { button: 0, clientX: 400, clientY: 400 });
    dispatchPointer(document, "pointerup", { button: 0, clientX: 400, clientY: 400 });
    await flushPromises();

    const writeCalls = apiFetchMock.mock.calls.filter(([_, init]) => {
      const m = (init as RequestInit | undefined)?.method;
      return m === "POST" || m === "PATCH" || m === "DELETE";
    });
    expect(writeCalls).toHaveLength(0);
    const note = getNoteEl("n1");
    expect(note.classList.contains("wall-note--selected")).toBe(true);
  });

  it("SSE wall:transient × other user → note moves, no local API calls", async () => {
    await openWall();
    const noteEl = getNoteEl("n1");
    const { applyTransient } = await import("./wall-realtime.js");

    apiFetchMock.mockClear();
    applyTransient(
      { noteId: "n1", userId: 99, x: 444, y: 333 },
      (id) => wallSurfaceEl.querySelector<HTMLElement>(`.wall-note[data-note-id="${id}"]`),
    );

    expect(noteEl.style.left).toBe("444px");
    expect(noteEl.style.top).toBe("333px");
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("SSE wall:refresh_needed × during edit → deferred until edit commits", async () => {
    await openWall();
    const noteEl = getNoteEl("n1");

    // Enter edit mode via dblclick.
    dispatchMouse(noteEl, "dblclick", { button: 0, clientX: 32, clientY: 32 });
    await flushPromises();

    const ta = noteEl.querySelector<HTMLTextAreaElement>("textarea.wall-note__editor");
    expect(ta).toBeTruthy();

    apiFetchMock.mockClear();

    // Trigger refetch while editing: should be deferred (no GET fired).
    const { refetchDoc } = await import("./wall-realtime.js");
    await refetchDoc({ onApplyDoc: () => { /* noop */ } });
    expect(apiFetchMock).not.toHaveBeenCalled();

    // Commit the edit (blur without text change still flushes the pending refetch).
    ta!.dispatchEvent(new Event("blur"));
    await flushPromises();

    // The deferred refetch fires now; it should hit the GET /wall endpoint.
    const getCall = apiFetchMock.mock.calls.find(
      ([url, init]) =>
        typeof url === "string" && url.endsWith("/wall") && !(init as RequestInit | undefined)?.method,
    );
    expect(getCall).toBeTruthy();
  });
});
