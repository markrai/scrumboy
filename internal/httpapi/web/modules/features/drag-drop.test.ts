// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const invalidateBoardMock = vi.hoisted(() => vi.fn());
const sortableCreateMock = vi.hoisted(() => vi.fn());
const recordBoardInteractionMock = vi.hoisted(() => vi.fn());
const recordLocalMutationMock = vi.hoisted(() => vi.fn());
const sortableInstances: Array<{ destroy: ReturnType<typeof vi.fn> }> = [];
const selectorState = vi.hoisted(() => ({
  slug: "alpha",
  tag: null as string | null,
  search: null as string | null,
  sprintId: null as string | null,
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
  recordBoardInteraction: recordBoardInteractionMock,
  recordLocalMutation: recordLocalMutationMock,
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

type SortableOptions = {
  group: string;
  sort?: boolean;
  onStart?: () => void;
  onEnd: (evt: any) => Promise<void>;
};

function getElement(id: string): HTMLElement {
  const element = document.getElementById(id);
  if (!element) throw new Error(`missing ${id}`);
  return element;
}

function getSortableOptions(id: string, occurrence = -1): SortableOptions {
  const matches = sortableCreateMock.mock.calls.filter(([element]) => (element as HTMLElement).id === id);
  const index = occurrence < 0 ? matches.length - 1 : occurrence;
  const match = matches[index];
  if (!match) throw new Error(`missing Sortable call for ${id}`);
  return match[1] as SortableOptions;
}

function makeCard(localId: number): HTMLButtonElement {
  const item = document.createElement("button");
  item.className = "card";
  item.setAttribute("data-todo-local-id", String(localId));
  return item;
}

function expectMove(localId: number, toStatus: string, afterId: number | null, beforeId: number | null): void {
  expect(apiFetchMock).toHaveBeenCalledWith(
    `/api/board/alpha/todos/${localId}/move`,
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ toStatus, afterId, beforeId }),
    }),
  );
}

