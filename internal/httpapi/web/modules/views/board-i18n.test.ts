// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Board } from "../types.js";
import realDeCatalog from "../i18n/locales/de.json";
import realEnCatalog from "../i18n/locales/en.json";

const apiFetchMock = vi.hoisted(() => vi.fn());
const initDnDMock = vi.hoisted(() => vi.fn());
const setDnDColumnsMock = vi.hoisted(() => vi.fn());
const customBootstrapBackLabel = vi.hoisted(() => ({ value: null as string | null }));
const domElements = vi.hoisted(() => ({
  app: document.createElement("div"),
  settingsDialog: document.createElement("dialog"),
}));

vi.mock("../dom/elements.js", () => ({
  app: domElements.app,
  settingsDialog: domElements.settingsDialog,
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../members-cache.js", () => ({
  fetchProjectMembers: vi.fn(async () => []),
  invalidateMembersCache: vi.fn(),
}));

vi.mock("../router.js", () => ({
  navigate: vi.fn(),
}));

vi.mock("../utils.js", () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;"),
  showToast: vi.fn(),
  renderAvatarContent: vi.fn(() => ""),
  renderUserAvatar: vi.fn(() => ""),
  processImageFile: vi.fn(),
  confirmDelete: vi.fn(),
  showConfirmDialog: vi.fn(),
  showPromptDialog: vi.fn(),
  sanitizeHexColor: (color?: string, fallback?: string) =>
    typeof color === "string" && /^#[0-9a-fA-F]{6}$/.test(color) ? color : (fallback ?? null),
  isAnonymousBoard: (board: Board | null) => !!(board?.project?.expiresAt && board.project.creatorUserId == null),
  isTemporaryBoard: (board: Board | null) => !!board?.project?.expiresAt,
}));

vi.mock("../field-tooltips.js", () => ({
  FIELD_TOOLTIPS: {},
  titleAttr: () => "",
  fieldLabelHTML: (label: string) => `<div class="field__label">${label}</div>`,
}));

vi.mock("../dialogs/todo.js", () => ({
  openTodoDialog: vi.fn(),
}));

vi.mock("../dialogs/settings.js", () => ({
  renderSettingsModal: vi.fn(),
}));

vi.mock("../features/drag-drop.js", () => ({
  initDnD: initDnDMock,
  setDnDColumns: setDnDColumnsMock,
  columnsSpec: () => [
    { key: "backlog", title: "Backlog" },
    { key: "done", title: "Done" },
  ],
  dragInProgress: false,
  dragJustEnded: false,
}));

vi.mock("../features/context-menu-button.js", () => ({
  setContextMenuStatus: vi.fn(),
  setContextMenuRole: vi.fn(),
}));

vi.mock("../events.js", () => ({
  on: vi.fn(),
  off: vi.fn(),
}));

vi.mock("../realtime/guard.js", () => ({
  recordLocalMutation: vi.fn(),
}));

vi.mock("../orchestration/board-refresh.js", () => ({
  registerBoardRefresher: vi.fn(),
  registerSprintsRefresher: vi.fn(),
  invalidateBoard: vi.fn(),
  getBoardLimitPerLaneFloor: () => 20,
  resetBoardLimitPerLaneFloor: vi.fn(),
  setBoardLimitPerLaneFloor: vi.fn(),
}));

vi.mock("../sprints.js", () => ({
  normalizeSprints: vi.fn(() => []),
}));

vi.mock("./mobile-lane-tabs.js", () => ({
  applyMobileLaneTabStyles: vi.fn(),
  buildMobileTabsInnerHtml: vi.fn(() => ""),
  mobileLaneTabStyleAttrForHtml: vi.fn(() => ({ tab: "", drop: "" })),
}));

vi.mock("./board-realtime.js", () => ({
  attachBoardInteractionListeners: vi.fn(),
  clearPendingRealtimeRefresh: vi.fn(),
  connectBoardEvents: vi.fn(),
  debugLog: vi.fn(),
  disconnectBoardEvents: vi.fn(),
  markBoardLoadSucceeded: vi.fn(),
  runWhileTodoDialogOpening: vi.fn(async (fn: () => unknown) => fn()),
  setInitialBoardLoadInFlight: vi.fn(),
}));

