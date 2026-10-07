let handlerAttached = false;
function hideContextMenu() {
    const contextMenu = document.getElementById("contextMenu");
    if (contextMenu && contextMenu.style.display !== "none") {
        contextMenu.style.display = "none";
    }
}
function onContextMenuEscapeKeydown(ev) {
    if (ev.key !== "Escape" || ev.repeat || ev.defaultPrevented)
        return;
    const contextMenu = document.getElementById("contextMenu");
    if (!contextMenu || contextMenu.style.display === "none")
        return;
    if (document.querySelector("dialog[open]"))
        return;
    ev.preventDefault();
    hideContextMenu();
}
export function setupContextMenuCloseHandler() {
    if (handlerAttached)
        return;
    document.addEventListener("click", hideContextMenu);
    document.addEventListener("keydown", onContextMenuEscapeKeydown);
    handlerAttached = true;
}
