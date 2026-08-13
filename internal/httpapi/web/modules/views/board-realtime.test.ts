// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
  invalidateBoardMock,
  dragState,
  selectorState,
  guardState,
} = vi.hoisted(() => ({
  invalidateBoardMock: vi.fn().mockResolvedValue(undefined),
  dragState: { value: false },
  selectorState: {
    slug: "alpha",
    tag: "bug",
    search: "login",
    sprintId: "7",
    assignee: null as string | null,
    sort: null as string | null,
    priority: null as string | null,
    authStatusAvailable: false,
    user: null as { id: number } | null,
    projectId: null as number | null,
  },
  guardState: {
    lastBoardInteractionTimestamp: 0,
    lastLocalMutationTimestamp: 0,
    bulkUpdating: false,
  },
}));

vi.mock("../utils.js", () => ({
  showToast: vi.fn(),
}));

vi.mock("../state/selectors.js", () => ({
  getAssigneeFromUrl: () => selectorState.assignee,
  getSortFromUrl: () => selectorState.sort ?? null,
  getPriorityFromUrl: () => selectorState.priority ?? null,
  getAuthStatusAvailable: () => selectorState.authStatusAvailable,
  getProjectId: () => selectorState.projectId,
  getSlug: () => selectorState.slug,
  getTag: () => selectorState.tag,
  getSearch: () => selectorState.search,
  getSprintIdFromUrl: () => selectorState.sprintId,
  getUser: () => selectorState.user,
}));

vi.mock("../members-cache.js", () => ({
  invalidateMembersCache: vi.fn(),
}));

vi.mock("../events.js", () => ({
  on: vi.fn(),
  off: vi.fn(),
  emit: vi.fn(),
}));

vi.mock("../realtime/guard.js", () => ({
  getLastBoardInteractionTimestamp: () => guardState.lastBoardInteractionTimestamp,
  getLastLocalMutationTimestamp: () => guardState.lastLocalMutationTimestamp,
  recordBoardInteraction: vi.fn(() => {
    guardState.lastBoardInteractionTimestamp = Date.now();
  }),
  isBulkUpdating: () => guardState.bulkUpdating,
}));

vi.mock("../core/assignmentNotify.js", () => ({
  playAssignmentSound: vi.fn(),
  showAssignmentDesktopNotification: vi.fn(),
}));

vi.mock("../core/sse-client.js", () => ({
  SseConnectionManager: class {
    open(): void {}
    stop(): void {}
    restartRequested(): void {}
  },
}));

vi.mock("../core/realtime.js", () => ({
  registerAnonymousSseRestart: vi.fn(),
}));

vi.mock("../orchestration/board-refresh.js", () => ({
  invalidateBoard: invalidateBoardMock,
}));

vi.mock("../features/drag-drop.js", () => ({
  get dragInProgress() {
    return dragState.value;
  },
}));

vi.mock("./board-selection.js", () => ({
  clearTodoMultiSelection: vi.fn(),
  updateBulkEditBar: vi.fn(),
}));

async function loadBoardRealtimeModule() {
  const mod = await import("./board-realtime.js");
  mod.__resetRealtimeRefreshStateForTest();
  return mod;
}

