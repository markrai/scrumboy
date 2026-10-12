// Double-click-to-create target resolution for the board view.
//
// Double-clicking anywhere on a lane (header, padding, or empty list space)
// creates a card in that lane; double-clicking empty board background (below,
// beside, or between lanes) creates a card in the first column of the
// workflow. Double-clicks on cards or interactive controls resolve to null so
// existing behavior (single-click edit, buttons) is untouched.

/** Empty board surface: creating from here targets the first workflow column. */
const BACKGROUND_SELECTOR = ".board, .mobile-board-wrapper, .container, .page";

/**
 * Elements whose double-clicks must not trigger creation: cards (single-click
 * already opens edit), lane controls like "Load more", and form controls.
 */
const INTERACTIVE_SELECTOR =
  "[data-todo-id], [data-load-more], button, a, input, textarea, select, label, [role='button'], [contenteditable='true']";

/**
 * Resolve a double-click target to the column key a new card should go to,
 * or null when the double-click should do nothing.
 */
export function resolveDblClickCreateTarget(
  target: EventTarget | null,
  firstColumnKey: string,
): string | null {
  if (!(target instanceof Element)) return null;
  if (target.closest(INTERACTIVE_SELECTOR)) return null;
  const col = target.closest(".col");
  if (col) {
    // The agenda lane shows calendar events, not workflow todos.
    if (col.classList.contains("col--agenda")) return null;
    return col.getAttribute("data-column") || null;
  }
  return target.matches(BACKGROUND_SELECTOR) ? firstColumnKey : null;
}