vi.mock("./board-command-capabilities.js", () => ({
  canShowVoiceCommands: vi.fn(() => false),
}));

vi.mock("../core/voiceflow-preferences.js", () => ({
  getVoiceFlowEnabledPreference: vi.fn(() => false),
}));

vi.mock("../dialogs/bulk-edit.js", () => ({
  initBulkEditDialog: vi.fn(),
  openBulkEditDialog: vi.fn(),
}));

vi.mock("./board-load-bootstrap.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./board-load-bootstrap.js")>();
  return {
    bootstrapLoadedBoardView: vi.fn(async (args) => {
      if (customBootstrapBackLabel.value != null) {
        return actual.bootstrapLoadedBoardView({
          ...args,
          renderLoadedBoard: (renderOpts) => {
            args.renderLoadedBoard({
              ...renderOpts,
              backLabel: customBootstrapBackLabel.value!,
            });
          },
        });
      }
      return actual.bootstrapLoadedBoardView(args);
    }),
  };
});

const enCatalog = {
  "board.actions.changeProjectImage": "Change project image",
  "board.actions.clearSearch": "Clear search",
  "board.actions.deleteProject": "Delete project",
  "board.actions.manageMembers": "Members",
  "board.actions.newTodo": "New Todo",
  "board.actions.openWall": "Open wall",
  "board.actions.renameProject": "Rename",
  "board.actions.settings": "Settings",
  "board.backToProjects": "\u2190 Projects",
  "board.filters.all": "All",
  "board.filters.allAssignees": "All assignees",
  "board.filters.assignee": "Assignee",
  "board.filters.assignedToMe": "Assigned to me",
  "board.filters.defaultOrder": "Default order",
  "board.filters.filteringOn": "Filtering: {value}",
  "board.filters.label": "Tags:",
  "board.filters.newestFirst": "Newest first",
  "board.filters.next": "Next tags",
  "board.filters.oldestFirst": "Oldest first",
  "board.filters.openFilters": "Filters",
  "board.filters.previous": "Previous tags",
  "board.filters.scheduled": "Scheduled",
  "board.filters.sort": "Sort",
  "board.filters.sortedBy": "Sorted: {value}",
  "board.filters.unassigned": "Unassigned",
  "board.filters.unscheduled": "Unscheduled",
  "board.loadMore": "Load more",
  "board.noResults": "No todos found matching \"{search}\"",
  "board.search.placeholder.desktop": "Search todos...",
  "board.search.placeholder.mobile": "Search",
  "board.todo.dragCard": "Drag card",
};

const pseudoCatalog = Object.fromEntries(
  Object.entries(enCatalog).map(([key, value]) => [key, `[!! ${value} !!]`]),
) as typeof enCatalog;

function board(): Board {
  return {
    project: {
      id: 1,
      name: "Alpha",
      slug: "alpha",
      dominantColor: "#123456",
      creatorUserId: 1,
    },
    tags: [{ name: "bug", color: "#ff0000" }],
    columnOrder: [
      { key: "backlog", name: "Backlog", color: "#9ca3af", isDone: false },
      { key: "done", name: "Done", color: "#ef4444", isDone: true },
    ],
    columns: {
      backlog: [],
      done: [],
    },
  };
}

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

async function renderPrefetchedBoard(
  mod: typeof import("./board.js"),
  opts: { tag?: string; search?: string; sort?: string | null } = {},
): Promise<void> {
  await mod.renderBoard(
    "alpha",
    opts.tag ?? "",
    opts.search ?? "needle",
    null,
    null,
    opts.sort ?? null,
    null,
    null,
    { prefetchedBoard: board() },
  );
  await flushPromises();
}

