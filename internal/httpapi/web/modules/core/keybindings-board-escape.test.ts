// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const routeState = vi.hoisted(() => ({ route: "boardBySlug" as string }));
const navigateMock = vi.hoisted(() => vi.fn());

vi.mock("../api.js", () => ({
  apiFetch: vi.fn(),
}));

vi.mock("../router.js", () => ({
  navigate: navigateMock,
}));

vi.mock("../utils.js", () => ({
  showToast: vi.fn(),
}));

vi.mock("../state/mutations.js", () => ({
  setProjectsTab: vi.fn(),
}));

vi.mock("../state/selectors.js", () => ({
  getAuthStatusAvailable: () => true,
  getLandingPageEnabled: () => false,
  getBoard: () => ({ id: 1 }),
  getProjectsTab: () => "projects",
  getRoute: () => routeState.route,
  getUser: () => ({ id: 1 }),
}));

function installBaseDOM(): void {
  document.body.innerHTML = `
    <dialog id="settingsDialog"></dialog>
    <dialog id="todoDialog"></dialog>
  `;
}

function openContextMenu(): void {
  document.body.insertAdjacentHTML(
    "beforeend",
    `<div id="contextMenu" class="context-menu" style="display: block;"></div>`,
  );
}

describe("boardEscapeBack with board context menu", () => {
  beforeEach(() => {
    vi.resetModules();
    navigateMock.mockClear();
    installBaseDOM();
    localStorage.clear();
    routeState.route = "boardBySlug";
  });

  afterEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("navigates back to projects when the context menu is absent", async () => {
    const keybindings = await import("./keybindings.js");

    keybindings.executeAction("boardEscapeBack");

    expect(navigateMock).toHaveBeenCalledWith("/");
  });

  it("does not navigate while the context menu is open", async () => {
    openContextMenu();
    const keybindings = await import("./keybindings.js");

    keybindings.executeAction("boardEscapeBack");

    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("Escape dismisses an open context menu without leaving the board", async () => {
    openContextMenu();
    const keybindings = await import("./keybindings.js");
    keybindings.initKeybindings({ openSettings: vi.fn() });
    const contextMenu = await import("../features/context-menu.js");
    contextMenu.setupContextMenuCloseHandler();

    document.dispatchEvent(
      new KeyboardEvent("keydown", { code: "Escape", key: "Escape", bubbles: true, cancelable: true }),
    );

    expect((document.getElementById("contextMenu") as HTMLElement).style.display).toBe("none");
    expect(navigateMock).not.toHaveBeenCalled();
  });
});
