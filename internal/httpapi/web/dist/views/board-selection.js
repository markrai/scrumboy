import { initBulkEditDialog, openBulkEditDialog } from '../dialogs/bulk-edit.js';
let selectedTodoIds = new Set();
let bulkEditUiInitialized = false;
export function getSelectedTodoIds() {
    return selectedTodoIds;
}
export function clearTodoMultiSelection() {
    selectedTodoIds.clear();
    const bar = document.getElementById("bulkEditBar");
    const btn = document.getElementById("bulkEditBarBtn");
    if (bar)
        bar.style.display = "none";
    if (btn)
        btn.textContent = "";
    document.querySelectorAll(".board .card--selected").forEach((el) => el.classList.remove("card--selected"));
}
export function updateBulkEditBar() {
    const bar = document.getElementById("bulkEditBar");
    const btn = document.getElementById("bulkEditBarBtn");
    if (!bar || !btn)
        return;
    const n = selectedTodoIds.size;
    if (n >= 2) {
        bar.style.display = "";
        btn.textContent = `Edit ${n} selected`;
    }
    else {
        bar.style.display = "none";
        btn.textContent = "";
    }
}
export function toggleTodoSelection(id) {
    if (selectedTodoIds.has(id))
        selectedTodoIds.delete(id);
    else
        selectedTodoIds.add(id);
    updateBulkEditBar();
    const card = document.querySelector(`[data-todo-id="${id}"]`);
    if (card)
        card.classList.toggle("card--selected", selectedTodoIds.has(id));
}
export function ensureBulkEditUi(opts) {
    if (bulkEditUiInitialized)
        return;
    bulkEditUiInitialized = true;
    initBulkEditDialog(() => {
        clearTodoMultiSelection();
        updateBulkEditBar();
    });
    const barBtn = document.getElementById("bulkEditBarBtn");
    barBtn?.addEventListener("click", () => {
        void openBulkEditDialog(Array.from(selectedTodoIds), {
            role: opts.getRole(),
            onPruned: (remaining) => {
                selectedTodoIds = new Set(remaining);
                updateBulkEditBar();
                opts.syncSelectionClasses(selectedTodoIds);
            },
        });
    });
}
