// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { temporaryBoardsNavLabelKey } from "../nav-labels.js";

const apiFetchMock = vi.hoisted(() => vi.fn());
const navigateMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const selectorState = vi.hoisted(() => ({
  projectsTab: "projects",
  projectView: "list",
  projects: [] as unknown[],
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
  showToast: showToastMock,
  renderUserAvatar: () => "",
  confirmDelete: vi.fn(),
  showPromptDialog: vi.fn(),
}));

vi.mock("../state/selectors.js", () => ({
  getProjectsTab: () => selectorState.projectsTab,
  getProjectView: () => selectorState.projectView,
  getProjects: () => selectorState.projects,
  getUser: () => selectorState.user,
}));

vi.mock("../state/mutations.js", () => ({
  setProjects: vi.fn((projects: unknown[]) => {
    selectorState.projects = projects;
  }),
  setProjectsTab: vi.fn((tab: string) => {
    selectorState.projectsTab = tab;
  }),
  setProjectView: vi.fn((view: string) => {
    selectorState.projectView = view;
  }),
  setSettingsActiveTab: vi.fn(),
}));

vi.mock("../dialogs/settings.js", () => ({
  renderSettingsModal: vi.fn(),
}));

vi.mock("../core/notifications.js", () => ({
  ingestProjectsFromApp: vi.fn(),
}));

const enCatalog = {
  "nav.temporaryBoards.long": "Temporary Boards",
  "nav.temporaryBoards.short": "Temporary",
  "projects.actions.create": "Create",
  "projects.actions.createTemporaryBoard": "Create Temporary Board",
  "projects.actions.delete": "Delete",
  "projects.actions.rename": "Rename",
  "projects.actions.renameProject": "Rename project",
  "projects.actions.settings": "Settings",
  "projects.create.failed": "Failed to create project",
  "projects.empty.projects": "No projects yet.",
  "projects.empty.temporary": "No temporary boards yet.",
  "projects.fields.namePlaceholder": "New project name",
  "projects.tabs.dashboard": "Dashboard",
  "projects.tabs.projects": "Projects",
  "projects.title": "Projects",
  "projects.delete.failed": "Failed to delete project",
  "projects.rename.failed": "Failed to rename project",
  "projects.view.grid": "Grid view",
  "projects.view.list": "List view",
  "projects.workflow.addLaneAction": "Add",
  "projects.workflow.addLaneAriaLabel": "Add lane",
  "projects.workflow.addLanePlaceholder": "Add lane...",
  "projects.workflow.cancelAction": "Cancel",
  "projects.workflow.confirmAction": "Confirm",
  "projects.workflow.creating": "Creating...",
  "projects.workflow.doneLabel": "Done",
  "projects.workflow.helper": "Configure lanes before creating the project.",
  "projects.workflow.laneColor": "Lane color for {name}",
  "projects.workflow.reorderLane": "Reorder lane",
  "projects.workflow.setDoneLane": "Set {name} as done lane",
  "projects.workflow.title": "Customize Workflow",
  "projects.workflow.validation.duplicateKey": "Duplicate lane keys. Rename lanes to fix.",
  "projects.workflow.validation.emptyName": "Lane names cannot be empty.",
  "projects.workflow.validation.exactlyOneDone": "Exactly one lane must be marked as Done.",
  "projects.workflow.validation.invalidColor": "Lane colors must be valid hex colors.",
  "projects.workflow.validation.invalidKey": "Lane keys must be snake_case (letters, numbers, underscore).",
  "projects.workflow.validation.minLanes": "Workflow must have at least 2 lanes.",
  "tooltips.doneLane": "Exactly one lane counts as done.",
  "tooltips.workflowAddLane": "Adds a new column before the done lane.",
};

