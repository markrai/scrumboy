// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { state, apiFetchMock, floorBySlug, defaultCardsPerLane } = vi.hoisted(() => ({
  state: { board: null as any, slug: null as string | null },
  apiFetchMock: vi.fn(),
  floorBySlug: { value: {} as Record<string, number> },
  defaultCardsPerLane: { value: 20 },
}));

vi.mock("../state/selectors.js", () => ({
  getBoard: () => state.board,
  getSlug: () => state.slug,
  getMobileTab: vi.fn(),
  getTag: vi.fn(() => ""),
  getSearch: vi.fn(() => ""),
  getSprintIdFromUrl: vi.fn(() => null),
  getEditingTodo: vi.fn(() => null),
  getProjectId: vi.fn(() => null),
  getTagColors: vi.fn(() => ({})),
  getUser: vi.fn(() => null),
  getBoardLaneMeta: vi.fn(() => ({})),
  getLaneDisplayCount: vi.fn(() => 0),
  getBoardMembers: vi.fn(() => []),
  getWallEnabled: vi.fn(() => false),
}));
vi.mock("../state/mutations.js", () => ({
  setProjectId: vi.fn(),
  setBoard: (board: any) => {
    state.board = board;
  },
  setSlug: (slug: string | null) => {
    state.slug = slug;
  },
  setTag: vi.fn(),
  setSearch: vi.fn(),
  setOpenTodoSegment: vi.fn(),
  setMobileTab: vi.fn(),
  setTagColors: vi.fn(),
  setSettingsActiveTab: vi.fn(),
  setBoardMembers: vi.fn(),
  setLaneLoading: vi.fn(),
  appendLaneTodos: vi.fn(),
}));

vi.mock("../dom/elements.js", () => ({
  app: document.createElement("div"),
  settingsDialog: document.createElement("dialog"),
}));
vi.mock("../api.js", () => ({ apiFetch: apiFetchMock }));
vi.mock("../core/notifications.js", () => ({ ingestProjectsFromApp: vi.fn() }));
vi.mock("../members-cache.js", () => ({
  fetchProjectMembers: vi.fn(),
  invalidateMembersCache: vi.fn(),
}));
vi.mock("../router.js", () => ({ navigate: vi.fn() }));
vi.mock("../utils.js", () => ({
  escapeHTML: (s: string) => s,
  showToast: vi.fn(),
  renderAvatarContent: vi.fn(() => ""),
  processImageFile: vi.fn(),
  confirmDelete: vi.fn(),
  showConfirmDialog: vi.fn(),
  showPromptDialog: vi.fn(),
  isAnonymousBoard: vi.fn(() => false),
  isTemporaryBoard: vi.fn(() => false),
}));
vi.mock("../field-tooltips.js", () => ({
  FIELD_TOOLTIPS: {},
  fieldLabelHTML: vi.fn(() => ""),
  titleAttr: vi.fn(() => ""),
}));
vi.mock("../i18n/index.js", () => ({
  apiErrorMessage: vi.fn(() => ""),
  I18N_LOCALE_CHANGED: "i18n:locale-changed",
  t: (k: string) => k,
}));
vi.mock("../dialogs/todo.js", () => ({ openTodoDialog: vi.fn() }));
vi.mock("../dialogs/settings.js", () => ({ renderSettingsModal: vi.fn() }));
vi.mock("../features/drag-drop.js", () => ({
  initDnD: vi.fn(),
  columnsSpec: vi.fn(() => []),
  setDnDColumns: vi.fn(),
  dragInProgress: false,
  dragJustEnded: false,
}));
vi.mock("../features/context-menu-button.js", () => ({
  setContextMenuStatus: vi.fn(),
  setContextMenuRole: vi.fn(),
}));
vi.mock("../orchestration/board-refresh.js", () => ({
  registerBoardRefresher: vi.fn(),
  registerSprintsRefresher: vi.fn(),
  invalidateBoard: vi.fn(),
  getDefaultCardsPerLane: () => defaultCardsPerLane.value,
  getBoardLimitPerLaneFloor: vi.fn((forSlug: string) => floorBySlug.value[forSlug] ?? defaultCardsPerLane.value),
  resetBoardLimitPerLaneFloor: vi.fn(),
  consumeForcePreferenceLimit: () => false,
}));
vi.mock("../sprints.js", () => ({ normalizeSprints: vi.fn((r: any) => r) }));
vi.mock("../events.js", () => ({ on: vi.fn(), off: vi.fn() }));
vi.mock("../realtime/guard.js", () => ({ recordLocalMutation: vi.fn() }));
vi.mock("./board-rendering.js", () => ({
  buildBoardColumnsHtml: vi.fn(() => ""),
  buildFiltersHtml: vi.fn(() => ""),
  buildNoResultsHtml: vi.fn(() => ""),
  buildTopbarHtml: vi.fn(() => ""),
  getBoardColumns: vi.fn(() => []),
  renderVoiceCommandTriggerHtml: vi.fn(() => ""),
  renderTodoCard: vi.fn(() => ""),
}));
vi.mock("./board-selection.js", () => ({
  clearTodoMultiSelection: vi.fn(),
  ensureBulkEditUi: vi.fn(),
  getSelectedTodoIds: vi.fn(() => new Set()),
  toggleTodoSelection: vi.fn(),
}));
vi.mock("./board-load-bootstrap.js", () => ({ bootstrapLoadedBoardView: vi.fn() }));
vi.mock("./board-filters.js", () => ({
  bindBoardFilterUi: vi.fn(),
  clearSprintChipData: vi.fn(),
  clearSprintChipDataIfSlugChanged: vi.fn(),
  computeBoardChipsRender: vi.fn(() => ({ chipsHTML: "", chipsUnchanged: true })),
  ensureSprintSubscription: vi.fn(),
  hasSprintChipDataForSlug: vi.fn(() => false),
  resetBoardFilterUiState: vi.fn(),
  setSprintChipDataForSlug: vi.fn(),
  updateChipsOnly: vi.fn(),
  notifySprintStateChanged: vi.fn(),
}));
vi.mock("./board-realtime.js", () => ({
  attachBoardInteractionListeners: vi.fn(),
  clearPendingRealtimeRefresh: vi.fn(),
  connectBoardEvents: vi.fn(),
  debugLog: vi.fn(),
  disconnectBoardEvents: vi.fn(),
  markBoardLoadSucceeded: vi.fn(),
  runWhileTodoDialogOpening: vi.fn(async (task: () => Promise<void>) => task()),
  setInitialBoardLoadInFlight: vi.fn(),
}));
vi.mock("./board-command-capabilities.js", () => ({ canShowVoiceCommands: vi.fn(() => false) }));
vi.mock("../core/voiceflow-preferences.js", () => ({ getVoiceFlowEnabledPreference: vi.fn(() => false) }));

