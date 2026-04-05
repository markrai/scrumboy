import { apiFetch } from '../api.js';
import { getSlug, getTag, getSearch, getSprintIdFromUrl, getBoardLaneMeta } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { invalidateBoard, setBoardLimitPerLaneFloor } from '../orchestration/board-refresh.js';
import { recordBoardInteraction, recordLocalMutation } from '../realtime/guard.js';
// Module-level state for drag and drop
export let dragInProgress = false;
export let dragJustEnded = false;
let moveInFlight = false;
let activeSortables = [];
let boardColumns = columnsSpec();
let mobileTabIntroGlowTimer = null;
// Board column specification
export function columnsSpec() {
    return [
        { key: "BACKLOG", title: "Backlog", color: undefined },
        { key: "NOT_STARTED", title: "Not Started", color: undefined },
        { key: "IN_PROGRESS", title: "In Progress", color: undefined },
        { key: "TESTING", title: "Testing", color: undefined },
        { key: "DONE", title: "Done", color: undefined },
    ];
}
export function setDnDColumns(columns) {
    boardColumns = columns.length > 0 ? columns : columnsSpec();
}
const LANE_CARD_CLASSES = ['card--backlog', 'card--not_started', 'card--in_progress', 'card--testing', 'card--done'];
function updateCardColorOptimistic(card, targetKey, targetColor) {
    const btn = (card instanceof HTMLButtonElement ? card : card.querySelector('button.card'));
    if (!btn)
        return;
    LANE_CARD_CLASSES.forEach((c) => btn.classList.remove(c));
    if (targetColor) {
        btn.style.borderColor = targetColor;
    }
    else {
        btn.style.borderColor = '';
        btn.classList.add(`card--${targetKey.toLowerCase()}`);
    }
}
function setMobileDragging(active) {
    const wrapper = document.querySelector(".mobile-board-wrapper");
    if (wrapper)
        wrapper.classList.toggle("dragging", active);
}
function clearMobileTabIntroGlow() {
    if (mobileTabIntroGlowTimer != null) {
        clearTimeout(mobileTabIntroGlowTimer);
        mobileTabIntroGlowTimer = null;
    }
    document.getElementById("mobileTabDropZones")?.classList.remove("mobile-tab-drops--intro-glow");
}
function startMobileTabIntroGlow() {
    const zones = document.getElementById("mobileTabDropZones");
    if (!zones)
        return;
    clearMobileTabIntroGlow();
    zones.classList.add("mobile-tab-drops--intro-glow");
    mobileTabIntroGlowTimer = setTimeout(() => {
        mobileTabIntroGlowTimer = null;
        zones.classList.remove("mobile-tab-drops--intro-glow");
    }, 1000);
}
function parseLocalId(el) {
    if (!el)
        return null;
    const raw = el.getAttribute("data-todo-local-id");
    if (raw == null)
        return null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : null;
}
function hasActiveBoardSubsetFilter() {
    const sprintId = getSprintIdFromUrl();
    return !!((getTag() && getTag().trim() !== "")
        || (getSearch() && getSearch().trim() !== "")
        || (sprintId && sprintId.trim() !== ""));
}
function getLaneItems(status) {
    const list = document.getElementById(`list_${status}`);
    if (!list)
        return [];
    return Array.from(list.querySelectorAll("[data-todo-local-id]"));
}
function preserveVisibleLaneCount(status, includePendingItem) {
    const visibleCount = getLaneItems(status).length + (includePendingItem ? 1 : 0);
    setBoardLimitPerLaneFloor(visibleCount);
}
async function getHiddenLaneBoundaryLocalId(status) {
    const slug = getSlug();
    const meta = getBoardLaneMeta()[status];
    if (!slug || !meta?.hasMore || !meta.nextCursor)
        return null;
    const params = new URLSearchParams();
    params.set("limit", "1");
    params.set("afterCursor", meta.nextCursor);
    const tag = getTag();
    const search = getSearch();
    const sprintId = getSprintIdFromUrl();
    if (tag)
        params.set("tag", tag);
    if (search)
        params.set("search", search);
    if (sprintId)
        params.set("sprintId", sprintId);
    const res = await apiFetch(`/api/board/${slug}/lanes/${status}?${params.toString()}`);
    return res?.items?.[0]?.localId ?? null;
}
async function getFilteredLaneEndMove(status) {
    const items = getLaneItems(status);
    const afterId = parseLocalId(items[items.length - 1] ?? null);
    // Filtered bottom-of-lane drops need the first hidden match as a boundary.
    const beforeId = await getHiddenLaneBoundaryLocalId(status);
    return { afterId, beforeId };
}
export function initDnD() {
    clearMobileTabIntroGlow();
    // Destroy previous instances to prevent duplicate handlers
    for (const s of activeSortables) {
        try {
            s.destroy();
        }
        catch (_) { /* element may already be removed */ }
    }
    activeSortables = [];
    const group = "board";
    const handleEnd = async (evt) => {
        dragInProgress = false;
        dragJustEnded = true;
        setTimeout(() => { dragJustEnded = false; }, 250);
        clearMobileTabIntroGlow();
        setMobileDragging(false);
        recordBoardInteraction();
        if (moveInFlight)
            return;
        try {
            const item = evt.item;
            if (!item)
                return;
            const todoLocalId = parseLocalId(item);
            if (!todoLocalId)
                return;
            const list = evt.to;
            const toStatus = list.getAttribute("data-status");
            if (!toStatus)
                return;
            const fromStatus = evt.from?.getAttribute("data-status");
            const isTabDrop = !!list.closest("#mobileTabDropZones");
            const filteredSubsetActive = hasActiveBoardSubsetFilter();
            let afterId = null;
            let beforeId = null;
            if (isTabDrop) {
                if (filteredSubsetActive) {
                    ({ afterId, beforeId } = await getFilteredLaneEndMove(toStatus));
                }
            }
            else {
                afterId = parseLocalId(item.previousElementSibling);
                beforeId = parseLocalId(item.nextElementSibling);
                if (filteredSubsetActive && beforeId == null) {
                    beforeId = await getHiddenLaneBoundaryLocalId(toStatus);
                }
            }
            // No-op: dropped in the same position it started
            if (!isTabDrop && evt.from === evt.to && evt.oldIndex === evt.newIndex)
                return;
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
                if (cardEl)
                    updateCardColorOptimistic(cardEl, toStatus, targetCol.color);
            }
            const laneChanged = fromStatus != null && fromStatus !== toStatus;
            if (laneChanged) {
                const laneTitle = targetCol?.title ?? toStatus;
                showToast(`Todo moved to ${laneTitle}`);
            }
            // Rely on SSE todo_moved event (debounced ~400ms) to refresh board; avoid double fetch.
        }
        catch (err) {
            showToast(err.message);
            invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl()).catch((e) => showToast(e.message));
        }
        finally {
            moveInFlight = false;
        }
    };
    boardColumns.forEach((c) => {
        const el = document.getElementById(`list_${c.key}`);
        if (!el)
            return;
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
                startMobileTabIntroGlow();
                recordBoardInteraction();
            },
            onEnd: handleEnd,
        }));
    });
    // Mobile tab drop zones: accept cards dragged onto the lane tabs
    boardColumns.forEach((c) => {
        const el = document.getElementById(`tab_drop_${c.key}`);
        if (!el)
            return;
        activeSortables.push(Sortable.create(el, {
            group,
            animation: 150,
            ghostClass: "card--ghost-tab",
            dragClass: "card--drag",
            onEnd: handleEnd,
        }));
    });
}