describe("board-realtime drag refresh guards", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-05-26T12:00:00Z"));
    vi.resetModules();
    invalidateBoardMock.mockReset();
    invalidateBoardMock.mockResolvedValue(undefined);
    dragState.value = false;
    selectorState.slug = "alpha";
    selectorState.tag = "bug";
    selectorState.search = "login";
    selectorState.sprintId = "7";
    selectorState.assignee = null;
    selectorState.sort = null;
    selectorState.priority = null;
    selectorState.authStatusAvailable = false;
    selectorState.user = null;
    selectorState.projectId = null;
    guardState.lastBoardInteractionTimestamp = 0;
    guardState.lastLocalMutationTimestamp = 0;
    guardState.bulkUpdating = false;
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("does not call invalidateBoard during drag even after the max delay or a forced flush", async () => {
    const mod = await loadBoardRealtimeModule();
    dragState.value = true;

    mod.__queuePendingRealtimeRefreshForTest("alpha");
    vi.advanceTimersByTime(mod.__getMaxRefreshDelayMsForTest() + 500);
    mod.__flushPendingRealtimeRefreshForTest(true);

    expect(invalidateBoardMock).not.toHaveBeenCalled();
    expect(mod.__getPendingRealtimeRefreshSlugForTest()).toBe("alpha");
  });

  it("flushes a queued refresh exactly once after drag ends", async () => {
    const mod = await loadBoardRealtimeModule();
    dragState.value = true;
    selectorState.priority = "deleted";

    mod.__queuePendingRealtimeRefreshForTest("alpha");
    vi.advanceTimersByTime(mod.__getMaxRefreshDelayMsForTest() + 500);
    expect(invalidateBoardMock).not.toHaveBeenCalled();

    dragState.value = false;
    vi.advanceTimersByTime(mod.__getRealtimeRefreshDebounceMsForTest() + 5);

    expect(invalidateBoardMock).toHaveBeenCalledTimes(1);
    expect(invalidateBoardMock).toHaveBeenCalledWith("alpha", "bug", "login", "7", null, null, "deleted");
    expect(mod.__getPendingRealtimeRefreshSlugForTest()).toBeNull();

    vi.advanceTimersByTime(mod.__getMaxRefreshDelayMsForTest());
    expect(invalidateBoardMock).toHaveBeenCalledTimes(1);
  });

  it("coalesces multiple queued refreshes during drag into one post-drag invalidate", async () => {
    const mod = await loadBoardRealtimeModule();
    dragState.value = true;

    mod.__queuePendingRealtimeRefreshForTest("alpha");
    mod.__queuePendingRealtimeRefreshForTest("alpha");
    mod.__queuePendingRealtimeRefreshForTest("alpha");
    vi.advanceTimersByTime(mod.__getMaxRefreshDelayMsForTest() + 500);
    expect(invalidateBoardMock).not.toHaveBeenCalled();

    dragState.value = false;
    vi.advanceTimersByTime(mod.__getRealtimeRefreshDebounceMsForTest() + 5);

    expect(invalidateBoardMock).toHaveBeenCalledTimes(1);
  });

  it("keeps the queued refresh behind the board interaction guard immediately after drag completion", async () => {
    const mod = await loadBoardRealtimeModule();
    dragState.value = true;

    mod.__queuePendingRealtimeRefreshForTest("alpha");

    dragState.value = false;
    guardState.lastBoardInteractionTimestamp = Date.now();
    mod.__flushPendingRealtimeRefreshForTest();

    expect(invalidateBoardMock).not.toHaveBeenCalled();

    vi.advanceTimersByTime(mod.__getRealtimeRefreshDebounceMsForTest() - 1);
    expect(invalidateBoardMock).not.toHaveBeenCalled();

    vi.advanceTimersByTime(2);
    expect(invalidateBoardMock).toHaveBeenCalledTimes(1);
    expect(invalidateBoardMock).toHaveBeenCalledWith("alpha", "bug", "login", "7", null, null, null);
  });

  it("preserves the old force-flush behavior for non-drag guards", async () => {
    const mod = await loadBoardRealtimeModule();
    mod.__setTodoDialogOpeningInProgressForTest(true);

    mod.__queuePendingRealtimeRefreshForTest("alpha");
    vi.advanceTimersByTime(mod.__getMaxRefreshDelayMsForTest() - 10);
    expect(invalidateBoardMock).not.toHaveBeenCalled();

    vi.advanceTimersByTime(20);
    expect(invalidateBoardMock).toHaveBeenCalledTimes(1);
    expect(invalidateBoardMock).toHaveBeenCalledWith("alpha", "bug", "login", "7", null, null, null);
  });
});