describe("board i18n locale switching", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    domElements.app.id = "app";
    domElements.app.innerHTML = "";
    document.body.appendChild(domElements.app);
    localStorage.clear();
    customBootstrapBackLabel.value = null;
    window.history.replaceState({}, "", "/alpha?search=needle");
    apiFetchMock.mockReset();
    apiFetchMock.mockResolvedValue(null);
    initDnDMock.mockReset();
    setDnDColumnsMock.mockReset();
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1024,
    });
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  afterEach(async () => {
    customBootstrapBackLabel.value = null;
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    const mutations = await import("../state/mutations.js");
    mutations.setUser(null);
    mutations.setAuthStatusAvailable(false);
    document.body.innerHTML = "";
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("does not initialize usable card dragging for viewers in chronological mode", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async () => enCatalog),
    });
    const membersCache = await import("../members-cache.js");
    const mutations = await import("../state/mutations.js");
    mutations.setAuthStatusAvailable(true);
    mutations.setUser({ id: 1, name: "Alex", email: "alex@example.com" } as any);
    vi.mocked(membersCache.fetchProjectMembers).mockResolvedValueOnce([
      { userId: 1, name: "Alex", email: "alex@example.com", role: "viewer" },
    ] as any);
    const viewerBoard = board();
    viewerBoard.columns.backlog = [{
      id: 7,
      localId: 12,
      title: "Read only",
      body: "",
      status: "BACKLOG",
      tags: [],
    }] as any;
    const mod = await import("./board.js");

    await mod.renderBoard("alpha", "", "needle", null, null, "newest", null, null, {
      prefetchedBoard: viewerBoard,
    });
    await flushPromises();

    expect(document.querySelector(".card__drag-handle")).not.toBeNull();
    expect(initDnDMock).not.toHaveBeenCalled();
  });

  it("updates the default back label after locale change without refetching", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    const mod = await import("./board.js");

    await renderPrefetchedBoard(mod);
    apiFetchMock.mockClear();

    const backBtn = document.getElementById("backBtn");
    expect(backBtn?.textContent).toBe("\u2190 Projects");
    expect(backBtn?.getAttribute("data-i18n-text")).toBe("board.backToProjects");

    await i18n.setLocale("pseudo");
    await flushPromises();

    expect(document.getElementById("backBtn")?.textContent).toBe("[!! \u2190 Projects !!]");
    expect(document.getElementById("backBtn")?.getAttribute("data-i18n-text")).toBe("board.backToProjects");
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("keeps a custom back label when locale changes", async () => {
    customBootstrapBackLabel.value = "\u2190 Home";
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    const mod = await import("./board.js");

    await renderPrefetchedBoard(mod);

    const backBtn = document.getElementById("backBtn");
    expect(backBtn?.textContent).toBe("\u2190 Home");
    expect(backBtn?.getAttribute("data-i18n-text")).toBeNull();

    await i18n.setLocale("pseudo");
    await flushPromises();

    expect(document.getElementById("backBtn")?.textContent).toBe("\u2190 Home");
    expect(document.getElementById("backBtn")?.getAttribute("data-i18n-text")).toBeNull();
  });

  it("does not stack board locale rerenders when renderBoard runs repeatedly", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    const mod = await import("./board.js");

    await renderPrefetchedBoard(mod);
    await renderPrefetchedBoard(mod);

    apiFetchMock.mockClear();
    setDnDColumnsMock.mockClear();

    await i18n.setLocale("pseudo");
    await flushPromises();
    expect(setDnDColumnsMock).toHaveBeenCalledTimes(1);

    setDnDColumnsMock.mockClear();
    await i18n.setLocale("en");
    await flushPromises();
    await i18n.setLocale("pseudo");
    await flushPromises();

    expect(setDnDColumnsMock).toHaveBeenCalledTimes(2);
    expect(document.getElementById("backBtn")?.textContent).toBe("[!! \u2190 Projects !!]");
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("preserves search and tag filter state across locale re-render", async () => {
    window.history.replaceState({}, "", "/alpha?tag=bug&search=needle");
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "pseudo") => (locale === "pseudo" ? pseudoCatalog : enCatalog)),
    });
    const mod = await import("./board.js");

    await renderPrefetchedBoard(mod, { tag: "bug", search: "needle" });
    apiFetchMock.mockClear();

    const searchInput = document.getElementById("searchInput") as HTMLInputElement | null;
    const activeTagChip = document.querySelector("[data-tag='bug']");
    expect(searchInput?.value).toBe("needle");
    expect(activeTagChip?.classList.contains("chip--active")).toBe(true);
    expect(document.querySelector(".board > .no-results")?.textContent).toBe('No todos found matching "needle"');

    await i18n.setLocale("pseudo");
    await flushPromises();

    expect((document.getElementById("searchInput") as HTMLInputElement | null)?.value).toBe("needle");
    expect(document.querySelector("[data-tag='bug']")?.classList.contains("chip--active")).toBe(true);
    expect(document.querySelector(".filters__label")?.textContent).toBe("[!! Tags: !!]");
    expect(document.querySelector(".board > .no-results")?.textContent).toBe('[!! No todos found matching "needle" !!]');
    expect(apiFetchMock).not.toHaveBeenCalled();
  });

  it("relocalizes an open Manage Members dialog without refetching or losing add-member selections", async () => {
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "de") => (locale === "de" ? realDeCatalog : realEnCatalog)),
    });
    const membersCache = await import("../members-cache.js");
    const mutations = await import("../state/mutations.js");
    mutations.setAuthStatusAvailable(true);
    mutations.setUser({ id: 1, name: "Alex", email: "alex@example.com" } as any);
    vi.mocked(membersCache.fetchProjectMembers).mockResolvedValue([
      { userId: 1, name: "Alex", email: "alex@example.com", role: "maintainer" },
      { userId: 2, name: "Sam", email: "sam@example.com", role: "viewer" },
    ] as any);
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/projects/1/members") {
        return [
          { userId: 1, name: "Alex", email: "alex@example.com", role: "maintainer" },
          { userId: 2, name: "Sam", email: "sam@example.com", role: "viewer" },
        ];
      }
      if (url === "/api/projects/1/available-users") {
        return [{ id: 3, name: "Taylor", email: "taylor@example.com" }];
      }
      if (url === "/api/board/alpha/sprints") return null;
      return null;
    });
    const addSpy = vi.spyOn(document, "addEventListener");
    const removeSpy = vi.spyOn(document, "removeEventListener");
    const mod = await import("./board.js");

    await renderPrefetchedBoard(mod, { search: "" });
    await flushPromises();

    const manageMembersBtn = document.getElementById("manageMembersBtn") as HTMLButtonElement | null;
    expect(manageMembersBtn).toBeTruthy();
    const localeAddsBeforeOpen = addSpy.mock.calls.filter(([type]) => type === i18n.I18N_LOCALE_CHANGED).length;

    manageMembersBtn!.click();
    await flushPromises();

    const dialog = document.getElementById("membersDialog") as HTMLDialogElement | null;
    expect(dialog).toBeTruthy();
    expect(document.getElementById("membersDialogTitle")?.textContent).toBe(realEnCatalog["board.members.dialogTitle"]);
    expect(document.getElementById("membersCurrentMembersLabel")?.textContent).toBe(realEnCatalog["board.members.currentMembers"]);
    expect(addSpy.mock.calls.filter(([type]) => type === i18n.I18N_LOCALE_CHANGED)).toHaveLength(localeAddsBeforeOpen + 1);

    const userSelect = document.getElementById("addMemberUser") as HTMLSelectElement;
    const roleSelect = document.getElementById("addMemberRole") as HTMLSelectElement;
    userSelect.value = "3";
    roleSelect.value = "viewer";
    expect(roleSelect.value).toBe("viewer");
    apiFetchMock.mockClear();
    vi.mocked(membersCache.fetchProjectMembers).mockClear();

    await i18n.setLocale("de");
    await flushPromises();

    expect(apiFetchMock).not.toHaveBeenCalled();
    expect(membersCache.fetchProjectMembers).not.toHaveBeenCalled();
    expect(document.getElementById("membersDialogTitle")?.textContent).toBe(realDeCatalog["board.members.dialogTitle"]);
    expect(document.getElementById("membersCurrentMembersLabel")?.textContent).toBe(realDeCatalog["board.members.currentMembers"]);
    expect(document.getElementById("membersUserFieldLabel")?.textContent).toBe(realDeCatalog["board.members.user"]);
    expect(document.querySelector('#addMemberRole option[value="maintainer"]')?.textContent).toBe(
      realDeCatalog["board.members.role.maintainer"],
    );
    expect((document.getElementById("addMemberUser") as HTMLSelectElement).value).toBe("3");
    expect((document.getElementById("addMemberRole") as HTMLSelectElement).value).toBe("viewer");

    (document.getElementById("addMemberCancel") as HTMLButtonElement).click();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(document.getElementById("membersDialog")).toBeNull();
    expect(removeSpy.mock.calls.filter(([type]) => type === i18n.I18N_LOCALE_CHANGED)).toHaveLength(1);
  });
});
