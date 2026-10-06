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
};

const deCatalog = {
  "board.selection.multiple": "{count} ausgew\u00e4hlte Eintr\u00e4ge bearbeiten",
  "board.selection.single": "1 ausgew\u00e4hlten Eintrag bearbeiten",
  "board.bulkArchive.limit": "W\u00e4hle h\u00f6chstens 500 Eintr\u00e4ge aus",
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
      <button data-todo-id="1" class="card"></button>
      <button data-todo-id="2" class="card"></button>
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

  it("keeps the bulk edit bar hidden for a single selected todo", async () => {
    const selection = await loadSelectionModule();

    selection.clearTodoMultiSelection();
    selection.toggleTodoSelection(1);

    expect((document.getElementById("bulkEditBar") as HTMLElement).style.display).toBe("none");
    expect(document.getElementById("bulkEditBarBtn")?.textContent).toBe("");
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
});