describe("drag-drop", () => {
  beforeEach(() => {
    vi.resetModules();
    selectorState.slug = "alpha";
    selectorState.tag = null;
    selectorState.search = null;
    selectorState.sprintId = null;
    selectorState.assignee = null;
    selectorState.sort = null;
    Object.values(selectorState.laneMeta).forEach((meta) => {
      meta.hasMore = false;
      meta.nextCursor = null;
      meta.loading = false;
    });
    document.body.innerHTML = `
      <div class="mobile-board-wrapper">
        <div id="list_backlog" data-status="backlog"></div>
        <div id="list_doing" data-status="doing"></div>
        <div id="mobileTabDropZones">
          <div id="tab_drop_backlog" data-status="backlog"></div>
          <div id="tab_drop_doing" data-status="doing"></div>
        </div>
      </div>
    `;
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue({});
    showToastMock.mockReset();
    invalidateBoardMock.mockReset();
    invalidateBoardMock.mockResolvedValue(undefined);
    recordBoardInteractionMock.mockReset();
    recordLocalMutationMock.mockReset();
    sortableCreateMock.mockReset();
    sortableInstances.length = 0;
    sortableCreateMock.mockImplementation(() => {
      const instance = { destroy: vi.fn() };
      sortableInstances.push(instance);
      return instance;
    });
    vi.stubGlobal("Sortable", { create: sortableCreateMock });
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("preserves manual same-lane rank anchors", async () => {
    const list = getElement("list_doing");
    const before = makeCard(11);
    const item = makeCard(12);
    const after = makeCard(13);
    list.append(before, item, after);

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    expect(getSortableOptions("list_doing").sort).toBe(true);
    await getSortableOptions("list_doing").onEnd({
      item,
      to: list,
      from: list,
      oldIndex: 0,
      newIndex: 1,
    });

    expectMove(12, "doing", 11, 13);
  });

  it("preserves manual cross-lane rank anchors", async () => {
    const from = getElement("list_backlog");
    const list = getElement("list_doing");
    const before = makeCard(21);
    const item = makeCard(22);
    const after = makeCard(23);
    list.append(before, item, after);

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    await getSortableOptions("list_doing").onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });

    expectMove(22, "doing", 21, 23);
  });

  it.each(["newest", "oldest"] as const)(
    "keeps connected cross-lane dragging enabled with in-lane sorting disabled for %s ordering",
    async (sort) => {
      selectorState.sort = sort;
      const dragDrop = await import("./drag-drop.js");

      dragDrop.initDnD();

      expect(sortableCreateMock).toHaveBeenCalledTimes(4);
      expect(getSortableOptions("list_backlog")).toEqual(expect.objectContaining({ group: "board", sort: false }));
      expect(getSortableOptions("list_doing")).toEqual(expect.objectContaining({ group: "board", sort: false }));
      expect(getSortableOptions("tab_drop_backlog")).toEqual(expect.objectContaining({ group: "board" }));
      expect(getSortableOptions("tab_drop_doing")).toEqual(expect.objectContaining({ group: "board" }));
      expect(apiFetchMock).not.toHaveBeenCalled();
    },
  );

  it("destroys chronological Sortables and re-enables in-lane sorting when returning to manual order", async () => {
    selectorState.sort = "newest";
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();
    expect(sortableInstances).toHaveLength(4);

    selectorState.sort = null;
    sortableCreateMock.mockClear();
    dragDrop.initDnD();

    sortableInstances.slice(0, 4).forEach((instance) => {
      expect(instance.destroy).toHaveBeenCalledTimes(1);
    });
    expect(getSortableOptions("list_doing").sort).toBe(true);
  });

  it("rejects a synthetic chronological same-lane callback without making a request", async () => {
    selectorState.sort = "newest";
    const list = getElement("list_doing");
    const item = makeCard(32);
    list.append(makeCard(31), item, makeCard(33));

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    await getSortableOptions("list_doing").onEnd({
      item,
      to: list,
      from: list,
      oldIndex: 0,
      newIndex: 1,
    });

    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("moves chronologically rendered cards across lanes without deriving DOM rank anchors", async () => {
    selectorState.sort = "oldest";
    const from = getElement("list_backlog");
    const list = getElement("list_doing");
    const item = makeCard(42);
    list.append(makeCard(41), item, makeCard(43));

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    await getSortableOptions("list_doing").onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });

    expectMove(42, "doing", null, null);
  });

  it("rejects a manual callback after the board switches to chronological order", async () => {
    const from = getElement("list_backlog");
    const list = getElement("list_doing");
    const item = makeCard(52);
    list.append(makeCard(51), item, makeCard(53));
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();
    const staleOptions = getSortableOptions("list_doing");

    selectorState.sort = "newest";
    await staleOptions.onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });

    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("rejects a chronological callback after the board switches to manual order", async () => {
    selectorState.sort = "oldest";
    const from = getElement("list_backlog");
    const list = getElement("list_doing");
    const item = makeCard(62);
    list.append(makeCard(61), item, makeCard(63));
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();
    const staleOptions = getSortableOptions("list_doing");

    selectorState.sort = null;
    await staleOptions.onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });

    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("keeps a newer active drag isolated from an older generation's stale onEnd", async () => {
    const from = getElement("list_backlog");
    const list = getElement("list_doing");
    const wrapper = document.querySelector(".mobile-board-wrapper");
    const dropZones = getElement("mobileTabDropZones");
    const item = makeCard(72);
    list.append(makeCard(71), item, makeCard(73));
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();
    const staleOptions = getSortableOptions("list_doing");
    if (!staleOptions.onStart) throw new Error("missing onStart for stale Sortable");

    staleOptions.onStart();
    expect(dragDrop.dragInProgress).toBe(true);
    expect(wrapper?.classList.contains("dragging")).toBe(true);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(true);

    dragDrop.initDnD();
    const currentOptions = getSortableOptions("list_doing");
    if (!currentOptions.onStart) throw new Error("missing onStart for current Sortable");

    expect(dragDrop.dragInProgress).toBe(false);
    expect(dragDrop.dragJustEnded).toBe(true);
    expect(wrapper?.classList.contains("dragging")).toBe(false);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(false);

    currentOptions.onStart();
    expect(dragDrop.dragInProgress).toBe(true);
    expect(dragDrop.dragJustEnded).toBe(false);
    expect(wrapper?.classList.contains("dragging")).toBe(true);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(true);
    recordBoardInteractionMock.mockClear();

    await staleOptions.onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });
    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(recordBoardInteractionMock).not.toHaveBeenCalled();
    expect(dragDrop.dragInProgress).toBe(true);
    expect(dragDrop.dragJustEnded).toBe(false);
    expect(wrapper?.classList.contains("dragging")).toBe(true);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(true);

    await currentOptions.onEnd({
      item,
      to: list,
      from,
      oldIndex: 0,
      newIndex: 1,
    });
    expectMove(72, "doing", 71, 73);
    expect(recordBoardInteractionMock).toHaveBeenCalledTimes(1);
    expect(dragDrop.dragInProgress).toBe(false);
    expect(dragDrop.dragJustEnded).toBe(true);
    expect(wrapper?.classList.contains("dragging")).toBe(false);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(false);
  });

  it("does not let a stale onStart reactivate drag state after reinitialization", async () => {
    const wrapper = document.querySelector(".mobile-board-wrapper");
    const dropZones = getElement("mobileTabDropZones");
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();
    const staleOptions = getSortableOptions("list_doing");
    if (!staleOptions.onStart) throw new Error("missing onStart for stale Sortable");

    dragDrop.initDnD();
    recordBoardInteractionMock.mockClear();
    staleOptions.onStart();

    expect(recordBoardInteractionMock).not.toHaveBeenCalled();
    expect(dragDrop.dragInProgress).toBe(false);
    expect(dragDrop.dragJustEnded).toBe(false);
    expect(wrapper?.classList.contains("dragging")).toBe(false);
    expect(dropZones.classList.contains("mobile-tab-drops--intro-glow")).toBe(false);
  });

  it("uses null anchors for chronological mobile lane-tab drops", async () => {
    selectorState.sort = "newest";
    const from = getElement("list_backlog");
    const tabDrop = getElement("tab_drop_doing");
    const item = makeCard(82);
    tabDrop.append(item);
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    await getSortableOptions("tab_drop_doing").onEnd({
      item,
      to: tabDrop,
      from,
      oldIndex: 0,
      newIndex: 0,
    });

    expectMove(82, "doing", null, null);
  });

  it("rejects a chronological mobile drop onto the card's current lane", async () => {
    selectorState.sort = "newest";
    const from = getElement("list_doing");
    const tabDrop = getElement("tab_drop_doing");
    const item = makeCard(92);
    tabDrop.append(item);
    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    await getSortableOptions("tab_drop_doing").onEnd({
      item,
      to: tabDrop,
      from,
      oldIndex: 0,
      newIndex: 0,
    });

    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("localizes move failure toasts and preserves current filters for recovery invalidation", async () => {
    selectorState.tag = "bug";
    selectorState.search = "login";
    selectorState.sprintId = "7";
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "pseudo",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    apiFetchMock.mockRejectedValue(new Error("raw move failure"));

    const dragDrop = await import("./drag-drop.js");
    dragDrop.initDnD();

    const list = getElement("list_doing");
    const from = getElement("list_backlog");
    const item = makeCard(12);
    list.appendChild(item);

    await getSortableOptions("list_doing").onEnd({
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
