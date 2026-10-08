// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";

const openTodoDialog = vi.hoisted(() => vi.fn());
const openWallDialog = vi.hoisted(() => vi.fn());

vi.mock("../dialogs/todo.js", () => ({ openTodoDialog }));
vi.mock("../dialogs/wall.js", () => ({ openWallDialog }));

describe("board context menu commands", () => {
  beforeEach(() => {
    vi.resetModules();
    openTodoDialog.mockReset();
    openWallDialog.mockReset();
    document.body.innerHTML = `
      <div id="contextMenu" style="display:block">
        <button id="contextMenuNewTodo"></button>
        <button id="contextMenuSendToWall"></button>
      </div>`;
  });

  it("preserves New Todo and sends a captured card to the Wall", async () => {
    const mod = await import("./context-menu-button.js");
    mod.setupContextMenuButtonHandler();
    mod.setContextMenuStatus("doing");
    mod.setContextMenuRole("contributor");
    document.getElementById("contextMenuNewTodo")?.click();
    expect(openTodoDialog).toHaveBeenCalledWith({ mode: "create", status: "doing", role: "contributor" });

    mod.setContextMenuStory({ localId: 17, projectId: 4, slug: "alpha", role: "contributor" });
    document.getElementById("contextMenuSendToWall")?.click();
    await vi.waitFor(() => {
      expect(openWallDialog).toHaveBeenCalledWith({
        projectId: 4,
        slug: "alpha",
        role: "contributor",
        storyLocalId: 17,
      });
    });
    expect((document.getElementById("contextMenu") as HTMLElement).style.display).toBe("none");
  });
});