const deCatalog = {
  "nav.temporaryBoards.long": "Temporäre Boards",
  "nav.temporaryBoards.short": "Temporär",
  "projects.actions.create": "Erstellen",
  "projects.actions.createTemporaryBoard": "Temporäres Board erstellen",
  "projects.actions.delete": "Löschen",
  "projects.actions.rename": "Umbenennen",
  "projects.actions.renameProject": "Projekt umbenennen",
  "projects.actions.settings": "Einstellungen",
  "projects.create.failed": "Projekt konnte nicht erstellt werden",
  "projects.empty.projects": "Noch keine Projekte.",
  "projects.empty.temporary": "Noch keine temporären Boards.",
  "projects.fields.namePlaceholder": "Neuer Projektname",
  "projects.tabs.dashboard": "Dashboard",
  "projects.tabs.projects": "Projekte",
  "projects.title": "Projekte",
  "projects.delete.failed": "Projekt konnte nicht gelöscht werden",
  "projects.rename.failed": "Projekt konnte nicht umbenannt werden",
  "projects.view.grid": "Rasteransicht",
  "projects.view.list": "Listenansicht",
  "projects.workflow.addLaneAction": "Hinzufügen",
  "projects.workflow.addLaneAriaLabel": "Spalte hinzufügen",
  "projects.workflow.addLanePlaceholder": "Spalte hinzufügen...",
  "projects.workflow.cancelAction": "Abbrechen",
  "projects.workflow.confirmAction": "Bestätigen",
  "projects.workflow.creating": "Wird erstellt...",
  "projects.workflow.doneLabel": "Erledigt",
  "projects.workflow.helper": "Spalten vor dem Erstellen des Projekts konfigurieren.",
  "projects.workflow.laneColor": "Spaltenfarbe für {name}",
  "projects.workflow.reorderLane": "Spalte neu anordnen",
  "projects.workflow.setDoneLane": "{name} als Erledigt-Spalte festlegen",
  "projects.workflow.title": "Workflow anpassen",
  "projects.workflow.validation.duplicateKey": "Doppelte Spaltenschlüssel. Benenne die Spalten um, um das Problem zu beheben.",
  "projects.workflow.validation.emptyName": "Spaltennamen dürfen nicht leer sein.",
  "projects.workflow.validation.exactlyOneDone": "Genau eine Spalte muss als Erledigt markiert sein.",
  "projects.workflow.validation.invalidColor": "Spaltenfarben müssen gültige Hex-Farben sein.",
  "projects.workflow.validation.invalidKey": "Spaltenschlüssel müssen snake_case sein (Buchstaben, Zahlen, Unterstrich).",
  "projects.workflow.validation.minLanes": "Der Workflow muss mindestens 2 Spalten haben.",
  "tooltips.doneLane": "Genau eine Spalte zählt als erledigt.",
  "tooltips.workflowAddLane": "Fügt vor der Erledigt-Spalte eine neue Spalte hinzu.",
};

const pseudoCatalog = {
  "nav.temporaryBoards.long": "[!! Temporary Boards !!]",
  "nav.temporaryBoards.short": "[!! Temporary !!]",
  "projects.actions.create": "[!! Create !!]",
  "projects.actions.createTemporaryBoard": "[!! Create Temporary Board !!]",
  "projects.actions.delete": "[!! Delete !!]",
  "projects.actions.rename": "[!! Rename !!]",
  "projects.actions.renameProject": "[!! Rename project !!]",
  "projects.actions.settings": "[!! Settings !!]",
  "projects.create.failed": "[!! Failed to create project !!]",
  "projects.empty.projects": "[!! No projects yet. !!]",
  "projects.empty.temporary": "[!! No temporary boards yet. !!]",
  "projects.fields.namePlaceholder": "[!! New project name !!]",
  "projects.tabs.dashboard": "[!! Dashboard !!]",
  "projects.tabs.projects": "[!! Projects !!]",
  "projects.title": "[!! Projects !!]",
  "projects.delete.failed": "[!! Failed to delete project !!]",
  "projects.rename.failed": "[!! Failed to rename project !!]",
  "projects.view.grid": "[!! Grid view !!]",
  "projects.view.list": "[!! List view !!]",
  "projects.workflow.addLaneAction": "[!! Add !!]",
  "projects.workflow.addLaneAriaLabel": "[!! Add lane !!]",
  "projects.workflow.addLanePlaceholder": "[!! Add lane... !!]",
  "projects.workflow.cancelAction": "[!! Cancel !!]",
  "projects.workflow.confirmAction": "[!! Confirm !!]",
  "projects.workflow.creating": "[!! Creating... !!]",
  "projects.workflow.doneLabel": "[!! Done !!]",
  "projects.workflow.helper": "[!! Configure lanes before creating the project. !!]",
  "projects.workflow.laneColor": "[!! Lane color for {name} !!]",
  "projects.workflow.reorderLane": "[!! Reorder lane !!]",
  "projects.workflow.setDoneLane": "[!! Set {name} as done lane !!]",
  "projects.workflow.title": "[!! Customize Workflow !!]",
  "projects.workflow.validation.duplicateKey": "[!! Duplicate lane keys. Rename lanes to fix. !!]",
  "projects.workflow.validation.emptyName": "[!! Lane names cannot be empty. !!]",
  "projects.workflow.validation.exactlyOneDone": "[!! Exactly one lane must be marked as Done. !!]",
  "projects.workflow.validation.invalidColor": "[!! Lane colors must be valid hex colors. !!]",
  "projects.workflow.validation.invalidKey": "[!! Lane keys must be snake_case (letters, numbers, underscore). !!]",
  "projects.workflow.validation.minLanes": "[!! Workflow must have at least 2 lanes. !!]",
  "tooltips.doneLane": "[!! Exactly one lane counts as done. !!]",
  "tooltips.workflowAddLane": "[!! Adds a new column before the done lane. !!]",
};

