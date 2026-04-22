// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiFetchMock = vi.hoisted(() => vi.fn());
const navigateMock = vi.hoisted(() => vi.fn());
const confirmDeleteMock = vi.hoisted(() => vi.fn());
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
  escapeHTML: (s: string) => s,
  showToast: vi.fn(),
  renderUserAvatar: () => "",
  confirmDelete: confirmDeleteMock,
}));

vi.mock("../state/selectors.js", () => ({
  getProjectsTab: () => "projects",
  getProjectView: () => "list",
  getProjects: () => [],
  getUser: () => null,
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
}));

async function flushPromises(count = 6): Promise<void> {
  for (let i = 0; i < count; i += 1) {
    await Promise.resolve();
  }
}

describe("projects delete confirmation", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    apiFetchMock.mockReset();
    navigateMock.mockReset();
    confirmDeleteMock.mockReset();
    apiFetchMock.mockImplementation(async (url: string) => {
      if (url === "/api/projects") {
        return [{ id: 99, slug: "alpha", name: "Alpha", role: "maintainer" }];
      }
      return {};
    });
  });

  it("blocks delete API call when confirmation is cancelled", async () => {
    confirmDeleteMock.mockResolvedValue(false);
    const mod = await import("./projects.js");
    await mod.renderProjects();

    const deleteBtn = document.querySelector("[data-del='99']");
    if (!(deleteBtn instanceof HTMLElement)) throw new Error("missing delete button");
    deleteBtn.click();
    await flushPromises();

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this project and all its todos?");
    expect(apiFetchMock).not.toHaveBeenCalledWith("/api/projects/99", { method: "DELETE" });
  });

  it("calls delete API when confirmation succeeds", async () => {
    confirmDeleteMock.mockResolvedValue(true);
    const mod = await import("./projects.js");
    await mod.renderProjects();

    const deleteBtn = document.querySelector("[data-del='99']");
    if (!(deleteBtn instanceof HTMLElement)) throw new Error("missing delete button");
    deleteBtn.click();
    await flushPromises(10);

    expect(confirmDeleteMock).toHaveBeenCalledWith("Delete this project and all its todos?");
    expect(apiFetchMock).toHaveBeenCalledWith("/api/projects/99", { method: "DELETE" });
  });
});
