// Singleton-scoped mutable state for the Scrumbaby/Wall feature.
//
// This module is intentionally tiny. The goal is to put *all* cross-function
// module-level `let` bindings behind explicit getter/setter functions so the
// rest of the wall code (including future splits: wall-realtime, wall-edit,
// wall-drag-controller, ...) never imports a live `let` binding.
//
// Invariant: there is at most one mounted wall at a time. `setMounted(null)`
// is the "unmount" signal; everything else is a reference to the active
// session's state object.

import type { WallRole } from "./wall-permissions.js";
import type { WallDocument } from "./wall-rendering.js";

export type TransientEntry = {
  lastX: number;
  lastY: number;
  lastSentAt: number;
  timer: ReturnType<typeof setTimeout> | null;
};

export type Mounted = {
  projectId: number;
  slug: string;
  role: WallRole;
  canEdit: boolean;
  doc: WallDocument;
  userId: number | null;
  onRefreshNeeded: () => void;
  onTransient: (payload: unknown) => void;
  abort: AbortController;
  prevHtmlOverflow: string;
  /** Per-note transient coalescing. One map entry per actively dragging note. */
  transient: Map<string, TransientEntry>;
  /** Pending single-click color-cycle timers per note. */
  colorTimers: Map<string, ReturnType<typeof setTimeout>>;
  /** Track last-clicked timestamp per note for mouse dblclick fallback. */
  lastTapAt: Map<string, number>;
  /** Multi-select: ids of notes currently selected (marquee / Ctrl-click). */
  selected: Set<string>;
};

let mounted: Mounted | null = null;

/** Guard flags for the SSE refetch vs. in-progress-edit race. */
let activeEditNoteId: string | null = null;
let pendingRefetchWhileEditing = false;

// Phase 2: drag-active flag. Mirrors the edit guard but is a boolean because
// drag can involve multiple participants. The refresh-needed debounce in
// wall-realtime uses this to keep the DOM stable during a drag; the debounce
// re-arms until the drag ends, which converges to a single refetch.
let activeDrag = false;

export function getMounted(): Mounted | null {
  return mounted;
}

export function setMounted(next: Mounted | null): void {
  mounted = next;
}

export function getActiveEditNoteId(): string | null {
  return activeEditNoteId;
}

export function setActiveEditNoteId(id: string | null): void {
  activeEditNoteId = id;
}

export function getPendingRefetch(): boolean {
  return pendingRefetchWhileEditing;
}

export function setPendingRefetch(flag: boolean): void {
  pendingRefetchWhileEditing = flag;
}

/** Reset the edit-race flags. Called from teardown and from edit-finish. */
export function resetEditGuards(): void {
  activeEditNoteId = null;
  pendingRefetchWhileEditing = false;
}

export function isDragActive(): boolean {
  return activeDrag;
}

export function setDragActive(flag: boolean): void {
  activeDrag = flag;
}
