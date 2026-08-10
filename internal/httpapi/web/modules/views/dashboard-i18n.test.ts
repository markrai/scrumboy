// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { temporaryBoardsNavLabelKey } from "../nav-labels.js";

const apiFetchMock = vi.hoisted(() => vi.fn());
const navigateMock = vi.hoisted(() => vi.fn());
const selectorState = vi.hoisted(() => ({
  dashboardLoading: false,
  dashboardNextCursor: null as string | null,
  dashboardSummary: null as Record<string, unknown> | null,
  dashboardTodos: [] as Record<string, unknown>[],
  dashboardTodoSort: "activity" as "activity" | "board",
  projects: [] as Record<string, unknown>[],
  user: null as unknown,
}));
const settingsDialogMock = vi.hoisted(() => document.createElement("dialog"));

vi.mock("../dom/elements.js", () => ({
  app: document.body,
  settingsDialog: settingsDialogMock,
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../router.js", () => ({
  navigate: navigateMock,
}));

vi.mock("../utils.js", () => ({
  escapeHTML: (s: string) =>
    String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;"),
  renderUserAvatar: () => "",
  sanitizeHexColor: (color: string | null | undefined, fallback = "#64748b") => color || fallback,
}));

vi.mock("../state/selectors.js", () => ({
  getDashboardLoading: () => selectorState.dashboardLoading,
  getDashboardNextCursor: () => selectorState.dashboardNextCursor,
  getDashboardSummary: () => selectorState.dashboardSummary,
  getDashboardTodos: () => selectorState.dashboardTodos,
  getDashboardTodoSort: () => selectorState.dashboardTodoSort,
  getProjects: () => selectorState.projects,
  getUser: () => selectorState.user,
}));

vi.mock("../state/mutations.js", () => ({
  appendDashboardTodos: vi.fn((todos: unknown[]) => {
    selectorState.dashboardTodos = [...selectorState.dashboardTodos, ...todos];
  }),
  setDashboardLoading: vi.fn((loading: boolean) => {
    selectorState.dashboardLoading = loading;
  }),
  setDashboardNextCursor: vi.fn((cursor: string | null) => {
    selectorState.dashboardNextCursor = cursor;
  }),
  setDashboardTodoSort: vi.fn((sort: "activity" | "board") => {
    selectorState.dashboardTodoSort = sort;
  }),
  setProjects: vi.fn((projects: unknown[]) => {
    selectorState.projects = projects;
  }),
  setProjectsTab: vi.fn(),
  setSettingsActiveTab: vi.fn(),
  setDashboardSummary: vi.fn((summary: unknown) => {
    selectorState.dashboardSummary = summary as Record<string, unknown> | null;
  }),
  setDashboardTodos: vi.fn((todos: unknown[]) => {
    selectorState.dashboardTodos = todos as Record<string, unknown>[];
  }),
}));

vi.mock("../core/notifications.js", () => ({
  ingestProjectsFromApp: vi.fn(),
}));

vi.mock("../dialogs/settings.js", () => ({
  renderSettingsModal: vi.fn(),
}));

const enCatalog = {
  "dashboard.empty.assignedTodos": "No todos assigned to you.",
  "dashboard.loadMore.action": "Load more",
  "dashboard.loadMore.loading": "Loading...",
  "dashboard.loadMore.loadingAria": "Loading more",
  "dashboard.loading.assignedTodos": "Loading assigned todos...",
  "dashboard.project.openTitle": "Open {name}",
  "dashboard.sort.activity": "Activity",
  "dashboard.sort.board.long": "Board Order (per project)",
  "dashboard.sort.board.short": "Board Order",
  "dashboard.sort.hint":
    "Order matches each project's board: column, then drag order. Projects appear in a fixed order (not alphabetical or by activity).",
  "dashboard.sort.label": "Sort",
  "dashboard.sprint.unscheduled": "Unscheduled",
  "dashboard.stats.avgLeadTime": "Avg. lead time",
  "dashboard.stats.currentSprint": "CURRENT SPRINT",
  "dashboard.stats.inProgress": "In progress",
  "dashboard.stats.oldestInProgress": "Oldest in progress",
  "dashboard.stats.pointsOnly": "{points} pts",
  "dashboard.stats.testing": "Testing",
  "dashboard.stats.totalAssigned": "Total assigned",
  "dashboard.stats.wip": "WIP",
  "dashboard.stats.yourFlow": "YOUR FLOW",
  "dashboard.stats.yourWorkload": "YOUR WORKLOAD",
  "dashboard.tabs.dashboard": "Dashboard",
  "dashboard.tabs.projects": "Projects",
  "dashboard.title": "Dashboard",
  "dashboard.todo.estimationPointsAria": "Estimation points",
  "nav.temporaryBoards.long": "Temporary Boards",
  "nav.temporaryBoards.short": "Temporary",
};

