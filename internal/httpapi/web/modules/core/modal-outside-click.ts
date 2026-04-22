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
/** Topmost open dialog at pointerdown; used to ignore synthetic clicks that open a newer modal mid-gesture (e.g. wall drag-to-trash confirm). */
let topAtPointerDown: HTMLDialogElement | null = null;
let initialized = false;

function getTopOpenDialog(): HTMLDialogElement | null {
  const openDialogs = Array.from(document.querySelectorAll("dialog[open]")) as HTMLDialogElement[];
  if (openDialogs.length === 0) return null;
  return openDialogs[openDialogs.length - 1];
}

function getDialogContentBox(dialog: HTMLDialogElement): Element | null {
  return dialog.querySelector(".dialog__form, .dialog__content");
}

function onPointerDown(ev: PointerEvent): void {
  topAtPointerDown = getTopOpenDialog();
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

function onDocumentClick(ev: MouseEvent): void {
  if (pointerStartedInsideContent) return;

  const t = ev.target;
  if (t == null || !(t instanceof Node)) return;

  const top = getTopOpenDialog();
  if (!top) return;
  // A modal opened during this gesture (e.g. confirm after drag-release).
  // Do not treat the follow-up synthetic click as "outside" the new topmost.
  if (topAtPointerDown !== top) return;

  const content = getDialogContentBox(top);
  if (!content) return;

  if (t === top) {
    top.close();
    return;
  }
  if (content.contains(t)) {
    return;
  }
  top.close();
}

export function initModalOutsideClickClose(): void {
  if (initialized) return;
  initialized = true;
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("click", onDocumentClick, true);
}
