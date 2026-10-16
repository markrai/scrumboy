// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const archiveTodosMock = vi.hoisted(() => vi.fn());

vi.mock("../dialogs/bulk-edit.js", () => ({
  initBulkEditDialog: vi.fn(),
  openBulkEditDialog: vi.fn(),
}));
vi.mock("../api.js", () => ({
  archiveTodos: archiveTodosMock,
}));

const enCatalog = {
  "board.selection.multiple": "Edit {count} selected",
  "board.selection.single": "Edit 1 selected",
  "board.bulkArchive.limit": "Select no more than 500 stories",
  "board.select.toggle": "Select",
  "board.select.done": "Done",
  "board.select.all": "Select all ({count})",
  "board.select.notLoaded": "{count} more not loaded",
};

const deCatalog = {
  "board.selection.multiple": "{count} ausgew\u00e4hlte Eintr\u00e4ge bearbeiten",
  "board.selection.single": "1 ausgew\u00e4hlten Eintrag bearbeiten",
  "board.bulkArchive.limit": "W\u00e4hle h\u00f6chstens 500 Eintr\u00e4ge aus",
  "board.select.toggle": "Ausw\u00e4hlen",
  "board.select.done": "Fertig",
  "board.select.all": "Alle ausw\u00e4hlen ({count})",
  "board.select.notLoaded": "{count} weitere nicht geladen",
};

async function loadSelectionModule() {
  return await import("./board-selection.js");
}