function loader(catalogs: Record<string, Record<string, string>>) {
  return vi.fn(async (locale: "en" | "de" | "pseudo") => catalogs[locale]);
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

describe("projects i18n shell", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    localStorage.clear();
    apiFetchMock.mockReset();
    navigateMock.mockReset();
    showToastMock.mockReset();
    selectorState.projectsTab = "projects";
    selectorState.projectView = "list";
    selectorState.projects = [];
    selectorState.user = null;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1024,
    });
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
      configurable: true,
      value() {
        (this as HTMLDialogElement).open = true;
      },
    });
    Object.defineProperty(HTMLDialogElement.prototype, "close", {
      configurable: true,
      value() {
        (this as HTMLDialogElement).open = false;
        this.dispatchEvent(new Event("close"));
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

  it("keeps English shell copy exact and hydrates it in place for pseudo without refetching", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [{ id: 99, slug: "alpha", name: "Alpha", role: "maintainer" }];
      }
      return {};
    });

    const { i18n, cleanup } = await setupI18n("en");
    try {
      const mod = await import("./projects.js");
      await mod.renderProjects();

      expect(document.querySelector(".panel__title")?.textContent).toBe("Projects");
      expect(document.querySelector(".chips--view-tabs")).not.toBeNull();
      expect(document.getElementById("dashboardTabBtn")?.textContent?.trim()).toBe("Dashboard");
      expect(document.querySelector('[data-projects-tab="projects"] .projects-tab__label')?.textContent).toBe("Projects");
      expect(document.querySelector('[data-projects-tab="temporary"] .projects-tab__label')?.textContent).toBe("Temporary Boards");
      expect(document.querySelector('[data-projects-tab="temporary"] .projects-tab__label')?.getAttribute("data-i18n-text")).toBe(
        temporaryBoardsNavLabelKey(1024),
      );
      expect((document.getElementById("projectName") as HTMLInputElement | null)?.getAttribute("placeholder")).toBe("New project name");
      expect(document.querySelector('#createProjectForm button[type="submit"]')?.textContent).toBe("Create");
      expect(document.getElementById("settingsBtn")?.getAttribute("aria-label")).toBe("Settings");
      expect(document.querySelector('[data-rename="99"]')?.textContent).toBe("Rename");
      expect(document.querySelector('[data-rename="99"]')?.getAttribute("title")).toBe("Rename project");
      expect(document.querySelector('[data-del="99"]')?.textContent).toBe("Delete");
      expect(document.querySelector('.view-toggle-btn[data-view="list"]')?.getAttribute("title")).toBe("List view");
      expect(apiFetchMock).toHaveBeenCalledTimes(1);

      await i18n.setLocale("pseudo");
      await flushPromises();

      expect(document.querySelector(".panel__title")?.textContent).toBe("[!! Projects !!]");
      expect(document.getElementById("dashboardTabBtn")?.textContent?.trim()).toBe("[!! Dashboard !!]");
      expect(document.querySelector('[data-projects-tab="projects"] .projects-tab__label')?.textContent).toBe("[!! Projects !!]");
      expect(document.querySelector('[data-projects-tab="temporary"] .projects-tab__label')?.textContent).toBe("[!! Temporary Boards !!]");
      expect(document.querySelector('[data-projects-tab="temporary"] .projects-tab__label')?.getAttribute("data-i18n-text")).toBe(
        temporaryBoardsNavLabelKey(1024),
      );
      expect((document.getElementById("projectName") as HTMLInputElement | null)?.getAttribute("placeholder")).toBe("[!! New project name !!]");
      expect(document.querySelector('#createProjectForm button[type="submit"]')?.textContent).toBe("[!! Create !!]");
      expect(document.getElementById("settingsBtn")?.getAttribute("aria-label")).toBe("[!! Settings !!]");
      expect(document.querySelector('[data-rename="99"]')?.textContent).toBe("[!! Rename !!]");
      expect(document.querySelector('[data-del="99"]')?.textContent).toBe("[!! Delete !!]");
      expect(document.querySelector('.view-toggle-btn[data-view="list"]')?.getAttribute("title")).toBe("[!! List view !!]");
      expect(apiFetchMock).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("uses the shared temporary board label key at the mobile breakpoint", async () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 767,
    });
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [];
      }
      return {};
    });

    const { i18n, cleanup } = await setupI18n("en");
    try {
      const mod = await import("./projects.js");
      await mod.renderProjects();

      const temporaryTabLabel = document.querySelector('[data-projects-tab="temporary"] .projects-tab__label');
      expect(temporaryTabLabel?.textContent).toBe("Temporary");
      expect(temporaryTabLabel?.getAttribute("data-i18n-text")).toBe(temporaryBoardsNavLabelKey(767));
      expect(apiFetchMock).toHaveBeenCalledTimes(1);

      await i18n.setLocale("pseudo");
      await flushPromises();

      const updatedTemporaryTabLabel = document.querySelector('[data-projects-tab="temporary"] .projects-tab__label');
      expect(updatedTemporaryTabLabel?.textContent).toBe("[!! Temporary !!]");
      expect(updatedTemporaryTabLabel?.getAttribute("data-i18n-text")).toBe(temporaryBoardsNavLabelKey(767));
      expect(apiFetchMock).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("keeps default workflow lane names in English under a non-English locale", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [];
      }
      return {};
    });

    const { cleanup } = await setupI18n("de");
    try {
      const mod = await import("./projects.js");
      await mod.renderProjects();

      const input = document.getElementById("projectName") as HTMLInputElement | null;
      if (!input) throw new Error("missing project name input");
      input.value = "Neue Sache";
      document.getElementById("createProjectForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      await flushPromises();

      expect(document.querySelector("dialog .dialog__title")?.textContent).toBe("Workflow anpassen");
      expect(Array.from(document.querySelectorAll<HTMLInputElement>("[data-lane-name]")).map((el) => el.value)).toEqual([
        "Backlog",
        "Not Started",
        "In Progress",
        "Testing",
        "Done",
      ]);
    } finally {
      cleanup();
    }
  });

  it("keeps the persisted Untitled lane fallback in English under a non-English locale", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [];
      }
      return {};
    });

    const { cleanup } = await setupI18n("de");
    try {
      const mod = await import("./projects.js");
      await mod.renderProjects();

      const input = document.getElementById("projectName") as HTMLInputElement | null;
      if (!input) throw new Error("missing project name input");
      input.value = "Neue Sache";
      document.getElementById("createProjectForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      await flushPromises();

      const firstLaneInput = document.querySelector<HTMLInputElement>("[data-lane-name]");
      if (!firstLaneInput) throw new Error("missing workflow lane input");
      firstLaneInput.value = "";
      firstLaneInput.dispatchEvent(new Event("input", { bubbles: true }));
      firstLaneInput.dispatchEvent(new Event("blur", { bubbles: true }));

      expect(firstLaneInput.value).toBe("Untitled");
    } finally {
      cleanup();
    }
  });

  it("uses localized create failure fallback messages when project creation fails", async () => {
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [];
      }
      if (url === "/api/projects" && init?.method === "POST") {
        throw new Error("create raw failure");
      }
      return {};
    });

    const { cleanup } = await setupI18n("en");
    try {
      const mod = await import("./projects.js");
      await mod.renderProjects();

      const input = document.getElementById("projectName") as HTMLInputElement | null;
      if (!input) throw new Error("missing project name input");
      input.value = "Alpha";
      document.getElementById("createProjectForm")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      await flushPromises();

      const confirmBtn = document.getElementById("workflowModalConfirm") as HTMLButtonElement | null;
      if (!confirmBtn) throw new Error("missing confirm button");
      confirmBtn.click();
      await flushPromises(10);

      expect(showToastMock).toHaveBeenCalledWith("Failed to create project");
      expect(confirmBtn.textContent).toBe("Confirm");
      expect(confirmBtn.disabled).toBe(false);
      expect(navigateMock).not.toHaveBeenCalled();
    } finally {
      cleanup();
    }
  });
});
