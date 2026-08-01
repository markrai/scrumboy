import { apiFetch } from '../api.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { getAssigneeFromUrl, getSlug, getTag, getSearch, getSortFromUrl, getSprintIdFromUrl, getBoardLaneMeta } from '../state/selectors.js';
import { showToast } from '../utils.js';
import { invalidateBoard, setBoardLimitPerLaneFloor } from '../orchestration/board-refresh.js';
import { recordBoardInteraction, recordLocalMutation } from '../realtime/guard.js';
// Module-level state for drag and drop
export let dragInProgress = false;
export let dragJustEnded = false;
let moveInFlight = false;
let activeSortables = [];
let dragDropGeneration = 0;
let dragJustEndedTimer = null;
let boardColumns = columnsSpec();
let mobileTabIntroGlowTimer = null;
function isChronologicalSortActive() {
    const sort = getSortFromUrl();
    return sort === "newest" || sort === "oldest";
}
/**
 * Fallback column list when the board API omits `columnOrder` (should be rare).
 * Keys MUST match store/API workflow keys (`internal/store` DefaultColumn*).
 */
export function columnsSpec() {
    return [
        { key: "backlog", title: "Backlog", color: undefined },
        { key: "not_started", title: "Not Started", color: undefined },
        { key: "doing", title: "In Progress", color: undefined },
        { key: "testing", title: "Testing", color: undefined },
        { key: "done", title: "Done", color: undefined },
    ];
}
export function setDnDColumns(columns) {
    boardColumns = columns.length > 0 ? columns : columnsSpec();
}
const LANE_CARD_CLASSES = ['card--backlog', 'card--not_started', 'card--in_progress', 'card--doing', 'card--testing', 'card--done'];
/** Maps workflow column_key to existing card border CSS suffix (legacy `in_progress` vs API `doing`). */
function cardClassSuffixForLaneKey(key) {
    const k = key.toLowerCase();
    if (k === "doing")
        return "in_progress";
    return k;
}
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
        btn.classList.add(`card--${cardClassSuffixForLaneKey(targetKey)}`);
    }
}
function setMobileDragging(active) {
    const wrapper = document.querySelector(".mobile-board-wrapper");
    if (wrapper)
        wrapper.classList.toggle("dragging", active);
}
function clearPostDragClickSuppression() {
    if (dragJustEndedTimer != null) {
        clearTimeout(dragJustEndedTimer);
        dragJustEndedTimer = null;
    }
    dragJustEnded = false;
}
function suppressPostDragClick() {
    clearPostDragClickSuppression();
    dragJustEnded = true;
    dragJustEndedTimer = setTimeout(() => {
        dragJustEndedTimer = null;
        dragJustEnded = false;
    }, 250);
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
    const assignee = getAssigneeFromUrl();
    return !!((getTag() && getTag().trim() !== "")
        || (getSearch() && getSearch().trim() !== "")
        || (sprintId && sprintId.trim() !== "")
        || (assignee && assignee.trim() !== ""));
}
function getLaneItems(status) {
    const list = document.getElementById(`list_${status}`);
    if (!list)
        return [];
    return Array.from(list.querySelectorAll("[data-todo-local-id]"));
}
function preserveVisibleLaneCount(status, includePendingItem) {
    const slug = getSlug();
    if (!slug)
        return;
    const visibleCount = getLaneItems(status).length + (includePendingItem ? 1 : 0);
    setBoardLimitPerLaneFloor(visibleCount, slug);
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
    const assignee = getAssigneeFromUrl();
    const sort = getSortFromUrl();
    if (tag)
        params.set("tag", tag);
    if (search)
        params.set("search", search);
    if (sprintId)
        params.set("sprintId", sprintId);
    if (assignee)
        params.set("assignee", assignee);
    if (sort)
        params.set("sort", sort);
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
    const cancelledActiveDrag = dragInProgress;
    const generation = ++dragDropGeneration;
    const chronological = isChronologicalSortActive();
    // Destroy previous instances to prevent duplicate handlers
    for (const s of activeSortables) {
        try {
            s.destroy();
        }
        catch (_) { /* element may already be removed */ }
    }
    activeSortables = [];
    clearMobileTabIntroGlow();
    setMobileDragging(false);
    dragInProgress = false;
    if (cancelledActiveDrag)
        suppressPostDragClick();
    const group = "board";
    const callbackIsCurrent = () => generation === dragDropGeneration
        && chronological === isChronologicalSortActive();
    const handleEnd = async (evt) => {
        if (!callbackIsCurrent())
            return;
        dragInProgress = false;
        suppressPostDragClick();
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
            const sameLane = evt.from === evt.to || (fromStatus != null && fromStatus === toStatus);
            if (chronological && sameLane)
                return;
            const isTabDrop = !!list.closest("#mobileTabDropZones");
            const filteredSubsetActive = hasActiveBoardSubsetFilter();
            let afterId = null;
            let beforeId = null;
            if (chronological) {
                // Chronological DOM neighbors are not valid manual-rank anchors: a
                // cross-lane drop always appends to the end of the target lane's
                // rank order instead. Same-lane reordering is disabled via the
                // per-Sortable `sort: false` option set below.
            }
            else if (isTabDrop) {
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
            if (!callbackIsCurrent())
                return;
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
                showToast(t("board.todo.movedTo", { lane: laneTitle }));
            }
            // Rely on SSE todo_moved event (debounced ~400ms) to refresh board; avoid double fetch.
        }
        catch (err) {
            showToast(apiErrorMessage(err, { fallbackKey: "board.todo.moveFailed" }));
            invalidateBoard(getSlug(), getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl())
                .catch((e) => showToast(apiErrorMessage(e, { fallbackKey: "board.refreshFailed" })));
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
            sort: !chronological,
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
                if (!callbackIsCurrent())
                    return;
                clearPostDragClickSuppression();
                dragInProgress = true;
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
