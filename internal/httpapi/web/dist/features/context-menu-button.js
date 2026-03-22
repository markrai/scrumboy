// Context menu button feature module
// Handles "New Todo" button click in context menu
import { openTodoDialog } from '../dialogs/todo.js';
let handlerAttached = false;
let contextMenuStatus = null;
let contextMenuRole = null;
export function setupContextMenuButtonHandler() {
    if (handlerAttached)
        return;
    const contextMenuNewTodo = document.getElementById("contextMenuNewTodo");
    if (!contextMenuNewTodo)
        return;
    contextMenuNewTodo.addEventListener("click", () => {
        if (contextMenuStatus) {
            openTodoDialog({ mode: "create", status: contextMenuStatus, role: contextMenuRole });
            const contextMenu = document.getElementById("contextMenu");
            if (contextMenu) {
                contextMenu.style.display = "none";
            }
            contextMenuStatus = null;
            contextMenuRole = null;
        }
    });
    handlerAttached = true;
}
// Export function to set context menu status (called by board view)
export function setContextMenuStatus(status) {
    contextMenuStatus = status;
}
// Export function to set context menu role (called by board view; used for sprint field visibility)
export function setContextMenuRole(role) {
    contextMenuRole = role;
}