const deCatalog = {
  "dashboard.empty.assignedTodos": "Dir sind keine Todos zugewiesen.",
  "dashboard.loadMore.action": "Mehr laden",
  "dashboard.loadMore.loading": "Wird geladen...",
  "dashboard.loadMore.loadingAria": "Mehr wird geladen",
  "dashboard.loading.assignedTodos": "Zugewiesene Todos werden geladen...",
  "dashboard.project.openTitle": "{name} öffnen",
  "dashboard.sort.activity": "Aktivität",
  "dashboard.sort.board.long": "Board-Reihenfolge (pro Projekt)",
  "dashboard.sort.board.short": "Board-Reihenfolge",
  "dashboard.sort.hint":
    "Die Reihenfolge entspricht dem Board jedes Projekts: Spalte, dann Ziehreihenfolge. Projekte erscheinen in einer festen Reihenfolge (nicht alphabetisch oder nach Aktivität).",
  "dashboard.sort.label": "Sortierung",
  "dashboard.sprint.unscheduled": "Nicht eingeplant",
  "dashboard.stats.avgLeadTime": "Durchschn. Lead Time",
  "dashboard.stats.currentSprint": "AKTUELLER SPRINT",
  "dashboard.stats.inProgress": "In Arbeit",
  "dashboard.stats.oldestInProgress": "Am längsten in Arbeit",
  "dashboard.stats.pointsOnly": "{points} Pkt.",
  "dashboard.stats.testing": "Test",
  "dashboard.stats.totalAssigned": "Insgesamt zugewiesen",
  "dashboard.stats.wip": "WIP",
  "dashboard.stats.yourFlow": "DEIN FLOW",
  "dashboard.stats.yourWorkload": "DEINE AUSLASTUNG",
  "dashboard.tabs.dashboard": "Dashboard",
  "dashboard.tabs.projects": "Projekte",
  "dashboard.title": "Dashboard",
  "dashboard.todo.estimationPointsAria": "Schätzpunkte",
  "nav.temporaryBoards.long": "Temporäre Boards",
  "nav.temporaryBoards.short": "Temporär",
};

