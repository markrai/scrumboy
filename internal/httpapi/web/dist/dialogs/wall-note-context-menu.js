// Sticky-note right-click context menu. Shown from wall.ts when the user
// right-clicks a note; returns the user's choice (or null on dismissal).
//
// The menu is appended to #wallDialog so it paints inside the wall's native
// <dialog> top-layer context; no z-index fighting with the modal. All
// listeners are wired to the caller-provided AbortSignal so wall teardown
// never leaks listeners. The promise always resolves exactly once and the
// DOM is torn down on every exit path.
import { wallDialog } from "../dom/elements.js";
export function openWallNoteContextMenu(clientX, clientY, signal, options) {
    return new Promise((resolve) => {
        const host = wallDialog ?? document.body;
        const showCreateTodo = options?.showCreateTodo !== false;
        const deleteLabel = options?.deleteLabel ?? "Delete";
        const menu = document.createElement("div");
        menu.className = "context-menu wall-note-context-menu";
        menu.setAttribute("role", "menu");
        menu.dataset.testid = "wallNoteContextMenu";
        let createBtn = null;
        if (showCreateTodo) {
            createBtn = document.createElement("button");
            createBtn.type = "button";
            createBtn.className = "context-menu__item";
            createBtn.setAttribute("role", "menuitem");
            createBtn.dataset.action = "create-todo";
            createBtn.textContent = "Create Todo from Note";
            menu.appendChild(createBtn);
        }
        const deleteBtn = document.createElement("button");
        deleteBtn.type = "button";
        deleteBtn.className = "context-menu__item";
        deleteBtn.setAttribute("role", "menuitem");
        deleteBtn.dataset.action = "delete";
        deleteBtn.textContent = deleteLabel;
        menu.appendChild(deleteBtn);
        // Position off-screen first so we can measure, then clamp to viewport.
        menu.style.left = "0px";
        menu.style.top = "0px";
        menu.style.visibility = "hidden";
        host.appendChild(menu);
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const rect = menu.getBoundingClientRect();
        const margin = 4;
        const left = Math.max(margin, Math.min(clientX, vw - rect.width - margin));
        const top = Math.max(margin, Math.min(clientY, vh - rect.height - margin));
        menu.style.left = `${Math.round(left)}px`;
        menu.style.top = `${Math.round(top)}px`;
        menu.style.visibility = "";
        let settled = false;
        const finish = (choice) => {
            if (settled)
                return;
            settled = true;
            cleanup();
            resolve(choice);
        };
        const localAc = new AbortController();
        const listenerOpts = { signal: localAc.signal };
        const cleanup = () => {
            localAc.abort();
            if (menu.parentNode)
                menu.parentNode.removeChild(menu);
        };
        // If the wall is torn down while the menu is open, bail out.
        if (signal.aborted) {
            finish(null);
            return;
        }
        signal.addEventListener("abort", () => finish(null), { once: true, signal: localAc.signal });
        if (createBtn) {
            createBtn.addEventListener("click", (ev) => {
                ev.preventDefault();
                ev.stopPropagation();
                finish("create-todo");
            }, listenerOpts);
        }
        deleteBtn.addEventListener("click", (ev) => {
            ev.preventDefault();
            ev.stopPropagation();
            finish("delete");
        }, listenerOpts);
        // Swallow our own contextmenu so a second right-click inside the menu
        // does not bubble back to the wall surface.
        menu.addEventListener("contextmenu", (ev) => {
            ev.preventDefault();
            ev.stopPropagation();
        }, listenerOpts);
        // Outside pointerdown / contextmenu dismiss. Capture so we see the event
        // before any other handler (e.g. the wall surface's own contextmenu).
        const dismissOnOutside = (ev) => {
            const target = ev.target;
            if (target && menu.contains(target))
                return;
            finish(null);
        };
        document.addEventListener("pointerdown", dismissOnOutside, {
            capture: true,
            signal: localAc.signal,
        });
        document.addEventListener("contextmenu", dismissOnOutside, {
            capture: true,
            signal: localAc.signal,
        });
        document.addEventListener("keydown", (ev) => {
            if (ev.key === "Escape") {
                ev.preventDefault();
                ev.stopPropagation();
                finish(null);
            }
        }, listenerOpts);
        // Defensive dismissal: if the viewport changes or the window loses
        // focus we drop the menu rather than leaving it floating at a stale
        // position.
        window.addEventListener("scroll", () => finish(null), { capture: true, ...listenerOpts });
        window.addEventListener("resize", () => finish(null), listenerOpts);
        window.addEventListener("blur", () => finish(null), listenerOpts);
        // Focus the first rendered item so keyboard users can act immediately.
        // When Create-Todo is hidden, Delete is the first (and only) item.
        requestAnimationFrame(() => {
            if (settled)
                return;
            (createBtn ?? deleteBtn).focus();
        });
    });
}