describe("board selection i18n", () => {
  beforeEach(async () => {
    vi.resetModules();
    document.body.innerHTML = `
      <div id="toast"></div>
      <div id="bulkEditBar" style="display: none"></div>
      <button id="bulkEditBarBtn" type="button"></button>
      <button id="bulkArchiveBarBtn" type="button" hidden></button>
      <button id="bulkSelectAllBtn" type="button" hidden></button>
      <button id="bulkClearBtn" type="button" hidden></button>
      <span id="bulkSelectHint" hidden></span>
      <button id="boardSelectToggle" type="button"></button>
      <div class="board">
        <section class="col"><div class="col__list">
          <button data-todo-id="1" class="card"></button>
          <button data-todo-id="2" class="card"></button>
          <button data-todo-id="3" class="card"></button>
        </div></section>
        <section class="col"><div class="col__list">
          <button data-todo-id="4" class="card"></button>
          <button data-todo-id="5" class="card"></button>
        </div></section>
      </div>
    `;
    archiveTodosMock.mockReset().mockResolvedValue({ transitionedCount: 2 });
    const i18n = await import("../i18n/index.js");
    await i18n.initI18n({
      locale: "en",
      loadLocale: vi.fn(async (locale: "en" | "de" | "pseudo") => (locale === "de" ? deCatalog : enCatalog)),
    });
  });

  afterEach(async () => {
    const i18n = await import("../i18n/index.js");
    i18n.resetI18nForTests();
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  it("renders English and German singular/plural selection copy", async () => {
    const i18n = await import("../i18n/index.js");
    const selection = await loadSelectionModule();

    expect(selection.__selectionLabelForTest(1)).toBe("Edit 1 selected");
    expect(selection.__selectionLabelForTest(3)).toBe("Edit 3 selected");

    await i18n.setLocale("de");

    expect(selection.__selectionLabelForTest(1)).toBe("1 ausgew\u00e4hlten Eintrag bearbeiten");
    expect(selection.__selectionLabelForTest(3)).toBe("3 ausgew\u00e4hlte Eintr\u00e4ge bearbeiten");
  });

  it("shows the bulk edit bar from the first selected todo", async () => {
    const selection = await loadSelectionModule();

    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);

    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("");
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 1 selected");
  });

  it("shows the bulk edit bar for two selected todos", async () => {
    const selection = await loadSelectionModule();

    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);
    selection.toggleTodoSelection(2);

    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("");
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 2 selected");
  });

  it("archives selected active stories with one batch request for project-local IDs", async () => {
    const mutations = await import("../state/mutations.js");
    mutations.setSlug("alpha");
    mutations.setBoard({
      project: { id: 1, name: "Alpha", slug: "alpha", dominantColor: "#000000", creatorUserId: 1 },
      tags: [],
      columns: {
        backlog: [
          { id: 1, localId: 12, title: "One", status: "backlog" },
          { id: 2, localId: 14, title: "Two", status: "backlog" },
        ],
      },
    });
    const selection = await loadSelectionModule();
    selection.ensureBulkEditUi({ getRole: () => "maintainer", syncSelectionClasses: () => {} });
    selection.toggleTodoSelection(1);
    selection.toggleTodoSelection(2);

    expect((document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement).hidden).toBe(false);
    (document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement).click();

    await vi.waitFor(() => expect(archiveTodosMock).toHaveBeenCalledWith("alpha", [12, 14]));
    expect(archiveTodosMock).toHaveBeenCalledTimes(1);
    expect(selection.getSelectedTodoIds().size).toBe(0);
  });

  it("never sends a batch larger than the server's 500-story limit", async () => {
    const todos = Array.from({ length: 501 }, (_, index) => ({
      id: index + 1,
      localId: index + 1,
      title: `Story ${index + 1}`,
      status: "backlog",
    }));
    const host = document.createElement("div");
    host.className = "board";
    host.innerHTML = todos.map((todo) => `<button data-todo-id="${todo.id}" class="card"></button>`).join("");
    document.body.appendChild(host);
    const mutations = await import("../state/mutations.js");
    mutations.setSlug("alpha");
    mutations.setBoard({
      project: { id: 1, name: "Alpha", slug: "alpha", dominantColor: "#000000", creatorUserId: 1 },
      tags: [],
      columns: { backlog: todos },
    });
    const selection = await loadSelectionModule();
    selection.ensureBulkEditUi({ getRole: () => "maintainer", syncSelectionClasses: () => {} });
    todos.forEach((todo) => selection.toggleTodoSelection(todo.id));

    const archiveButton = document.getElementById("bulkArchiveBarBtn") as HTMLButtonElement;
    expect(archiveButton.disabled).toBe(true);
    archiveButton.click();
    expect(archiveTodosMock).not.toHaveBeenCalled();
  });
  it("shift-click selects the inclusive range between the anchor and the target in DOM order", async () => {
    const selection = await loadSelectionModule();

    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(2);
    selection.selectRangeTo(4);

    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([2, 3, 4]);
    expect(document.querySelectorAll(".card--selected")).toHaveLength(3);
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 3 selected");
  });

  it("shift-click works backwards and without an anchor falls back to a single toggle", async () => {
    const selection = await loadSelectionModule();

    selection.clearTodoMultiSelection();
    selection.selectRangeTo(3);
    expect(Array.from(selection.getSelectedTodoIds())).toEqual([3]);

    selection.selectRangeTo(1);
    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 2, 3]);
  });

  it("select all only takes cards that are on the board, skipping lanes hidden by CSS", async () => {
    const selection = await loadSelectionModule();
    (document.querySelectorAll(".col")[1] as HTMLElement).style.display = "none";

    expect(selection.visibleTodoIdsInDomOrder()).toEqual([1, 2, 3]);
    selection.selectAllVisible();

    expect(Array.from(selection.getSelectedTodoIds())).toEqual([1, 2, 3]);
    expect(document.getElementById("bulkSelectAllBtn")?.textContent).toBe("Select all (3)");
    expect((document.getElementById("bulkSelectAllBtn") as HTMLButtonElement).disabled).toBe(true);
  });

  it("prunes selected todos that a filter change removed from the rendered board", async () => {
    const selection = await loadSelectionModule();

    selection.selectAllVisible();
    document.querySelector('[data-todo-id="2"]')?.remove();
    document.querySelector('[data-todo-id="5"]')?.remove();
    selection.pruneSelectionToRenderedBoard();

    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 3, 4]);
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("Edit 3 selected");
  });

  it("select mode shows the bar before anything is selected and Done clears it", async () => {
    const selection = await loadSelectionModule();

    selection.setSelectMode(true);
    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("");
    expect((document.getElementById("bulkEditBarBtn") as HTMLElement).hidden).toBe(true);
    expect(document.querySelector(".board")?.classList.contains("board--select-mode")).toBe(true);
    expect(document.getElementById("boardSelectToggle")?.textContent).toBe("Done");

    selection.clearTodoMultiSelection();
    expect(selection.isSelectMode()).toBe(false);
    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("none");
    expect(document.getElementById("boardSelectToggle")?.textContent).toBe("Select");
  });

  it("warns when matching todos are not loaded yet", async () => {
    const mutations = await import("../state/mutations.js");
    const board = {
      project: { id: 1, name: "A", slug: "a", dominantColor: "#000000", creatorUserId: 1 },
      columns: { BACKLOG: [{ id: 1, localId: 1 }, { id: 2, localId: 2 }, { id: 3, localId: 3 }], DOING: [] },
    } as any;
    mutations.setBoard(board);
    mutations.setBoardLaneMeta({
      BACKLOG: { hasMore: true, nextCursor: "x", loading: false, totalCount: 10 },
      DOING: { hasMore: false, nextCursor: null, loading: false },
    } as any);
    const selection = await loadSelectionModule();

    selection.toggleTodoSelection(1);

    const hint = document.getElementById("bulkSelectHint") as HTMLElement;
    expect(hint.hidden).toBe(false);
    expect(hint.textContent).toBe("7 more not loaded");
  });

  it("keeps selections in a lane hidden by the mobile tab strip when pruning, and select all is additive", async () => {
    const selection = await loadSelectionModule();
    selection.toggleTodoSelection(4);
    (document.querySelectorAll(".col")[1] as HTMLElement).style.display = "none";

    selection.pruneSelectionToRenderedBoard();
    expect(Array.from(selection.getSelectedTodoIds())).toEqual([4]);

    selection.selectAllVisible();
    expect(Array.from(selection.getSelectedTodoIds()).sort()).toEqual([1, 2, 3, 4]);
  });

  it("refreshes the select-all count after a render adds cards and Clear keeps select mode", async () => {
    const selection = await loadSelectionModule();
    selection.selectAllVisible();
    expect(document.getElementById("bulkSelectAllBtn")?.textContent).toBe("Select all (5)");
    const extra = document.createElement("button");
    extra.setAttribute("data-todo-id", "6");
    document.querySelector(".col__list")?.appendChild(extra);

    selection.pruneSelectionToRenderedBoard();
    expect(document.getElementById("bulkSelectAllBtn")?.textContent).toBe("Select all (6)");
    expect((document.getElementById("bulkSelectAllBtn") as HTMLButtonElement).disabled).toBe(false);

    selection.setSelectMode(true);
    selection.clearTodoMultiSelection({ keepSelectMode: true });
    expect(selection.isSelectMode()).toBe(true);
    expect(selection.getSelectedTodoIds().size).toBe(0);
  });
});