const pseudoCatalog = {
  "dashboard.empty.assignedTodos": "[!! No todos assigned to you. !!]",
  "dashboard.loadMore.action": "[!! Load more !!]",
  "dashboard.loadMore.loading": "[!! Loading... !!]",
  "dashboard.loadMore.loadingAria": "[!! Loading more !!]",
  "dashboard.loading.assignedTodos": "[!! Loading assigned todos... !!]",
  "dashboard.project.openTitle": "[!! Open {name} !!]",
  "dashboard.sort.activity": "[!! Activity !!]",
  "dashboard.sort.board.long": "[!! Board Order (per project) !!]",
  "dashboard.sort.board.short": "[!! Board Order !!]",
  "dashboard.sort.hint":
    "[!! Order matches each project's board: column, then drag order. Projects appear in a fixed order (not alphabetical or by activity). !!]",
  "dashboard.sort.label": "[!! Sort !!]",
  "dashboard.sprint.unscheduled": "[!! Unscheduled !!]",
  "dashboard.stats.avgLeadTime": "[!! Avg. lead time !!]",
  "dashboard.stats.currentSprint": "[!! CURRENT SPRINT !!]",
  "dashboard.stats.inProgress": "[!! In progress !!]",
  "dashboard.stats.oldestInProgress": "[!! Oldest in progress !!]",
  "dashboard.stats.pointsOnly": "[!! {points} pts !!]",
  "dashboard.stats.testing": "[!! Testing !!]",
  "dashboard.stats.totalAssigned": "[!! Total assigned !!]",
  "dashboard.stats.wip": "[!! WIP !!]",
  "dashboard.stats.yourFlow": "[!! YOUR FLOW !!]",
  "dashboard.stats.yourWorkload": "[!! YOUR WORKLOAD !!]",
  "dashboard.tabs.dashboard": "[!! Dashboard !!]",
  "dashboard.tabs.projects": "[!! Projects !!]",
  "dashboard.title": "[!! Dashboard !!]",
  "dashboard.todo.estimationPointsAria": "[!! Estimation points !!]",
  "nav.temporaryBoards.long": "[!! Temporary Boards !!]",
  "nav.temporaryBoards.short": "[!! Temporary !!]",
};

function loader(catalogs: Record<string, Record<string, string>>) {
  return vi.fn(async (locale: "en" | "de" | "pseudo") => catalogs[locale]);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flushPromises(count = 8): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

async function setupI18n(locale: "en" | "de" | "pseudo" = "en") {
  const i18n = await import("../i18n/index.js");
  await i18n.initI18n({
    locale,
    loadLocale: loader({ en: enCatalog, de: deCatalog, pseudo: pseudoCatalog }),
  });
  const hydrateOnLocaleChange = () => i18n.hydrateI18n(document.body);
  document.addEventListener(i18n.I18N_LOCALE_CHANGED, hydrateOnLocaleChange);
  return {
    i18n,
    cleanup: () => document.removeEventListener(i18n.I18N_LOCALE_CHANGED, hydrateOnLocaleChange),
  };
}

function setViewportWidth(width: number): void {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: width,
  });
}

