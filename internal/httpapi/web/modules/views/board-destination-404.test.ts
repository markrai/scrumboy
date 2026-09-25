// @vitest-environment happy-dom
/**
 * Proves production renderBoard/loadBoardBySlug rethrow API 404s with status,
 * so the router catch can own destination-level redirects (not a mocked renderBoard throw).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { state, apiFetchMock, bootstrapLoadedBoardViewMock } = vi.hoisted(() => ({
  state: { board: null as any, slug: null as string | null },
  apiFetchMock: vi.fn(),
  bootstrapLoadedBoardViewMock: vi.fn(),
}));

vi.mock("../state/selectors.js", () => ({
  getBoard: () => state.board,
  getSlug: () => state.slug,
  getMobileTab: vi.fn(),
  getTagsFromUrl: () => [],
  getSearch: () => "",
  getSprintIdFromUrl: () => null,
  getAssigneeFromUrl: () => null,
  getSortFromUrl: () => null,
  getPriorityFromUrl: () => null,
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
  sanitizeHexColor: vi.fn((color?: string | null) => (color && /^#[0-9a-f]{6}$/i.test(color) ? color : null)),
}));
vi.mock("../field-tooltips.js", () => ({
  FIELD_TOOLTIPS: {},
  fieldLabelHTML: vi.fn(() => ""),
  titleAttr: vi.fn(() => ""),
}));
vi.mock("../i18n/index.js", () => ({
  apiErrorMessage: vi.fn(() => "Not found"),
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
  getDefaultCardsPerLane: () => 20,
  getBoardLimitPerLaneFloor: vi.fn(() => 20),
  getRequestedBoardLimitPerLane: vi.fn(() => 20),
  resetBoardLimitPerLaneFloor: vi.fn(),
  consumeForcePreferenceLimit: () => false,
}));
vi.mock("../sprints.js", () => ({
  normalizeSprints: vi.fn((r: any) => r),
  boardSprintsEnabled: vi.fn(() => false),
}));
vi.mock("../events.js", () => ({ on: vi.fn(), off: vi.fn() }));
vi.mock("../realtime/guard.js", () => ({ recordLocalMutation: vi.fn() }));
vi.mock("./board-rendering.js", () => ({
  buildPriorityTierMap: vi.fn(() => ({})),
  buildBoardColumnsHtml: vi.fn(() => ""),
  buildChipsHTML: vi.fn(() => ""),
  buildFiltersHtml: vi.fn(() => ""),
  buildNoResultsHtml: vi.fn(() => ""),
  buildSprintFilterSectionHtml: vi.fn(() => ""),
  buildTopbarHtml: vi.fn(() => ""),
  getCombinedChipData: vi.fn(() => []),
  getBoardColumns: vi.fn(() => []),
  isBoardFilterActive: vi.fn(() => false),
  visibleBoardLaneCount: vi.fn(() => 0),
  renderVoiceCommandTriggerHtml: vi.fn(() => ""),
  renderTodoCard: vi.fn(() => ""),
}));
vi.mock("./board-selection.js", () => ({
  clearTodoMultiSelection: vi.fn(),
  ensureBulkEditUi: vi.fn(),
  getSelectedTodoIds: vi.fn(() => new Set()),
  toggleTodoSelection: vi.fn(),
}));
vi.mock("./board-load-bootstrap.js", () => ({ bootstrapLoadedBoardView: bootstrapLoadedBoardViewMock }));
vi.mock("./board-filters.js", () => ({
  clearSprintChipData: vi.fn(),
  clearSprintChipDataIfSlugChanged: vi.fn(),
  computeBoardChipsRender: vi.fn(() => ({ chipsHTML: "", chipsUnchanged: true })),
  ensureSprintSubscription: vi.fn(),
  getSprintChipDataForSlug: vi.fn(() => null),
  hasSprintChipDataForSlug: vi.fn(() => false),
  setSprintChipDataForSlug: vi.fn(),
  updateChipsOnly: vi.fn(),
  updateOmniTagPills: vi.fn(),
  notifySprintStateChanged: vi.fn(),
  resetBoardFilterUiState: vi.fn(),
  normalizeBoardTagFilters: (tags: readonly string[]) => [...tags],
  appendTagParams: vi.fn(),
  cancelPendingSearchReload: vi.fn(),
  priorityFilterExistsOnBoard: vi.fn(() => true),
  bindBoardFilterUi: vi.fn(),
}));
vi.mock("./board-realtime.js", () => ({
  attachBoardInteractionListeners: vi.fn(),
  clearPendingRealtimeRefresh: vi.fn(),
  clearResolverRequest: vi.fn(),
  connectBoardEvents: vi.fn(),
  debugLog: vi.fn(),
  disconnectBoardEvents: vi.fn(),
  markBoardLoadSucceeded: vi.fn(),
  runWhileTodoDialogOpening: vi.fn(async (task: () => Promise<void>) => task()),
  setInitialBoardLoadInFlight: vi.fn(),
}));
vi.mock("./board-command-capabilities.js", () => ({ canShowVoiceCommands: vi.fn(() => false) }));
vi.mock("../core/voiceflow-preferences.js", () => ({ getVoiceFlowEnabledPreference: vi.fn(() => false) }));

function notFoundError(): Error & { status: number } {
  return Object.assign(new Error("Not found"), {
    status: 404,
    data: { error: { code: "NOT_FOUND", message: "Not found" } },
  });
}

describe("board destination API 404 propagation", () => {
  beforeEach(() => {
    vi.resetModules();
    state.board = null;
    state.slug = null;
    apiFetchMock.mockReset();
    bootstrapLoadedBoardViewMock.mockReset();
    window.history.replaceState({}, "", "/nonsense");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loadBoardBySlug rethrows apiFetch 404 with status for the router", async () => {
    apiFetchMock.mockRejectedValueOnce(notFoundError());
    const board = await import("./board.js");

    await expect(board.loadBoardBySlug("nonsense", [], null)).rejects.toMatchObject({ status: 404 });
    expect(bootstrapLoadedBoardViewMock).not.toHaveBeenCalled();
  });

  it("renderBoard rethrows board-load 404 with status for the router", async () => {
    apiFetchMock.mockRejectedValueOnce(notFoundError());
    const board = await import("./board.js");

    await expect(
      board.renderBoard("nonsense", [], "", null, null, null, null, null, null, { skipLoad: false }),
    ).rejects.toMatchObject({ status: 404 });
    expect(bootstrapLoadedBoardViewMock).not.toHaveBeenCalled();
  });
});
