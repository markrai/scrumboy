// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../api.js", () => ({ apiFetch: vi.fn() }));
vi.mock("../dom/elements.js", () => {
  const wallSurface = document.createElement("div");
  wallSurface.id = "wallSurface";
  document.body.appendChild(wallSurface);
  return {
    wallDialog: null,
    wallSurface,
    wallTrash: null,
    closeWallBtn: null,
  };
});

import { apiFetch } from "../api.js";
import {
  refetchDoc,
  applyTransient,
  startRealtime,
  __getRealtimeCounters,
  __resetRealtimeCounters,
  __getRefreshDebounceMs,
} from "./wall-realtime.js";
import { emit } from "../events.js";
import {
  setMounted,
  setActiveEditNoteId,
  setPendingRefetch,
  getPendingRefetch,
  resetEditGuards,
  setDragActive,
  type Mounted,
} from "./wall-state.js";

const mock = apiFetch as unknown as ReturnType<typeof vi.fn>;

function makeState(overrides: Partial<Mounted> = {}): Mounted {
  return {
    projectId: 1,
    slug: "abc",
    role: "editor" as any,
    canEdit: true,
    doc: { notes: [], edges: [], version: 0 } as any,
    userId: 42,
    onRefreshNeeded: () => { /* noop */ },
    onTransient: () => { /* noop */ },
    abort: new AbortController(),
    prevHtmlOverflow: "",
    transient: new Map(),
    colorTimers: new Map(),
    lastTapAt: new Map(),
    selected: new Set<string>(),
    ...overrides,
  } as Mounted;
}