function addLane(count: number): void {
  const list = document.createElement("div");
  list.className = "col__list";
  for (let i = 0; i < count; i++) {
    const card = document.createElement("div");
    card.setAttribute("data-todo-local-id", String(i + 1));
    list.appendChild(card);
  }
  document.body.appendChild(list);
}

function limitPerLaneFromFetchUrl(url: string): number {
  const qs = url.includes("?") ? url.slice(url.indexOf("?") + 1) : "";
  return Number(new URLSearchParams(qs).get("limitPerLane"));
}

describe("getRequestedBoardLimitPerLane", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    state.board = null;
    state.slug = null;
    floorBySlug.value = {};
    defaultCardsPerLane.value = 20;
    apiFetchMock.mockReset();
  });

  afterEach(() => {
    vi.resetModules();
  });

  it("preserves on-screen lane size across an unfiltered same-board refresh", async () => {
    const board = await import("./board.js");
    state.slug = "alpha";
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    addLane(45);

    expect(board.getRequestedBoardLimitPerLane()).toBe(45);
  });

  it("preserves on-screen lane size across a filtered same-board refresh", async () => {
    const board = await import("./board.js");
    // Filter state (tag/search/sprint) doesn't factor into this calculation at all --
    // it only cares whether the DOM-derived lane sizes belong to the currently loaded board.
    state.slug = "alpha";
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    addLane(60);

    expect(board.getRequestedBoardLimitPerLane()).toBe(60);
  });

  it("uses the user preference baseline when navigating to a different board", async () => {
    defaultCardsPerLane.value = 50;
    const board = await import("./board.js");
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    state.slug = "beta";
    addLane(45);

    expect(board.getRequestedBoardLimitPerLane()).toBe(50);
  });

  it("defaults to the user preference on initial load with no existing board or DOM", async () => {
    defaultCardsPerLane.value = 50;
    const board = await import("./board.js");
    state.board = null;
    state.slug = "alpha";

    expect(board.getRequestedBoardLimitPerLane()).toBe(50);
  });

  it("ignores DOM size when forSlug does not match the currently loaded board", async () => {
    const board = await import("./board.js");
    // Stale invalidate for "alpha" while the UI has already moved to "beta".
    state.slug = "beta";
    state.board = { project: { slug: "beta" }, tags: [], columns: {} };
    addLane(45);

    expect(board.getRequestedBoardLimitPerLane("alpha")).toBe(20);
    expect(board.getRequestedBoardLimitPerLane("beta")).toBe(45);
  });
});

describe("loadBoardBySlug limitPerLane query", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    state.board = null;
    state.slug = null;
    floorBySlug.value = {};
    defaultCardsPerLane.value = 20;
    apiFetchMock.mockReset();
    // Resolve with a minimal board; pathname mismatch below aborts before bootstrap.
    apiFetchMock.mockResolvedValue({ project: { id: 1, slug: "unused" }, tags: [], columns: {} });
  });

  afterEach(() => {
    vi.resetModules();
  });

  it("requests the DOM-derived limit on a same-board refresh", async () => {
    const board = await import("./board.js");
    state.slug = "alpha";
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    addLane(45);
    window.history.replaceState({}, "", "/other");

    await board.loadBoardBySlug("alpha", null, null, null);

    expect(apiFetchMock).toHaveBeenCalledTimes(1);
    expect(limitPerLaneFromFetchUrl(apiFetchMock.mock.calls[0][0])).toBe(45);
  });

  it("requests max(DOM, floor) when the filtered-drag floor is elevated", async () => {
    const board = await import("./board.js");
    state.slug = "alpha";
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    addLane(45);
    floorBySlug.value = { alpha: 60 };
    window.history.replaceState({}, "", "/other");

    await board.loadBoardBySlug("alpha", null, null, null);

    expect(limitPerLaneFromFetchUrl(apiFetchMock.mock.calls[0][0])).toBe(60);
  });

  it("requests the user preference when navigating to a different board even if a prior floor is elevated", async () => {
    defaultCardsPerLane.value = 50;
    const board = await import("./board.js");
    state.board = { project: { slug: "alpha" }, tags: [], columns: {} };
    state.slug = "beta";
    addLane(45);
    floorBySlug.value = { alpha: 60 };
    window.history.replaceState({}, "", "/other");

    await board.loadBoardBySlug("beta", null, null, null);

    expect(limitPerLaneFromFetchUrl(apiFetchMock.mock.calls[0][0])).toBe(50);
  });
});
