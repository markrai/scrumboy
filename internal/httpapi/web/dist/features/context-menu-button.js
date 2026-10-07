// Context menu button feature module
// Handles "New Todo" button click in context menu
import { openTodoDialog } from '../dialogs/todo.js';
import { t } from '../i18n/index.js';
import { showToast } from '../utils.js';
let handlerAttached = false;
let contextMenuStatus = null;
let contextMenuRole = null;
let contextMenuStory = null;
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
    const sendToWall = document.getElementById("contextMenuSendToWall");
    sendToWall?.addEventListener("click", async () => {
        const target = contextMenuStory;
        const contextMenu = document.getElementById("contextMenu");
        if (contextMenu)
            contextMenu.style.display = "none";
        contextMenuStory = null;
        if (!target)
            return;
        try {
            const mod = await import("../dialogs/wall.js");
            await mod.openWallDialog({
                projectId: target.projectId,
                slug: target.slug,
                role: target.role,
                storyLocalId: target.localId,
            });
        }
        catch (err) {
            console.warn("wall load failed", err);
            showToast(t("board.wallOpenFailed"));
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
export function setContextMenuStory(story) {
    contextMenuStory = story;
}