describe("wall-realtime.refetchDoc", () => {
  beforeEach(() => {
    mock.mockReset();
    resetEditGuards();
    setMounted(null);
  });

  it("no-ops when nothing is mounted", async () => {
    const onApplyDoc = vi.fn();
    await refetchDoc({ onApplyDoc });
    expect(mock).not.toHaveBeenCalled();
    expect(onApplyDoc).not.toHaveBeenCalled();
  });

  it("defers (no GET) when an edit is in progress, and raises the pending flag", async () => {
    setMounted(makeState());
    setActiveEditNoteId("n1");
    const onApplyDoc = vi.fn();
    await refetchDoc({ onApplyDoc });
    expect(mock).not.toHaveBeenCalled();
    expect(onApplyDoc).not.toHaveBeenCalled();
    expect(getPendingRefetch()).toBe(true);
  });

  it("defers if an edit begins during the in-flight GET (second guard)", async () => {
    const state = makeState();
    setMounted(state);
    let resolveFetch: (v: any) => void = () => {};
    mock.mockImplementationOnce(() => new Promise((r) => { resolveFetch = r; }));
    const onApplyDoc = vi.fn();
    const p = refetchDoc({ onApplyDoc });
    // Edit starts while the GET is in flight.
    setActiveEditNoteId("n2");
    resolveFetch({ notes: [], edges: [], version: 1 });
    await p;
    expect(onApplyDoc).not.toHaveBeenCalled();
    expect(getPendingRefetch()).toBe(true);
  });

  it("applies the fetched doc when no edit is active", async () => {
    const state = makeState();
    setMounted(state);
    mock.mockResolvedValueOnce({ notes: [{ id: "x" }], edges: [], version: 2 });
    const onApplyDoc = vi.fn();
    await refetchDoc({ onApplyDoc });
    expect(onApplyDoc).toHaveBeenCalledWith(state, { notes: [{ id: "x" }], edges: [], version: 2 });
  });

  it("logs but does not throw when the GET rejects with a non-404", async () => {
    setMounted(makeState());
    const warn = vi.spyOn(console, "warn").mockImplementation(() => { /* noop */ });
    mock.mockRejectedValueOnce(new Error("boom"));
    await refetchDoc({ onApplyDoc: vi.fn() });
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});

describe("wall-realtime.applyTransient", () => {
  beforeEach(() => {
    resetEditGuards();
    setMounted(null);
  });

  function mountWithNote(id: string, opts: { dragging?: boolean } = {}): HTMLElement {
    const state = makeState();
    setMounted(state);
    const el = document.createElement("div");
    el.className = "wall-note" + (opts.dragging ? " wall-note--dragging" : "");
    el.dataset.noteId = id;
    el.style.left = "0px";
    el.style.top = "0px";
    // offsetWidth/offsetHeight are 0 in happy-dom; avoid NaN assertions.
    return el;
  }

  it("moves the element to the transient coordinates", () => {
    const el = mountWithNote("n1");
    const lookup = (id: string) => (id === "n1" ? el : null);
    applyTransient({ payload: { noteId: "n1", x: 42, y: 84, by: 7 } }, lookup);
    expect(el.style.left).toBe("42px");
    expect(el.style.top).toBe("84px");
  });

  it("ignores transients originated by the local user (echo suppression)", () => {
    setMounted(makeState({ userId: 7 }));
    const el = document.createElement("div");
    el.className = "wall-note";
    el.dataset.noteId = "n1";
    el.style.left = "1px";
    el.style.top = "2px";
    const lookup = (id: string) => (id === "n1" ? el : null);
    applyTransient({ payload: { noteId: "n1", x: 42, y: 84, by: 7 } }, lookup);
    expect(el.style.left).toBe("1px");
  });

  it("ignores notes the local user is currently dragging", () => {
    const el = mountWithNote("n1", { dragging: true });
    el.style.left = "1px";
    el.style.top = "2px";
    const lookup = (id: string) => (id === "n1" ? el : null);
    applyTransient({ payload: { noteId: "n1", x: 42, y: 84, by: 99 } }, lookup);
    expect(el.style.left).toBe("1px");
  });
});

describe("wall-realtime Phase 0 counters", () => {
  beforeEach(() => {
    mock.mockReset();
    resetEditGuards();
    setMounted(null);
    __resetRealtimeCounters();
  });

  it("increments refetchDocInvocations on each refetchDoc call", async () => {
    setMounted(makeState());
    mock.mockResolvedValue({ notes: [], edges: [], version: 1 });
    expect(__getRealtimeCounters().refetchDocInvocations).toBe(0);
    await refetchDoc({ onApplyDoc: () => { /* noop */ } });
    await refetchDoc({ onApplyDoc: () => { /* noop */ } });
    expect(__getRealtimeCounters().refetchDocInvocations).toBe(2);
  });

  it("does not increment refetchDocInvocations when deferred by an active edit", async () => {
    setMounted(makeState());
    setActiveEditNoteId("n1");
    await refetchDoc({ onApplyDoc: () => { /* noop */ } });
    expect(__getRealtimeCounters().refetchDocInvocations).toBe(0);
  });

  it("counts wall:refresh_needed events delivered via startRealtime", () => {
    vi.useFakeTimers();
    const stop = startRealtime({
      onRefreshNeeded: () => { /* noop */ },
      onTransient: () => { /* noop */ },
    });
    emit("wall:refresh_needed");
    emit("wall:refresh_needed");
    emit("wall:refresh_needed");
    expect(__getRealtimeCounters().refreshNeededReceived).toBe(3);
    stop();
    emit("wall:refresh_needed");
    expect(__getRealtimeCounters().refreshNeededReceived).toBe(3);
    vi.useRealTimers();
  });

  it("resets realtime counters via __resetRealtimeCounters", async () => {
    setMounted(makeState());
    mock.mockResolvedValue({ notes: [], edges: [], version: 1 });
    await refetchDoc({ onApplyDoc: () => { /* noop */ } });
    expect(__getRealtimeCounters().refetchDocInvocations).toBe(1);
    __resetRealtimeCounters();
    expect(__getRealtimeCounters()).toEqual({ refreshNeededReceived: 0, refetchDocInvocations: 0 });
  });
});

describe("wall-realtime Phase 2 refresh_needed debounce", () => {
  beforeEach(() => {
    mock.mockReset();
    resetEditGuards();
    setDragActive(false);
    setMounted(null);
    __resetRealtimeCounters();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("collapses a burst of wall:refresh_needed into exactly one callback after the window elapses", () => {
    vi.useFakeTimers();
    const onRefreshNeeded = vi.fn();
    const stop = startRealtime({
      onRefreshNeeded,
      onTransient: () => { /* noop */ },
    });

    for (let i = 0; i < 5; i += 1) emit("wall:refresh_needed");
    expect(onRefreshNeeded).not.toHaveBeenCalled();

    vi.advanceTimersByTime(__getRefreshDebounceMs() + 5);
    expect(onRefreshNeeded).toHaveBeenCalledTimes(1);
    expect(__getRealtimeCounters().refreshNeededReceived).toBe(5);

    stop();
  });

  it("holds the debounced refetch while a drag is active and flushes once after drag ends", () => {
    vi.useFakeTimers();
    const onRefreshNeeded = vi.fn();
    const stop = startRealtime({
      onRefreshNeeded,
      onTransient: () => { /* noop */ },
    });

    setDragActive(true);
    emit("wall:refresh_needed");
    emit("wall:refresh_needed");
    vi.advanceTimersByTime(__getRefreshDebounceMs() + 5);
    expect(onRefreshNeeded).not.toHaveBeenCalled();

    // Still no call while drag is active even across multiple windows.
    vi.advanceTimersByTime(__getRefreshDebounceMs() * 3);
    expect(onRefreshNeeded).not.toHaveBeenCalled();

    setDragActive(false);
    vi.advanceTimersByTime(__getRefreshDebounceMs() + 5);
    expect(onRefreshNeeded).toHaveBeenCalledTimes(1);

    stop();
  });

  it("stop() flushes a pending refresh when no guard is active", () => {
    vi.useFakeTimers();
    const onRefreshNeeded = vi.fn();
    const stop = startRealtime({
      onRefreshNeeded,
      onTransient: () => { /* noop */ },
    });

    emit("wall:refresh_needed");
    expect(onRefreshNeeded).not.toHaveBeenCalled();

    stop();
    expect(onRefreshNeeded).toHaveBeenCalledTimes(1);

    emit("wall:refresh_needed");
    vi.advanceTimersByTime(__getRefreshDebounceMs() + 5);
    expect(onRefreshNeeded).toHaveBeenCalledTimes(1);
  });

  it("stop() drops a pending refresh when a drag is still active (no blowup on teardown-during-drag)", () => {
    vi.useFakeTimers();
    const onRefreshNeeded = vi.fn();
    const stop = startRealtime({
      onRefreshNeeded,
      onTransient: () => { /* noop */ },
    });

    setDragActive(true);
    emit("wall:refresh_needed");
    stop();
    expect(onRefreshNeeded).not.toHaveBeenCalled();
    setDragActive(false);
  });

  it("each startRealtime registration has its own independent debounce timer", () => {
    vi.useFakeTimers();
    const onA = vi.fn();
    const onB = vi.fn();
    const stopA = startRealtime({ onRefreshNeeded: onA, onTransient: () => { /* noop */ } });
    emit("wall:refresh_needed");
    stopA();

    const stopB = startRealtime({ onRefreshNeeded: onB, onTransient: () => { /* noop */ } });
    emit("wall:refresh_needed");
    vi.advanceTimersByTime(__getRefreshDebounceMs() + 5);

    expect(onA).toHaveBeenCalledTimes(1);
    expect(onB).toHaveBeenCalledTimes(1);
    stopB();
  });
});
