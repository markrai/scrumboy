let handlerAttached = false;

function hideContextMenu(): void {
  const contextMenu = document.getElementById("contextMenu") as HTMLElement | null;
  if (contextMenu && contextMenu.style.display !== "none") {
    contextMenu.style.display = "none";
  }
}

function onContextMenuEscapeKeydown(ev: KeyboardEvent): void {
  if (ev.key !== "Escape" || ev.repeat || ev.defaultPrevented) return;

  const contextMenu = document.getElementById("contextMenu") as HTMLElement | null;
  if (!contextMenu || contextMenu.style.display === "none") return;
  if (document.querySelector("dialog[open]")) return;

  ev.preventDefault();
  hideContextMenu();
  releaseInvokerFocus();
}

function releaseInvokerFocus(): void {
  const active = document.activeElement as HTMLElement | null;
  if (active && active.closest("#contextMenu, [data-todo-id]")) active.blur();
}

export function setupContextMenuCloseHandler(): void {
  if (handlerAttached) return;

  document.addEventListener("click", hideContextMenu);
  document.addEventListener("keydown", onContextMenuEscapeKeydown);

  handlerAttached = true;
}
