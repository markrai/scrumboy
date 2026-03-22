import { apiFetch } from '../api.js';
import { getSlug, getTag, getSearch, getSprintIdFromUrl, getBoardLaneMeta } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { invalidateBoard, setBoardLimitPerLaneFloor } from '../orchestration/board-refresh.js';
import { recordBoardInteraction, recordLocalMutation } from '../realtime/guard.js';
import type { LanePageResponse, TodoStatus } from '../types.js';

// SortableJS is loaded globally
declare const Sortable: any;

// Module-level state for drag and drop
export let dragInProgress = false;
export let dragJustEnded = false;
let moveInFlight = false;
let activeSortables: any[] = [];
let boardColumns: Array<{ key: string; title: string; color?: string }> = columnsSpec();

// Board column specification
export function columnsSpec(): Array<{ key: string; title: string; color?: string }> {
  return [
    { key: "BACKLOG", title: "Backlog", color: undefined },
    { key: "NOT_STARTED", title: "Not Started", color: undefined },
    { key: "IN_PROGRESS", title: "In Progress", color: undefined },
    { key: "TESTING", title: "Testing", color: undefined },
    { key: "DONE", title: "Done", color: undefined },
  ];
}

export function setDnDColumns(columns: Array<{ key: string; title: string; color?: string }>): void {
  boardColumns = columns.length > 0 ? columns : columnsSpec();
}

const LANE_CARD_CLASSES = ['card--backlog', 'card--not_started', 'card--in_progress', 'card--testing', 'card--done'];

function updateCardColorOptimistic(card: Element, targetKey: string, targetColor?: string): void {
  const btn = (card instanceof HTMLButtonElement ? card : card.querySelector('button.card')) as HTMLElement | null;
  if (!btn) return;

  LANE_CARD_CLASSES.forEach((c) => btn.classList.remove(c));

  if (targetColor) {
    btn.style.borderColor = targetColor;
  } else {
    btn.style.borderColor = '';
    btn.classList.add(`card--${targetKey.toLowerCase()}`);
  }
}

function setMobileDragging(active: boolean): void {
  const wrapper = document.querySelector(".mobile-board-wrapper");
  if (wrapper) wrapper.classList.toggle("dragging", active);
}

function parseLocalId(el: Element | null): number | null {
  if (!el) return null;
  const raw = el.getAttribute("data-todo-local-id");
  if (raw == null) return null;
  const n = Number(raw);
  return Number.isFinite(n) && n > 0 ? n : null;
}

function hasActiveBoardSubsetFilter(): boolean {
  const sprintId = getSprintIdFromUrl();
  return !!(
    (getTag() && getTag().trim() !== "")
    || (getSearch() && getSearch().trim() !== "")
    || (sprintId && sprintId.trim() !== "")
  );
}

function getLaneItems(status: string): Element[] {
  const list = document.getElementById(`list_${status}`);
  if (!list) return [];
  return Array.from(list.querySelectorAll("[data-todo-local-id]"));
}

function preserveVisibleLaneCount(status: string, includePendingItem: boolean): void {
  const visibleCount = getLaneItems(status).length + (includePendingItem ? 1 : 0);
  setBoardLimitPerLaneFloor(visibleCount);
}

async function getHiddenLaneBoundaryLocalId(status: string): Promise<number | null> {
  const slug = getSlug();
  const meta = getBoardLaneMeta()[status as TodoStatus];
  if (!slug || !meta?.hasMore || !meta.nextCursor) return null;

  const params = new URLSearchParams();
  params.set("limit", "1");
  params.set("afterCursor", meta.nextCursor);

  const tag = getTag();
  const search = getSearch();
  const sprintId = getSprintIdFromUrl();
  if (tag) params.set("tag", tag);
  if (search) params.set("search", search);
  if (sprintId) params.set("sprintId", sprintId);

  const res = await apiFetch<LanePageResponse>(`/api/board/${slug}/lanes/${status}?${params.toString()}`);
  return res?.items?.[0]?.localId ?? null;
}

