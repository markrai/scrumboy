/**
 * Close the topmost open <dialog> on outside interaction.
 *
 * Invariant: only the interactive content box (.dialog__form or .dialog__content)
 * counts as "inside". The <dialog> element itself is treated as outside (backdrop
 * / dimmed chrome), including when the click target is the dialog node — because
 * Node.contains() is true for the node itself, dialog.contains(dialog) would wrongly
 * suppress close without an explicit target === dialog branch.
 *
 * Pointerdown guard: if the gesture started inside the content box, do not close
 * on click (e.g. drag from inside to outside).
 */
let pointerStartedInsideContent = false;
let initialized = false;
function getTopOpenDialog() {
    const openDialogs = Array.from(document.querySelectorAll("dialog[open]"));
    if (openDialogs.length === 0)
        return null;
    return openDialogs[openDialogs.length - 1];
}
function getDialogContentBox(dialog) {
    return dialog.querySelector(".dialog__form, .dialog__content");
}
function onPointerDown(ev) {
    const t = ev.target;
    if (t == null || !(t instanceof Node)) {
        pointerStartedInsideContent = false;
        return;
    }
    const top = getTopOpenDialog();
    if (!top) {
        pointerStartedInsideContent = false;
        return;
    }
    const content = getDialogContentBox(top);
    if (!content) {
        pointerStartedInsideContent = false;
        return;
    }
    pointerStartedInsideContent = content.contains(t);
}
function onDocumentClick(ev) {
    if (pointerStartedInsideContent)
        return;
    const t = ev.target;
    if (t == null || !(t instanceof Node))
        return;
    const top = getTopOpenDialog();
    if (!top)
        return;
    const content = getDialogContentBox(top);
    if (!content)
        return;
    if (t === top) {
        top.close();
        return;
    }
    if (content.contains(t)) {
        return;
    }
    top.close();
}
export function initModalOutsideClickClose() {
    if (initialized)
        return;
    initialized = true;
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("click", onDocumentClick, true);
}
