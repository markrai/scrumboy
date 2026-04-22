// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../api.js", () => {
  const apiFetch = vi.fn();
  return { apiFetch };
});

import { apiFetch } from "../api.js";
import {
  __getTransientFailureCount,
  __getTransientPostsSent,
  __resetTransientFailureState,
  createEdgeRemote,
  createNote,
  deleteEdgeRemote,
  deleteNoteRemote,
  fetchWall,
  patchNoteRemote,
  postTransient,
} from "./wall-api.js";

const mock = apiFetch as unknown as ReturnType<typeof vi.fn>;

describe("wall-api", () => {
  beforeEach(() => {
    mock.mockReset();
    mock.mockResolvedValue({});
    __resetTransientFailureState();
    vi.spyOn(console, "debug").mockImplementation(() => { /* silence */ });
  });

  it("fetchWall GETs /api/board/{slug}/wall", async () => {
    await fetchWall("my slug");
    expect(mock).toHaveBeenCalledTimes(1);
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/my%20slug/wall");
    expect(init).toBeUndefined();
  });

  it("createNote POSTs the input body at /wall/notes", async () => {
    const input = { x: 10, y: 20, width: 200, height: 120, color: "#fff", text: "hi" };
    await createNote("abc", input);
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/notes");
    expect(init).toEqual({ method: "POST", body: JSON.stringify(input) });
  });

  it("patchNoteRemote PATCHes with ifVersion + patch merged", async () => {
    await patchNoteRemote("abc", "note 1", { ifVersion: 3, color: "#123" });
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/notes/note%201");
    expect(init).toEqual({
      method: "PATCH",
      body: JSON.stringify({ ifVersion: 3, color: "#123" }),
    });
  });

  it("deleteNoteRemote DELETEs /wall/notes/{id}", async () => {
    await deleteNoteRemote("abc", "note/1");
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/notes/note%2F1");
    expect(init).toEqual({ method: "DELETE" });
  });

  it("createEdgeRemote POSTs { from, to } to /wall/edges", async () => {
    await createEdgeRemote("abc", "a", "b");
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/edges");
    expect(init).toEqual({ method: "POST", body: JSON.stringify({ from: "a", to: "b" }) });
  });

  it("deleteEdgeRemote DELETEs /wall/edges/{id}", async () => {
    await deleteEdgeRemote("abc", "e 1");
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/edges/e%201");
    expect(init).toEqual({ method: "DELETE" });
  });

  it("postTransient POSTs the body to /wall/transient", async () => {
    await postTransient("abc", { noteId: "n1", x: 5, y: 6 });
    const [url, init] = mock.mock.calls[0];
    expect(url).toBe("/api/board/abc/wall/transient");
    expect(init).toEqual({
      method: "POST",
      body: JSON.stringify({ noteId: "n1", x: 5, y: 6 }),
    });
  });

  it("postTransient swallows rejections so callers never see unhandled errors", async () => {
    mock.mockRejectedValueOnce(new Error("boom"));
    await expect(postTransient("abc", { noteId: "n1", x: 0, y: 0 })).resolves.toBeUndefined();
  });
});

describe("wall-api.postTransient observability", () => {
  beforeEach(() => {
    mock.mockReset();
    __resetTransientFailureState();
    delete (globalThis as any).__scrumboyWallDebug;
  });

  it("increments the failure counter when the fetch rejects", async () => {
    mock.mockRejectedValue(new Error("boom"));
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(__getTransientFailureCount()).toBe(2);
  });

  it("throttles logging to one message per window when bursts of failures occur", async () => {
    const debug = vi.spyOn(console, "debug").mockImplementation(() => { /* noop */ });
    mock.mockRejectedValue(new Error("boom"));
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(debug).toHaveBeenCalledTimes(1);
    debug.mockRestore();
  });

  it("elevates the log level to warn when window.__scrumboyWallDebug is set", async () => {
    (globalThis as any).__scrumboyWallDebug = true;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => { /* noop */ });
    const debug = vi.spyOn(console, "debug").mockImplementation(() => { /* noop */ });
    mock.mockRejectedValue(new Error("boom"));
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(warn).toHaveBeenCalledTimes(1);
    expect(debug).not.toHaveBeenCalled();
    warn.mockRestore();
    debug.mockRestore();
  });

  it("does not log anything on successful posts", async () => {
    mock.mockResolvedValue({});
    const debug = vi.spyOn(console, "debug").mockImplementation(() => { /* noop */ });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => { /* noop */ });
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(debug).not.toHaveBeenCalled();
    expect(warn).not.toHaveBeenCalled();
    expect(__getTransientFailureCount()).toBe(0);
    debug.mockRestore();
    warn.mockRestore();
  });

  it("increments transientPostsSent on successful posts and leaves it at 0 on failure", async () => {
    mock.mockResolvedValueOnce({});
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(__getTransientPostsSent()).toBe(1);
    mock.mockRejectedValueOnce(new Error("boom"));
    await postTransient("abc", { noteId: "n1", x: 1, y: 2 });
    expect(__getTransientPostsSent()).toBe(1);
    mock.mockResolvedValueOnce({});
    await postTransient("abc", { noteId: "n1", x: 3, y: 4 });
    expect(__getTransientPostsSent()).toBe(2);
  });

  it("resets transientPostsSent via __resetTransientFailureState", async () => {
    mock.mockResolvedValue({});
    await postTransient("abc", { noteId: "n1", x: 0, y: 0 });
    expect(__getTransientPostsSent()).toBe(1);
    __resetTransientFailureState();
    expect(__getTransientPostsSent()).toBe(0);
  });
});
