// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const showPromptDialogMock = vi.hoisted(() => vi.fn());
const showToastMock = vi.hoisted(() => vi.fn());
const renderAuthMock = vi.hoisted(() => vi.fn());
const settingsDialogMock = vi.hoisted(() => document.createElement("dialog"));

vi.mock("../dom/elements.js", () => ({
  app: document.body,
  settingsDialog: settingsDialogMock,
}));

vi.mock("../api.js", () => ({
  apiFetch: apiFetchMock,
}));

vi.mock("../router.js", () => ({
  navigate: vi.fn(),
}));

vi.mock("./auth.js", () => ({
  renderAuth: renderAuthMock,
}));

vi.mock("../utils.js", () => ({
  escapeHTML: (s: string) => s,
  showToast: showToastMock,
  renderUserAvatar: () => "",
  confirmDelete: vi.fn(),
  showPromptDialog: showPromptDialogMock,
}));

vi.mock("../state/selectors.js", () => ({
  getProjectsTab: () => "projects",
  getProjectView: () => "list",
  getProjects: () => [],
  getUser: () => null,
  getOidcEnabled: () => true,
  getLocalAuthEnabled: () => false,
  getSelfServicePasswordResetEnabled: () => true,
}));

vi.mock("../state/mutations.js", () => ({
  setProjects: vi.fn(),
  setProjectsTab: vi.fn(),
  setProjectView: vi.fn(),
  setSettingsActiveTab: vi.fn(),
}));

vi.mock("../dialogs/settings.js", () => ({
  renderSettingsModal: vi.fn(),
}));

vi.mock("../core/notifications.js", () => ({
  ingestProjectsFromApp: vi.fn(),
}));

vi.mock("../nav-labels.js", () => ({
  temporaryBoardsNavLabel: () => "Temporary",
  temporaryBoardsNavLabelKey: () => "nav.temporaryBoards.short",
}));

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

describe("projects rename dialog", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    apiFetchMock.mockReset();
    showPromptDialogMock.mockReset();
    showToastMock.mockReset();
    renderAuthMock.mockReset();
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/projects") {
        return [{ id: 99, slug: "alpha", name: "Alpha", role: "maintainer" }];
      }
      return {};
    });
  });

  it("opens the shared prompt dialog and blocks the patch when cancelled", async () => {
    showPromptDialogMock.mockResolvedValue(null);
    const mod = await import("./projects.js");
    await mod.renderProjects();

    const renameBtn = document.querySelector("[data-rename='99']");
    if (!(renameBtn instanceof HTMLElement)) throw new Error("missing rename button");
    renameBtn.click();
    await flushPromises();

    expect(showPromptDialogMock).toHaveBeenCalledWith({
      title: "Rename Project",
      label: "Project Name",
      initialValue: "Alpha",
      confirmLabel: "Rename",
      placeholder: "Project name",
      maxLength: 200,
    });
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/projects/99", expect.anything());
  });

  it("patches the project name when the shared prompt returns a new value", async () => {
    showPromptDialogMock.mockResolvedValue("Beta");
    const mod = await import("./projects.js");
    await mod.renderProjects();

    const renameBtn = document.querySelector("[data-rename='99']");
    if (!(renameBtn instanceof HTMLElement)) throw new Error("missing rename button");
    renameBtn.click();
    await flushPromises(10);

    expect(showPromptDialogMock).toHaveBeenCalledTimes(1);
    expect(apiFetchMock).toHaveBeenCalledWith("/api/projects/99", {
      method: "PATCH",
      body: JSON.stringify({ name: "Beta" }),
    });
  });

  it("uses the localized rename fallback when the patch request fails", async () => {
    showPromptDialogMock.mockResolvedValue("Beta");
    apiFetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/projects" && !init) {
        return [{ id: 99, slug: "alpha", name: "Alpha", role: "maintainer" }];
      }
      if (url === "/api/projects/99" && init?.method === "PATCH") {
        throw new Error("rename raw failure");
      }
      return {};
    });

    const mod = await import("./projects.js");
    await mod.renderProjects();

    const renameBtn = document.querySelector("[data-rename='99']");
    if (!(renameBtn instanceof HTMLElement)) throw new Error("missing rename button");
    renameBtn.click();
    await flushPromises(10);

    expect(showToastMock).toHaveBeenCalledWith("Failed to rename project");
  });

  it("forwards auth capabilities when a projects request reports an expired session", async () => {
    apiFetchMock.mockRejectedValueOnce(Object.assign(new Error("unauthorized"), { status: 401 }));
    const mod = await import("./projects.js");

    await mod.renderProjects();

    expect(renderAuthMock).toHaveBeenCalledWith({
      next: "/",
      oidcEnabled: true,
      localAuthEnabled: false,
      selfServicePasswordResetEnabled: true,
    });
  });
});
