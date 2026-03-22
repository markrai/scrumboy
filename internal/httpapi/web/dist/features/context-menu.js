// Context menu feature module
// Handles document-level click handler for closing context menu
let handlerAttached = false;
export function setupContextMenuCloseHandler() {
    if (handlerAttached)
        return;
    document.addEventListener("click", () => {
        const contextMenu = document.getElementById("contextMenu");
        if (contextMenu) {
            contextMenu.style.display = "none";
        }
    });
    handlerAttached = true;
}