async function getFilteredLaneEndMove(status: string): Promise<{ afterId: number | null; beforeId: number | null }> {
  const items = getLaneItems(status);
  const afterId = parseLocalId(items[items.length - 1] ?? null);

  // Filtered bottom-of-lane drops need the first hidden match as a boundary.
  const beforeId = await getHiddenLaneBoundaryLocalId(status);
  return { afterId, beforeId };
}

export function initDnD(): void {
  // Destroy previous instances to prevent duplicate handlers
  for (const s of activeSortables) {
    try { s.destroy(); } catch (_) { /* element may already be removed */ }
  }
  activeSortables = [];

  const group = "board";

  const handleEnd = async (evt: any) => {
    dragInProgress = false;
    dragJustEnded = true;
    setTimeout(() => { dragJustEnded = false; }, 250);
    setMobileDragging(false);
    recordBoardInteraction();

    if (moveInFlight) return;

    try {
      const item = evt.item;
      if (!item) return;
      const todoLocalId = parseLocalId(item);
      if (!todoLocalId) return;

      const list = evt.to;
      const toStatus = list.getAttribute("data-status");
      if (!toStatus) return;

      const isTabDrop = !!list.closest("#mobileTabDropZones");
      const filteredSubsetActive = hasActiveBoardSubsetFilter();

      let afterId: number | null = null;
      let beforeId: number | null = null;
      if (isTabDrop) {
        if (filteredSubsetActive) {
          ({ afterId, beforeId } = await getFilteredLaneEndMove(toStatus));
        }
      } else {
        afterId = parseLocalId(item.previousElementSibling);
        beforeId = parseLocalId(item.nextElementSibling);
        if (filteredSubsetActive && beforeId == null) {
          beforeId = await getHiddenLaneBoundaryLocalId(toStatus);
        }
      }

      // No-op: dropped in the same position it started
      if (!isTabDrop && evt.from === evt.to && evt.oldIndex === evt.newIndex) return;

      if (filteredSubsetActive) {
        preserveVisibleLaneCount(toStatus, isTabDrop);
      }

      moveInFlight = true;
      recordLocalMutation();
      await apiFetch(`/api/board/${getSlug()}/todos/${todoLocalId}/move`, {
        method: "POST",
        body: JSON.stringify({ toStatus, afterId, beforeId }),
      });

      const targetCol = boardColumns.find((c) => c.key === toStatus);
      if (targetCol) {
        const cardEl = item.classList.contains('card') ? item : item.closest('.card');
        if (cardEl) updateCardColorOptimistic(cardEl, toStatus, targetCol.color);
      }

      const laneTitle = targetCol?.title || toStatus;
      showToast(`Todo moved to ${laneTitle}`);

      // Rely on SSE todo_moved event (debounced ~400ms) to refresh board; avoid double fetch.
    } catch (err: any) {
      showToast(err.message);
      invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl()).catch((e: any) => showToast(e.message));
    } finally {
      moveInFlight = false;
    }
  };

  boardColumns.forEach((c) => {
    const el = document.getElementById(`list_${c.key}`);
    if (!el) return;
    activeSortables.push(Sortable.create(el, {
      group,
      handle: ".card__drag-handle",
      animation: 150,
      ghostClass: "card--ghost",
      dragClass: "card--drag",
      forceFallback: true,
      fallbackOnBody: true,
      fallbackClass: "card--fallback",
      delay: 100,
      delayOnTouchOnly: true,
      onStart: () => {
        dragInProgress = true;
        dragJustEnded = false;
        setMobileDragging(true);
        recordBoardInteraction();
      },
      onEnd: handleEnd,
    }));
  });

  // Mobile tab drop zones: accept cards dragged onto the lane tabs
  boardColumns.forEach((c) => {
    const el = document.getElementById(`tab_drop_${c.key}`);
    if (!el) return;
    activeSortables.push(Sortable.create(el, {
      group,
      animation: 150,
      ghostClass: "card--ghost-tab",
      dragClass: "card--drag",
      onEnd: handleEnd,
    }));
  });
}
