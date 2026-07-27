// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const invalidateBoardMock = vi.hoisted(() => vi.fn());
const sortableCreateMock = vi.hoisted(() => vi.fn());
const selectorState = vi.hoisted(() => ({
  slug: "alpha",
  tag: "bug",
  search: "login",
  sprintId: "7",
  assignee: null as string | null,
  sort: null as string | null,
  laneMeta: {
    backlog: { hasMore: false, nextCursor: null, loading: false },
    not_started: { hasMore: false, nextCursor: null, loading: false },
    doing: { hasMore: false, nextCursor: null, loading: false },
    testing: { hasMore: false, nextCursor: null, loading: false },
    done: { hasMore: false, nextCursor: null, loading: false },
  },
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../state/selectors.js", () => ({
  getAssigneeFromUrl: () => selectorState.assignee,
  getSortFromUrl: () => selectorState.sort,
  getSlug: () => selectorState.slug,
  getTag: () => selectorState.tag,
  getSearch: () => selectorState.search,
  getSprintIdFromUrl: () => selectorState.sprintId,
  getBoardLaneMeta: () => selectorState.laneMeta,
}));

vi.mock("../utils.js", () => ({
  showToast: showToastMock,
}));

vi.mock("../orchestration/board-refresh.js", () => ({
  invalidateBoard: invalidateBoardMock,
  setBoardLimitPerLaneFloor: vi.fn(),
}));

vi.mock("../realtime/guard.js", () => ({
  recordBoardInteraction: vi.fn(),
  recordLocalMutation: vi.fn(),
}));

const enCatalog = {
  "board.refreshFailed": "Failed to refresh board",
  "board.todo.moveFailed": "Failed to move todo",
  "board.todo.movedTo": "Todo moved to {lane}",
};

const pseudoCatalog = {
  "board.refreshFailed": "[!! Failed to refresh board !!]",
  "board.todo.moveFailed": "[!! Failed to move todo !!]",
  "board.todo.movedTo": "[!! Todo moved to {lane} !!]",
};

describe("drag-drop i18n errors", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = `
      <div id="list_doing" data-status="doing"></div>
      <div id="tab_drop_doing" data-status="doing"></div>
    `;
    apiFetchMock.mockReset();
    showToastMock.mockReset();
    invalidateBoardMock.mockReset();
    invalidateBoardMock.mockResolvedValue(undefined);
    sortableCreateMock.mockReset();
    sortableCreateMock.mockImplementation(() => ({ destroy: vi.fn() }));
    vi.stubGlobal("Sortable", { create: sortableCreateMock });
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("localizes move failure toasts and preserves current filters for recovery invalidation", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "pseudo",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    apiFetchMock.mockRejectedValue(new Error("raw move failure"));

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    const list = document.getElementById("list_doing");
    if (!list) throw new Error("missing list_doing");
    const from = document.createElement("div");
    from.setAttribute("data-status", "backlog");
    const item = document.createElement("button");
    item.className = "card";
    item.setAttribute("data-todo-local-id", "12");
    list.appendChild(item);

    const doingSortableCall = sortableCreateMock.mock.calls.find(([el]) => (el as HTMLElement).id === "list_doing");
    if (!doingSortableCall) throw new Error("missing Sortable call for doing lane");
    await doingSortableCall[1].onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 0,
    });

    expect(showToastMock).toHaveBeenCalledWith("[!! Failed to move todo !!]");
    expect(invalidateBoardMock).toHaveBeenCalledWith("alpha", "bug", "login", "7", null, null);
  });
});