function installMatchMedia(): void {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn((query: string) => ({
      matches: query === "(min-width: 768px)" ? window.innerWidth >= 768 : false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

function baseSummary(overrides: Record<string, unknown> = {}) {
  return {
    totalAssignedStoryPoints: 0,
    wipCount: 0,
    wipInProgressCount: 0,
    wipTestingCount: 0,
    avgLeadTimeDays: null,
    weeklyThroughput: [],
    projects: [],
    ...overrides,
  };
}

function baseProjects() {
  return [
    { id: 1, slug: "alpha", name: "Alpha", dominantColor: "#111111" },
    { id: 2, slug: "temp", name: "Temp", dominantColor: "#222222", expiresAt: "2026-01-10T00:00:00.000Z" },
  ];
}

describe("dashboard i18n", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    document.documentElement.removeAttribute("data-theme");
    document.documentElement.lang = "en";
    document.documentElement.removeAttribute("data-locale");
    localStorage.clear();
    apiFetchMock.mockReset();
    navigateMock.mockReset();
    selectorState.dashboardLoading = false;
    selectorState.dashboardNextCursor = null;
    selectorState.dashboardSummary = null;
    selectorState.dashboardTodos = [];
    selectorState.dashboardTodoSort = "activity";
    selectorState.projects = [];
    selectorState.user = null;
    setViewportWidth(1024);
    installMatchMedia();
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
      configurable: true,
      value() {
        (this as HTMLDialogElement).open = true;
      },
    });
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    document.body.innerHTML = "";
    document.documentElement.lang = "en";
    document.documentElement.removeAttribute("data-locale");
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("keeps English loading shell copy exact while dashboard data is pending", async () => {
    const summaryDeferred = deferred<Record<string, unknown>>();
    const todosDeferred = deferred<Record<string, unknown>>();
    const projectsDeferred = deferred<Record<string, unknown>[]>();
    apiFetchMock.mockImplementation((url: string) => {
      if (url.startsWith("/api/dashboard/summary?")) return summaryDeferred.promise;
      if (url === "/api/dashboard/todos?limit=20") return todosDeferred.promise;
      if (url === "/api/projects") return projectsDeferred.promise;
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { cleanup } = await setupI18n("en");
    try {
      const mod = await import("./dashboard.js");
      const renderPromise = mod.renderDashboard();

      expect(document.querySelector(".panel__title")?.textContent).toBe("Dashboard");
      expect(document.querySelector(".dashboard-sort__label")?.textContent).toBe("Sort");
      expect(document.querySelector("#dashboardSortHint")?.textContent).toBe(
        "Order matches each project's board: column, then drag order. Projects appear in a fixed order (not alphabetical or by activity).",
      );
      expect(
        Array.from(document.querySelectorAll(".list__item .muted")).map((el) => el.textContent),
      ).toEqual([
        "Loading assigned todos...",
        "Loading assigned todos...",
        "Loading assigned todos...",
      ]);

      summaryDeferred.resolve(baseSummary());
      todosDeferred.resolve({ items: [], nextCursor: null });
      projectsDeferred.resolve(baseProjects());
      await renderPromise;
    } finally {
      cleanup();
    }
  });

  it("keeps English loaded dashboard copy exact and hydrates it in place for pseudo without refetching", async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/dashboard/summary?")) {
        return baseSummary({
          projects: [{ projectId: 1, projectName: "Alpha", projectSlug: "alpha" }],
        });
      }
      if (url === "/api/dashboard/todos?limit=20") {
        return {
          items: [
            {
              id: 9,
              localId: 9,
              title: "Raw Todo",
              projectId: 1,
              projectName: "Alpha",
              projectSlug: "alpha",
              projectDominantColor: "#123456",
              estimationPoints: 5,
              statusName: "Doing Raw",
              statusColor: "#00aa00",
              updatedAt: "2026-01-02T15:04:05.000Z",
            },
          ],
          nextCursor: "next-page",
        };
      }
      if (url === "/api/projects") {
        return baseProjects();
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n, cleanup } = await setupI18n("en");
    try {
      const mod = await import("./dashboard.js");
      await mod.renderDashboard();

      expect(document.querySelector(".panel__title")?.textContent).toBe("Dashboard");
      expect(document.querySelector("#dashboardTabBtn")?.textContent?.trim()).toBe("Dashboard");
      expect(document.querySelector("#projectsTabBtn .dashboard-tab__label")?.textContent).toBe("Projects");
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.textContent).toBe("Temporary Boards");
      expect(document.querySelector(".dashboard-sort__label")?.textContent).toBe("Sort");
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[0]?.textContent).toBe("Activity");
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[1]?.textContent).toBe("Board Order (per project)");
      expect(document.getElementById("dashboardLoadMoreBtn")?.textContent?.trim()).toBe("Load more");
      expect(document.querySelector('[data-sprint-section="unscheduled"] .dashboard-project-group__tab--sprint')?.textContent).toBe("Unscheduled");
      expect(document.querySelector(".dashboard-stats__section .dashboard-stats__label")?.textContent).toBe("YOUR WORKLOAD");
      expect(document.body.textContent).not.toContain("CURRENT SPRINT");
      expect(apiFetchMock).toHaveBeenCalledTimes(3);

      await i18n.setLocale("pseudo");
      await flushPromises();

      expect(document.querySelector(".panel__title")?.textContent).toBe("[!! Dashboard !!]");
      expect(document.querySelector("#dashboardTabBtn")?.textContent?.trim()).toBe("[!! Dashboard !!]");
      expect(document.querySelector("#projectsTabBtn .dashboard-tab__label")?.textContent).toBe("[!! Projects !!]");
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.textContent).toBe("[!! Temporary Boards !!]");
      expect(document.querySelector(".dashboard-sort__label")?.textContent).toBe("[!! Sort !!]");
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[0]?.textContent).toBe("[!! Activity !!]");
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[1]?.textContent).toBe("[!! Board Order (per project) !!]");
      expect(document.getElementById("dashboardLoadMoreBtn")?.textContent?.trim()).toBe("[!! Load more !!]");
      expect(document.querySelector('[data-sprint-section="unscheduled"] .dashboard-project-group__tab--sprint')?.textContent).toBe("[!! Unscheduled !!]");
      expect(document.querySelector(".dashboard-stats__section .dashboard-stats__label")?.textContent).toBe("[!! YOUR WORKLOAD !!]");
      expect(document.body.textContent).not.toContain("[!! CURRENT SPRINT !!]");
      expect(apiFetchMock).toHaveBeenCalledTimes(3);
    } finally {
      cleanup();
    }
  });

  it("uses the shared temporary label and responsive board-order labels at 767 and 768", async () => {
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/dashboard/summary?")) return baseSummary();
      if (url === "/api/dashboard/todos?limit=20") return { items: [], nextCursor: null };
      if (url === "/api/projects") return baseProjects();
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { cleanup } = await setupI18n("en");
    try {
      const mod = await import("./dashboard.js");

      setViewportWidth(767);
      await mod.renderDashboard();
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.textContent).toBe("Temporary");
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.getAttribute("data-i18n-text")).toBe(
        temporaryBoardsNavLabelKey(767),
      );
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[1]?.textContent).toBe("Board Order");

      setViewportWidth(768);
      await mod.renderDashboard();
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.textContent).toBe("Temporary Boards");
      expect(document.querySelector("#temporaryTabBtn .dashboard-tab__label")?.getAttribute("data-i18n-text")).toBe(
        temporaryBoardsNavLabelKey(768),
      );
      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.options[1]?.textContent).toBe("Board Order (per project)");
    } finally {
      cleanup();
    }
  });

  it("uses the app locale for dashboard timestamps while keeping user data raw and only localizing fallback unscheduled without refetching", async () => {
    const updatedAt = "2026-01-02T15:04:05.000Z";
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/dashboard/summary?")) {
        return baseSummary({
          projects: [{ projectId: 1, projectName: "Project Raw", projectSlug: "project-raw" }],
        });
      }
      if (url === "/api/dashboard/todos?limit=20") {
        return {
          items: [
            {
              id: 42,
              localId: 42,
              title: "Todo Raw",
              projectId: 1,
              projectName: "Project Raw",
              projectSlug: "project-raw",
              projectDominantColor: "#663399",
              estimationPoints: 5,
              statusName: "Doing Raw",
              statusColor: "#336699",
              updatedAt,
            },
          ],
          nextCursor: null,
        };
      }
      if (url === "/api/projects") {
        return baseProjects();
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n, cleanup } = await setupI18n("en");
    try {
      const mod = await import("./dashboard.js");
      await mod.renderDashboard();

      await i18n.setLocale("de");
      await flushPromises();

      expect(document.querySelector(".panel__title")?.textContent).toBe("Dashboard");
      expect(document.querySelector("#projectsTabBtn .dashboard-tab__label")?.textContent).toBe("Projekte");
      expect(document.querySelector('[data-sprint-section="unscheduled"] .dashboard-project-group__tab--sprint')?.textContent).toBe("Nicht eingeplant");
      expect(document.querySelector(".dashboard-project-group__tab-name")?.textContent).toBe("Project Raw");
      expect(document.querySelector('[data-open-todo-local-id="42"] .dashboard-todo__title')?.textContent).toBe("Todo Raw");
      expect(document.querySelector('[data-open-todo-local-id="42"] .status-pill')?.textContent).toBe("Doing Raw");
      expect(document.querySelector('[data-open-todo-local-id="42"] .dashboard-todo__points')?.getAttribute("aria-label")).toBe("Schätzpunkte");
      expect(document.querySelector('[data-open-todo-local-id="42"] .muted div:last-child')?.textContent).toBe(
        new Intl.DateTimeFormat("de", {
          year: "numeric",
          month: "numeric",
          day: "numeric",
          hour: "numeric",
          minute: "2-digit",
          second: "2-digit",
        }).format(new Date(updatedAt)),
      );
      expect(apiFetchMock).toHaveBeenCalledTimes(3);
    } finally {
      cleanup();
    }
  });

  it("uses the shared long-date helper for sprint tooltip ranges", async () => {
    const sprintStart = new Date(2026, 0, 5, 12, 0, 0).getTime();
    const sprintEnd = new Date(2026, 0, 16, 12, 0, 0).getTime();
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/dashboard/summary?")) {
        return baseSummary({
          projects: [
            {
              projectId: 1,
              projectName: "Project Raw",
              projectSlug: "project-raw",
              activeSprint: {
                id: 10,
                name: "Sprint Raw",
                startAt: sprintStart,
                endAt: sprintEnd,
              },
              sprintSections: [
                {
                  id: 10,
                  name: "Sprint Raw",
                  state: "ACTIVE",
                  startAt: sprintStart,
                  endAt: sprintEnd,
                },
              ],
            },
          ],
        });
      }
      if (url === "/api/dashboard/todos?limit=20") {
        return {
          items: [
            {
              id: 43,
              localId: 43,
              title: "Todo Raw",
              projectId: 1,
              projectName: "Project Raw",
              projectSlug: "project-raw",
              projectDominantColor: "#663399",
              statusName: "Doing Raw",
              statusColor: "#336699",
              updatedAt: "2026-01-02T15:04:05.000Z",
              sprintId: 10,
            },
          ],
          nextCursor: null,
        };
      }
      if (url === "/api/projects") {
        return baseProjects();
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { i18n, cleanup } = await setupI18n("de");
    const formatLongDateSpy = vi.spyOn(i18n, "formatLongDateWithWeekday");
    try {
      const mod = await import("./dashboard.js");
      await mod.renderDashboard();

      const sprintTab = document.querySelector('[data-sprint-section="active"] .dashboard-project-group__tab--sprint');
      expect(sprintTab?.getAttribute("title")).toBe(
        `Sprint Raw\n${i18n.formatLongDateWithWeekday(sprintStart)} - ${i18n.formatLongDateWithWeekday(sprintEnd)}`,
      );
      expect(document.querySelector(".dashboard-stats__section .dashboard-stats__label")?.textContent).toBe("AKTUELLER SPRINT");
      expect(formatLongDateSpy).toHaveBeenCalledWith(sprintStart);
      expect(formatLongDateSpy).toHaveBeenCalledWith(sprintEnd);
      expect(apiFetchMock).toHaveBeenCalledTimes(3);
    } finally {
      cleanup();
    }
  });

  it("restores the previous sort when the dashboard sort refetch fails", async () => {
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/api/dashboard/summary?") && !url.includes("sort=")) {
        return baseSummary();
      }
      if (url === "/api/dashboard/todos?limit=20") {
        return { items: [], nextCursor: null };
      }
      if (url === "/api/projects") {
        return baseProjects();
      }
      if (url.startsWith("/api/dashboard/summary?")) {
        throw new Error("summary refetch failed");
      }
      if (url === "/api/dashboard/todos?limit=20&sort=board") {
        throw new Error("todos refetch failed");
      }
      throw new Error(`unexpected apiFetch url: ${url}`);
    });

    const { cleanup } = await setupI18n("en");
    try {
      const mod = await import("./dashboard.js");
      await mod.renderDashboard();

      const select = document.getElementById("dashboardTodoSort") as HTMLSelectElement | null;
      if (!select) throw new Error("missing dashboard sort select");
      select.value = "board";
      select.dispatchEvent(new Event("change", { bubbles: true }));
      await flushPromises(12);

      expect((document.getElementById("dashboardTodoSort") as HTMLSelectElement | null)?.value).toBe("activity");
      expect(selectorState.dashboardTodoSort).toBe("activity");
      expect(consoleErrorSpy).toHaveBeenCalled();
    } finally {
      cleanup();
    }
  });
});
