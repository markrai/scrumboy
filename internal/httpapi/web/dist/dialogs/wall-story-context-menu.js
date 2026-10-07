import { wallDialog } from "../dom/elements.js";
import { t } from "../i18n/index.js";
export function openWallStoryContextMenu(clientX, clientY, signal, canEdit) {
    return new Promise((resolve) => {
        const host = wallDialog ?? document.body;
        const menu = document.createElement("div");
        menu.className = "context-menu wall-story-context-menu";
        menu.setAttribute("role", "menu");
        const actions = [
            { action: "open", key: "wall.menu.openStory" },
            ...(canEdit ? [{ action: "remove", key: "wall.menu.removeStory" }] : []),
        ];
        for (const item of actions) {
            const button = document.createElement("button");
            button.type = "button";
            button.className = "context-menu__item";
            button.setAttribute("role", "menuitem");
            button.dataset.action = item.action;
            button.dataset.i18nText = item.key;
            button.textContent = t(item.key);
            menu.appendChild(button);
        }
        menu.style.cssText = "left:0;top:0;visibility:hidden";
        host.appendChild(menu);
        const rect = menu.getBoundingClientRect();
        menu.style.left = `${Math.max(4, Math.min(clientX, window.innerWidth - rect.width - 4))}px`;
        menu.style.top = `${Math.max(4, Math.min(clientY, window.innerHeight - rect.height - 4))}px`;
        menu.style.visibility = "";
        let settled = false;
        const ac = new AbortController();
        const finish = (choice) => {
            if (settled)
                return;
            settled = true;
            ac.abort();
            menu.remove();
            resolve(choice);
        };
        if (signal.aborted)
            return finish(null);
        signal.addEventListener("abort", () => finish(null), { once: true, signal: ac.signal });
        menu.addEventListener("click", (event) => {
            const button = event.target?.closest("[data-action]");
            if (!button)
                return;
            event.preventDefault();
            event.stopPropagation();
            finish(button.dataset.action);
        }, { signal: ac.signal });
        menu.addEventListener("contextmenu", (event) => {
            event.preventDefault();
            event.stopPropagation();
        }, { signal: ac.signal });
        const outside = (event) => {
            if (!menu.contains(event.target))
                finish(null);
        };
        document.addEventListener("pointerdown", outside, { capture: true, signal: ac.signal });
        document.addEventListener("contextmenu", outside, { capture: true, signal: ac.signal });
        document.addEventListener("keydown", (event) => {
            if (event.key === "Escape")
                finish(null);
        }, { signal: ac.signal });
        requestAnimationFrame(() => menu.querySelector("button")?.focus());
    });
}
