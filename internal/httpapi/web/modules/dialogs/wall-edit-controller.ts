// Inline text-edit controller for Scrumbaby/Wall notes.
//
// This module owns the lifecycle of the editing textarea (entering edit mode,
// commit wiring, flushing any deferred SSE refetch that arrived while
// the user was typing). The actual textarea DOM swap lives in
// `wall-rendering.ts`; the scheduling of durable PATCHes lives in the caller
// (`onCommitText`) so this controller never imports wall-api directly.

import { enterEditMode, exitEditMode, isEditing, type WallNote } from "./wall-rendering.js";
import {
  getMounted,
  getPendingRefetch,
  setActiveEditNoteId,
  setPendingRefetch,
} from "./wall-state.js";
import { getCodePointLimitHandle } from "../unicode-limits.js";

export interface BeginEditOptions {
  /** Commit a text change to the server when the edited value differs. */
  onCommitText: (id: string, text: string) => void;
  /** Re-fetch the wall doc once the edit ends (only when a refresh was deferred). */
  onFlushDeferredRefetch: () => void;
}

/**
 * Swap a note display for a textarea and wire up commit (blur, Enter, Escape).
 *
 * Focus is synchronous on purpose: a preceding `renderSurface()` has just
 * rebuilt `#wallSurface`, and we need the textarea focused before any
 * follow-up SSE refetch can land. See `wall-realtime.refetchDoc` for the
 * corresponding defer-during-edit guard.
 */
export function beginEdit(noteEl: HTMLElement, note: WallNote, opts: BeginEditOptions): void {
  const state = getMounted();
  if (!state || !state.canEdit) return;
  if (isEditing(noteEl)) return;
  setActiveEditNoteId(note.id);
  const ta = enterEditMode(noteEl, note.text);
  ta.focus();
  const end = ta.value.length;
  try { ta.setSelectionRange(end, end); } catch { /* ignore */ }

  let finished = false;
  const finish = (commit: boolean) => {
    if (finished) return;
    if (commit) {
      const handle = getCodePointLimitHandle(ta);
      if (handle && !handle.report()) {
        // Keep editing: blur already fired, so restore focus and retain over-limit text.
        finished = false;
        queueMicrotask(() => {
          if (!finished && ta.isConnected) ta.focus();
        });
        return;
      }
    }
    finished = true;
    setActiveEditNoteId(null);
    const newText = ta.value;
    exitEditMode(noteEl, commit ? newText : note.text);
    ta.removeEventListener("blur", onBlur);
    ta.removeEventListener("keydown", onKey);
    if (commit && newText !== note.text) {
      opts.onCommitText(note.id, newText);
    }
    // Flush any refresh_needed events that arrived while the user was typing.
    if (getPendingRefetch()) {
      setPendingRefetch(false);
      opts.onFlushDeferredRefetch();
    }
  };
  const onBlur = () => finish(true);
  const onKey = (ev: KeyboardEvent) => {
    if (ev.key === "Escape") {
      ev.preventDefault();
      // Matches existing Wall/Postbaby behavior: Escape commits when valid.
      finish(true);
    } else if (ev.key === "Enter" && !ev.shiftKey) {
      ev.preventDefault();
      finish(true);
    }
    // Shift+Enter falls through to native newline insertion.
  };
  ta.addEventListener("blur", onBlur);
  ta.addEventListener("keydown", onKey);
}
