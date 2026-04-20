import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { ingestProjectsFromApp } from '../core/notifications.js';
import { fetchProjectMembers, invalidateMembersCache } from '../members-cache.js';
import { navigate } from '../router.js';
import { escapeHTML, showToast, renderAvatarContent, processImageFile } from '../utils.js';
import { getBoard, getMobileTab, getSlug, getTag, getSearch, getSprintIdFromUrl, getEditingTodo, getProjectId, getTagColors, getUser, getBoardLaneMeta, getLaneDisplayCount, getBoardMembers, } from '../state/selectors.js';
import { setProjectId, setBoard, setOpenTodoSegment, setMobileTab, setTagColors, setSettingsActiveTab, setBoardMembers, setLaneLoading, appendLaneTodos, } from '../state/mutations.js';
import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';
import { openTodoDialog } from '../dialogs/todo.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { initDnD, columnsSpec, setDnDColumns, dragInProgress, dragJustEnded } from '../features/drag-drop.js';
import { setContextMenuStatus, setContextMenuRole } from '../features/context-menu-button.js';
import { applyMobileLaneTabStyles, buildMobileTabsInnerHtml, mobileLaneTabStyleAttrForHtml, } from './mobile-lane-tabs.js';
import { registerBoardRefresher, registerSprintsRefresher, getBoardLimitPerLaneFloor, resetBoardLimitPerLaneFloor } from '../orchestration/board-refresh.js';
import { normalizeSprints } from '../sprints.js';
import { on, off } from '../events.js';
import { recordLocalMutation, } from '../realtime/guard.js';
import { buildBoardColumnsHtml, buildFiltersHtml, buildNoResultsHtml, buildTopbarHtml, getBoardColumns, renderVoiceCommandTriggerHtml, renderTodoCard, } from './board-rendering.js';
import { clearTodoMultiSelection, ensureBulkEditUi, getSelectedTodoIds, toggleTodoSelection, } from './board-selection.js';
import { bootstrapLoadedBoardView } from './board-load-bootstrap.js';
import { bindBoardFilterUi, clearSprintChipData, clearSprintChipDataIfSlugChanged, computeBoardChipsRender, ensureSprintSubscription, hasSprintChipDataForSlug, resetBoardFilterUiState, setSprintChipDataForSlug, updateChipsOnly, } from './board-filters.js';
export { notifySprintStateChanged } from './board-filters.js';
import { attachBoardInteractionListeners, clearPendingRealtimeRefresh, connectBoardEvents, debugLog, disconnectBoardEvents, markBoardLoadSucceeded, runWhileTodoDialogOpening, setInitialBoardLoadInFlight, } from './board-realtime.js';
import { canShowVoiceCommands } from './board-command-capabilities.js';
import { getVoiceFlowEnabledPreference } from '../core/voiceflow-preferences.js';
// Symbol for idempotent listener attachment
const BOUND_FLAG = Symbol('bound');
const HIGHLIGHT_CLASS = "card--highlight";
// Global variable to track user's role in current project
let currentUserProjectRole = null;
// Track last project ID we fetched members for to prevent duplicate fetches
let lastFetchedProjectId = null;
let boardLoadSequence = 0;
let resolverController = null;
let highlightRafId = null;
let highlightTimeoutId = null;
function getVoiceCommandContext() {
    const board = getBoard();
    const projectId = getProjectId();
    const projectSlug = getSlug();
    if (!board || projectId == null || !projectSlug)
        return null;
    return {
        projectId,
        projectSlug,
        board,
        members: getBoardMembers(),
        role: currentUserProjectRole,
    };
}
function canUseVoiceCommandContext(context) {
    return getVoiceFlowEnabledPreference() && !!context && canShowVoiceCommands({
        projectId: context.projectId,
        projectSlug: context.projectSlug,
        role: context.role,
        isTemporary: isTemporaryBoard(context.board),
        isAnonymous: isAnonymousBoard(context.board),
    });
}
function canShowVoiceCommandsForBoard(projectId, board) {
    return getVoiceFlowEnabledPreference() && canShowVoiceCommands({
        projectId,
        projectSlug: board.project?.slug,
        role: currentUserProjectRole,
        isTemporary: isTemporaryBoard(board),
        isAnonymous: isAnonymousBoard(board),
    });
}
function bindVoiceCommandButton() {
    const voiceCommandBtn = document.getElementById("voiceCommandBtn");
    if (!voiceCommandBtn || voiceCommandBtn[BOUND_FLAG])
        return;
    voiceCommandBtn.addEventListener("click", async () => {
        const openingContext = getVoiceCommandContext();
        if (!canUseVoiceCommandContext(openingContext)) {
            showToast("Commands are unavailable for this board");
            return;
        }
        const initialProjectId = openingContext.projectId;
        const initialProjectSlug = openingContext.projectSlug;
        try {
            const { openVoiceCommandDialog } = await import("../voice/flow.js");
            const latestContext = getVoiceCommandContext();
            if (!canUseVoiceCommandContext(latestContext)
                || latestContext.projectId !== initialProjectId
                || latestContext.projectSlug !== initialProjectSlug) {
                showToast("The board changed before commands opened");
                return;
            }
            openVoiceCommandDialog({
                initialProjectId,
                initialProjectSlug,
                getContext: getVoiceCommandContext,
                refreshBoard: async () => {
                    const context = getVoiceCommandContext();
                    if (!context || context.projectId !== initialProjectId || context.projectSlug !== initialProjectSlug)
                        return;
                    await loadBoardBySlug(context.projectSlug, getTag(), getSearch(), getSprintIdFromUrl());
                },
                openTodo: async (localId) => {
                    const context = getVoiceCommandContext();
                    if (!context || context.projectId !== initialProjectId || context.projectSlug !== initialProjectSlug)
                        return;
                    navigate(`/${context.projectSlug}/t/${localId}`);
                },
                recordMutation: recordLocalMutation,
                showMessage: showToast,
            });
        }
        catch (err) {
            showToast(err?.message || "Commands failed to load");
        }
    });
    voiceCommandBtn[BOUND_FLAG] = true;
}
function syncVoiceCommandPreferenceInTopbar() {
    const topbar = document.querySelector(".topbar");
    const board = getBoard();
    const projectId = getProjectId();
    const slug = getSlug();
    if (!topbar || !board || projectId == null || !slug)
        return;
    const showVoiceCommands = canShowVoiceCommandsForBoard(projectId, board);
    topbar.classList.toggle("topbar--voice-commands-on", showVoiceCommands);
    topbar.classList.toggle("topbar--voice-commands-off", !showVoiceCommands);
    const existing = document.getElementById("voiceCommandBtn");
    if (!showVoiceCommands) {
        existing?.remove();
        return;
    }
    if (!existing) {
        topbar.querySelector(".search-input-wrapper")?.insertAdjacentHTML("beforebegin", renderVoiceCommandTriggerHtml());
    }
    bindVoiceCommandButton();
}
on("voiceflow:enabled-changed", syncVoiceCommandPreferenceInTopbar);
/** Older builds stored uppercase `mobileTab_${slug}` values; workflow column_key is store-shaped (lowercase). */
const LEGACY_MOBILE_TAB_KEYS = {
    BACKLOG: "backlog",
    NOT_STARTED: "not_started",
    IN_PROGRESS: "doing",
    TESTING: "testing",
    DONE: "done",
};
function resolveMobileTabKeyFromStorage(saved, cols) {
    if (!saved || cols.length === 0)
        return null;
    if (cols.some((c) => c.key === saved))
        return saved;
    const mapped = LEGACY_MOBILE_TAB_KEYS[saved];
    if (mapped && cols.some((c) => c.key === mapped))
        return mapped;
    return null;
}
function hasActiveBoardSubsetFilter(tag, search, sprintId) {
    return !!((tag && tag.trim() !== "")
        || (search && search.trim() !== "")
        || (sprintId && sprintId.trim() !== ""));
}
function getRequestedBoardLimitPerLane(tag, search, sprintId) {
    if (!hasActiveBoardSubsetFilter(tag, search, sprintId))
        return 20;
    // Preserve the current filtered subset size across a drag-triggered refresh.
    const counts = Array.from(document.querySelectorAll(".col__list"))
        .map((el) => el.querySelectorAll("[data-todo-local-id]").length);
    return counts.length > 0 ? Math.max(20, ...counts) : 20;
}
/** Cached members lookup; rebuilt when members change. Avoids repeated Object.fromEntries during render. */
let membersByUserIdCache = {};
let membersByUserIdCacheSource = null;
function getMembersByUserId() {
    const members = getBoardMembers();
    if (members !== membersByUserIdCacheSource ||
        membersByUserIdCacheSource?.length !== members.length) {
        membersByUserIdCacheSource = members;
        membersByUserIdCache = Object.fromEntries(members.map((m) => [m.userId, m]));
    }
    return membersByUserIdCache;
}
/** Lightweight render signature for updateBoardContent skip; avoids stale UI from board-only comparison. */
let lastUpdateBoardContentBoard = null;
let lastUpdateBoardContentTag = "";
let lastUpdateBoardContentSearch = "";
let lastUpdateBoardContentSprintId = null;
// Runtime access to renderProjects from projects view (after Step 2)
// For now, we'll use a dynamic import that will work once projects.js exists
async function getRenderProjects() {
    try {
        // @ts-ignore - projects.js will exist after Step 2
        const projectsModule = await import('./projects.js');
        return projectsModule.renderProjects;
    }
    catch {
        return window.renderProjects || renderProjects;
    }
}
export function stopBoardEvents() {
    disconnectBoardEvents();
}
function isModifiedFibonacciModeEnabled() {
    const mode = getBoard()?.project?.estimationMode;
    return mode == null || mode === "MODIFIED_FIBONACCI";
}
function clearResolverRequest() {
    if (resolverController) {
        resolverController.abort();
        resolverController = null;
    }
}
export function abortTodoResolverRequest() {
    clearResolverRequest();
}
function replaceBoardPath(slug) {
    const url = new URL(window.location.href);
    const qs = url.search ? url.search : "";
    history.replaceState({}, "", `/${slug}${qs}`);
}
function findTodoInBoardByLocalId(localId) {
    const board = getBoard();
    if (!board || !board.columns)
        return null;
    const cols = board.columns;
    for (const c of Object.keys(cols)) {
        const todos = cols[c] || [];
        const t = todos.find((x) => x.localId === localId);
        if (t)
            return t;
    }
    return null;
}
function isSameEditingTodo(localId) {
    return (getEditingTodo()?.localId || null) === localId;
}
function scheduleCardHighlight(todo) {
    if (highlightRafId !== null) {
        cancelAnimationFrame(highlightRafId);
        highlightRafId = null;
    }
    if (highlightTimeoutId !== null) {
        clearTimeout(highlightTimeoutId);
        highlightTimeoutId = null;
    }
    const localId = todo.localId;
    highlightRafId = requestAnimationFrame(() => {
        highlightRafId = null;
        if (!isSameEditingTodo(localId))
            return;
        const el = (document.querySelector(`[data-todo-local-id="${localId}"]`) ||
            document.getElementById(`todo_${todo.id}`));
        if (!el)
            return;
        el.scrollIntoView({ behavior: "smooth", block: "nearest" });
        el.classList.add(HIGHLIGHT_CLASS);
        highlightTimeoutId = setTimeout(() => {
            highlightTimeoutId = null;
            el.classList.remove(HIGHLIGHT_CLASS);
        }, 2000);
    });
}
function attachBoardDelegationHandlers() {
    const boardEl = document.querySelector(".board");
    if (!boardEl)
        return;
    attachBoardInteractionListeners();
    if (boardEl[BOUND_FLAG])
        return;
    boardEl[BOUND_FLAG] = true;
    boardEl.addEventListener("click", (e) => {
        const card = e.target.closest("[data-todo-id]");
        if (card) {
            if (e.target.closest(".card__drag-handle"))
                return;
            if (dragInProgress || dragJustEnded)
                return;
            const me = e;
            const id = Number(card.getAttribute("data-todo-id"));
            const todo = findTodoInBoard(id);
            if (!todo)
                return;
            if (me.ctrlKey || me.metaKey) {
                if (currentUserProjectRole === "viewer") {
                    clearTodoMultiSelection();
                    openTodoFromCard(todo);
                    return;
                }
                e.preventDefault();
                e.stopPropagation();
                toggleTodoSelection(id);
                return;
            }
            clearTodoMultiSelection();
            openTodoFromCard(todo);
            return;
        }
        const loadMore = e.target.closest("[data-load-more]");
        if (loadMore) {
            document.activeElement?.blur();
            const status = loadMore.getAttribute("data-load-more");
            if (status)
                handleLoadMore(status);
            return;
        }
    });
    boardEl.addEventListener("contextmenu", (e) => {
        const colList = e.target.closest(".col__list");
        if (!colList)
            return;
        const contextMenu = document.getElementById("contextMenu");
        if (!contextMenu)
            return;
        e.preventDefault();
        const status = colList.getAttribute("data-status");
        if (status) {
            setContextMenuStatus(status);
            setContextMenuRole(currentUserProjectRole);
            const contextMenuNewTodo = document.getElementById("contextMenuNewTodo");
            if (contextMenuNewTodo) {
                contextMenuNewTodo.style.display =
                    isTemporaryBoard(getBoard()) || currentUserProjectRole === "maintainer" ? "" : "none";
            }
            const mouseEvent = e;
            contextMenu.style.display = "block";
            contextMenu.style.left = `${mouseEvent.pageX}px`;
            contextMenu.style.top = `${mouseEvent.pageY}px`;
        }
    });
    ensureBulkEditUi({
        getRole: () => currentUserProjectRole,
        syncSelectionClasses: (selectedIds) => {
            const currentBoardEl = document.querySelector(".board");
            if (!currentBoardEl)
                return;
            currentBoardEl.querySelectorAll("[data-todo-id]").forEach((el) => {
                const id = Number(el.getAttribute("data-todo-id"));
                if (!Number.isFinite(id))
                    return;
                el.classList.toggle("card--selected", selectedIds.has(id));
            });
        },
    });
}
/**
 * Patch assignee avatars into cards that were rendered without members.
 * Avoids full board rebuild when members arrive after first paint.
 * Call after setBoardMembers(members) so getMembersByUserId() returns the new lookup.
 */
function hydrateAvatarsOnCards(members) {
    const boardEl = document.querySelector(".board");
    if (!boardEl)
        return;
    const cards = Array.from(boardEl.querySelectorAll("[data-assignee-user-id]"));
    const toHydrate = cards.filter((c) => c.dataset.avatarHydrated !== "1");
    if (toHydrate.length === 0)
        return;
    const membersByUserId = getMembersByUserId();
    toHydrate.forEach((card) => {
        const assigneeUserId = parseInt(card.getAttribute("data-assignee-user-id") ?? "", 10);
        if (!Number.isFinite(assigneeUserId))
            return;
        const assignee = membersByUserId[assigneeUserId];
        if (!assignee)
            return;
        const avatarHTML = `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`;
        const badges = card.querySelector(".card__badges");
        if (badges) {
            badges.insertAdjacentHTML("beforeend", avatarHTML);
        }
        else {
            const footer = card.querySelector(".card__footer");
            if (footer) {
                footer.insertAdjacentHTML("beforeend", avatarHTML);
            }
            else {
                const dragHandle = card.querySelector(".card__drag-handle");
                if (dragHandle) {
                    dragHandle.insertAdjacentHTML("afterend", `<div class="card__footer">${avatarHTML}</div>`);
                }
            }
        }
        card.dataset.avatarHydrated = "1";
    });
}
function openTodoFromCard(todo) {
    void runWhileTodoDialogOpening(() => openTodoDialog({ mode: "edit", todo, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole })).catch((err) => {
        console.warn("Failed to open todo dialog:", err?.message || err);
    });
    setOpenTodoSegment(String(todo.localId));
    const slug = getSlug();
    if (!slug)
        return;
    const url = new URL(window.location.href);
    const targetPath = `/${slug}/t/${todo.localId}`;
    if (url.pathname === targetPath)
        return;
    history.pushState({}, "", `${targetPath}${url.search}`);
}
// Load more todos for a lane (targeted column append)
async function handleLoadMore(status) {
    const slug = getSlug();
    const tag = getTag();
    const search = getSearch();
    const sprintId = getSprintIdFromUrl();
    if (!slug)
        return;
    const meta = getBoardLaneMeta()[status];
    if (!meta?.hasMore || meta.loading)
        return;
    setLaneLoading(status, true);
    try {
        const params = new URLSearchParams();
        params.set("limit", "20");
        if (meta.nextCursor)
            params.set("afterCursor", meta.nextCursor);
        if (tag)
            params.set("tag", tag);
        if (search)
            params.set("search", search);
        if (sprintId)
            params.set("sprintId", sprintId);
        const qs = params.toString();
        const res = await apiFetch(`/api/board/${slug}/lanes/${status}${qs ? `?${qs}` : ""}`);
        const items = res?.items ?? [];
        const nextCursor = res?.nextCursor ?? null;
        const hasMore = res?.hasMore ?? false;
        appendLaneTodos(status, items, nextCursor ?? null, hasMore);
        // Targeted column append: add cards to #list_{status}
        // Card clicks are handled by delegated handler on .board
        const listEl = document.getElementById(`list_${status}`);
        if (listEl) {
            const board = getBoard();
            const columnColor = board?.columnOrder?.find((col) => col.key === status)?.color;
            const membersByUserId = getMembersByUserId();
            const tagColors = getTagColors();
            const showPointsMode = isModifiedFibonacciModeEnabled();
            const cardOpts = { tagColors, showPointsMode, selectedIds: getSelectedTodoIds() };
            items.forEach((t) => {
                const card = document.createElement("div");
                card.innerHTML = renderTodoCard(t, columnColor, membersByUserId, cardOpts);
                const btn = card.firstElementChild;
                if (btn)
                    listEl.appendChild(btn);
            });
        }
        // Update Load more button visibility
        const loadMoreEl = document.querySelector(`[data-load-more="${status}"]`);
        if (loadMoreEl) {
            loadMoreEl.style.display = hasMore ? "" : "none";
        }
        // Update column count (total in lane, not displayed count)
        const countEl = document.querySelector(`[data-count-for="${status}"]`);
        if (countEl) {
            countEl.textContent = String(getLaneDisplayCount(status));
        }
        updateMobileTabs();
    }
    catch (err) {
        showToast(err.message || "Failed to load more");
    }
    finally {
        setLaneLoading(status, false);
        checkMobileLoadMoreVisibility();
    }
}
// Find a todo in the board by ID
function findTodoInBoard(id) {
    const board = getBoard();
    if (!board || !board.columns)
        return null;
    const cols = board.columns;
    for (const c of Object.keys(cols)) {
        const todos = cols[c] || [];
        const t = todos.find((x) => x.id === id);
        if (t)
            return t;
    }
    return null;
}
// Per-lane scroll handler refs so we can call them on tab switch
const mobileLoadMoreHandlers = new Map();
// Set up scroll-based visibility for the mobile triangle per lane
function initMobileLoadMoreVisibility() {
    if (window.innerWidth > 620)
        return;
    mobileLoadMoreHandlers.clear();
    document.querySelectorAll("[data-load-more]").forEach((loadMoreEl) => {
        const status = loadMoreEl.getAttribute("data-load-more");
        if (!status)
            return;
        const listEl = document.getElementById(`list_${status}`);
        if (!listEl)
            return;
        const update = () => {
            const meta = getBoardLaneMeta()[status];
            const distFromBottom = listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight;
            const atBottom = distFromBottom < 40;
            if (atBottom && meta?.hasMore && !meta?.loading) {
                loadMoreEl.classList.add("visible");
            }
            else {
                loadMoreEl.classList.remove("visible");
            }
        };
        mobileLoadMoreHandlers.set(status, update);
        listEl.addEventListener("scroll", update, { passive: true });
        update();
    });
}
function checkMobileLoadMoreVisibility() {
    mobileLoadMoreHandlers.forEach((fn) => fn());
}
// Update mobile tabs display
function bindMobileTabClickHandlersIfNeeded() {
    const mobileTabsEl = document.getElementById("mobileTabs");
    if (!mobileTabsEl)
        return;
    mobileTabsEl.querySelectorAll("[data-tab]").forEach((el) => {
        if (!el[BOUND_FLAG]) {
            el.addEventListener("click", (e) => {
                e.preventDefault();
                const tab = el.getAttribute("data-tab");
                if (tab) {
                    setMobileTab(tab);
                    const slug = getSlug();
                    if (slug) {
                        localStorage.setItem(`mobileTab_${slug}`, tab);
                    }
                    updateMobileTabs();
                }
            });
            el[BOUND_FLAG] = true;
        }
    });
}
/** If the active lane was removed or is unknown, fall back to the first column. */
function ensureMobileTabForBoard(board) {
    const cols = getBoardColumns(board);
    if (cols.length === 0)
        return;
    const keys = new Set(cols.map((c) => c.key));
    const cur = getMobileTab();
    if (!cur || !keys.has(cur)) {
        const next = cols[0].key;
        setMobileTab(next);
        const slug = getSlug();
        if (slug)
            localStorage.setItem(`mobileTab_${slug}`, next);
    }
}
/**
 * Keeps mobile tab buttons and drop overlays in sync with workflow (colors, labels, counts).
 * Rebuilds the strip when lane count/order changes; otherwise updates styles in place.
 */
function syncMobileLaneTabsStrip(board) {
    const mobileTabsEl = document.getElementById("mobileTabs");
    if (!mobileTabsEl)
        return;
    const boardCols = getBoardColumns(board);
    const existingTabs = mobileTabsEl.querySelectorAll(":scope > .mobile-tab");
    const orderMatch = existingTabs.length === boardCols.length &&
        boardCols.every((c, i) => existingTabs[i]?.getAttribute("data-tab") === c.key);
    if (!orderMatch) {
        mobileTabsEl.innerHTML = buildMobileTabsInnerHtml(boardCols, {
            activeTabKey: getMobileTab(),
            laneLabel: (key) => {
                const col = boardCols.find((c) => c.key === key);
                const title = col?.title ?? "";
                return `${title} ${getLaneDisplayCount(key)}`;
            },
        });
        bindMobileTabClickHandlersIfNeeded();
        return;
    }
    const tabByKey = new Map();
    mobileTabsEl.querySelectorAll(":scope > .mobile-tab").forEach((el) => {
        const k = el.getAttribute("data-tab");
        if (k)
            tabByKey.set(k, el);
    });
    const dropByKey = new Map();
    const dropContainer = document.getElementById("mobileTabDropZones");
    if (dropContainer) {
        dropContainer.querySelectorAll(".mobile-tab-drop").forEach((el) => {
            const k = el.getAttribute("data-status");
            if (k)
                dropByKey.set(k, el);
        });
    }
    boardCols.forEach((c) => {
        const tab = tabByKey.get(c.key);
        if (!tab)
            return;
        applyMobileLaneTabStyles(tab, c, "tab");
        const textSpan = tab.querySelector(".mobile-tab__text");
        const label = `${c.title} ${getLaneDisplayCount(c.key)}`;
        if (textSpan)
            textSpan.textContent = label;
        else
            tab.textContent = label;
        const drop = dropByKey.get(c.key);
        if (drop)
            applyMobileLaneTabStyles(drop, c, "drop");
    });
}
function updateMobileTabs() {
    const board = getBoard();
    const boardCols = board ? getBoardColumns(board) : columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "done" }));
    const firstKey = boardCols[0]?.key || "backlog";
    const slug = getSlug();
    if (!getMobileTab()) {
        const raw = slug ? localStorage.getItem(`mobileTab_${slug}`) : null;
        const resolved = resolveMobileTabKeyFromStorage(raw, boardCols);
        setMobileTab((resolved ?? firstKey));
    }
    // Update tab active states
    const tabs = document.querySelectorAll(".mobile-tab");
    tabs.forEach((tab) => {
        const tabKey = tab.getAttribute("data-tab");
        if (tabKey === getMobileTab()) {
            tab.classList.add("mobile-tab--active");
        }
        else {
            tab.classList.remove("mobile-tab--active");
        }
    });
    // Show/hide columns based on active tab
    const columns = document.querySelectorAll(".board .col");
    columns.forEach((col) => {
        const colKey = col.getAttribute("data-column");
        if (colKey && colKey === getMobileTab()) {
            col.classList.add("col--mobile-active");
        }
        else {
            col.classList.remove("col--mobile-active");
        }
    });
    checkMobileLoadMoreVisibility();
}
// Handle project image upload
async function handleProjectImageUpload(projectId) {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/*";
    input.onchange = async (e) => {
        const file = e.target.files?.[0];
        if (!file)
            return;
        try {
            const finalDataUrl = await processImageFile(file);
            recordLocalMutation();
            await apiFetch(`/api/projects/${projectId}`, {
                method: "PATCH",
                body: JSON.stringify({ image: finalDataUrl }),
            });
            syncTopbarFromBoard({ project: { image: finalDataUrl } });
            if (getSlug()) {
                await loadBoardBySlug(getSlug(), getTag(), getSearch(), getSprintIdFromUrl());
            }
            else {
                const renderProjects = await getRenderProjects();
                await renderProjects();
            }
            showToast("Project image updated");
        }
        catch (err) {
            showToast(err?.message ?? String(err) ?? "Upload failed");
        }
    };
    input.click();
}
/** Sync #projectImageBtn children from board.project.image (incremental board updates). */
function syncTopbarFromBoard(board) {
    const btn = document.getElementById("projectImageBtn");
    if (!btn)
        return;
    const img = btn.querySelector("img.project-image-topbar") ?? btn.querySelector("img");
    if (board.project.image) {
        const src = board.project.image;
        if (img) {
            img.src = src;
        }
        else {
            btn.innerHTML = `<img src="${escapeHTML(src)}" alt="" class="project-image-topbar" />`;
        }
    }
    else {
        if (img)
            img.remove();
        if (!btn.querySelector(".project-image-topbar-placeholder")) {
            btn.innerHTML = `<span class="project-image-topbar-placeholder">📷</span>`;
        }
    }
}
// Full board + filters update. Use for SSE refresh, filter change, search.
// For chips-only updates (e.g. deferred sprints load), use updateChipsOnly instead.
function updateBoardContent(board, tag, search, sprintId) {
    // Skip full rebuild when render signature matches (board + tag + search + sprintId)
    if (board === lastUpdateBoardContentBoard &&
        tag === lastUpdateBoardContentTag &&
        search === lastUpdateBoardContentSearch &&
        sprintId === lastUpdateBoardContentSprintId) {
        return;
    }
    setBoard(board);
    ensureMobileTabForBoard(board);
    // Update tag colors from board data
    const tagColors = { ...getTagColors() };
    board.tags.forEach(t => {
        if (t.color) {
            tagColors[t.name] = t.color;
        }
    });
    setTagColors(tagColors);
    const isAnonymousTempBoard = isAnonymousBoard(board);
    const { chipsHTML, chipsUnchanged } = computeBoardChipsRender(board, tag || "", sprintId ?? null);
    // Chips guard: skip filters DOM and initMobileTagPagination when chips HTML unchanged
    if (!chipsUnchanged) {
        const filtersEl = document.querySelector(".filters");
        if (filtersEl) {
            filtersEl.innerHTML = buildFiltersHtml(chipsHTML, { innerOnly: true });
            bindBoardFilterUi({
                reloadBoard: loadBoardBySlug,
                showError: (message) => showToast(message),
            });
        }
    }
    // Precompute for card render loop
    const showPointsMode = isModifiedFibonacciModeEnabled();
    const membersByUserId = getMembersByUserId();
    // Update board columns
    const boardEl = document.querySelector(".board");
    if (boardEl) {
        // Remove existing "No results" message if present
        const existingNoResults = boardEl.querySelector(".no-results");
        if (existingNoResults) {
            existingNoResults.remove();
        }
        const boardCols = getBoardColumns(board);
        setDnDColumns(boardCols.map((c) => ({ key: c.key, title: c.title, color: c.color })));
        const cardOpts = { tagColors, showPointsMode, selectedIds: getSelectedTodoIds() };
        boardEl.innerHTML = buildBoardColumnsHtml({
            boardCols,
            board,
            activeMobileTab: getMobileTab(),
            laneMetaByKey: getBoardLaneMeta(),
            laneDisplayCount: (key) => getLaneDisplayCount(key),
            membersByUserId,
            cardOpts,
        });
        // Add "No results" state if search is active and no todos match
        if (search && search.trim() !== "") {
            const totalTodos = Object.values(board.columns).reduce((sum, todos) => sum + todos.length, 0);
            if (totalTodos === 0) {
                boardEl.insertAdjacentHTML("beforeend", buildNoResultsHtml(search));
            }
        }
    }
    syncMobileLaneTabsStrip(board);
    updateMobileTabs();
    // DnD must run after mobile tab strip DOM is final (Sortable binds #tab_drop_* inside #mobileTabDropZones).
    if (currentUserProjectRole === "maintainer" || isTemporaryBoard(board)) {
        initDnD();
    }
    initMobileLoadMoreVisibility();
    lastUpdateBoardContentBoard = board;
    lastUpdateBoardContentTag = tag;
    lastUpdateBoardContentSearch = search;
    lastUpdateBoardContentSprintId = sprintId;
}
function renderBoardFromData(board, projectId, tag, search, sprintId, opts = {}) {
    const boardCols = getBoardColumns(board);
    setDnDColumns(boardCols.map((c) => ({ key: c.key, title: c.title, color: c.color })));
    // Detect mobile view for placeholder text
    const isMobile = window.innerWidth <= 620;
    const searchPlaceholder = isMobile ? "Search" : "Search todos...";
    const backLabel = opts.backLabel || "← Projects";
    const backHref = opts.backHref || "";
    const minimalTopbar = !!opts.minimalTopbar;
    setProjectId(projectId);
    setBoard(board);
    if (board.project?.id != null && board.project.slug) {
        ingestProjectsFromApp([board.project]);
    }
    // Role is now resolved in loadBoardBySlug before calling renderBoardFromData.
    // Initialize DnD if user is maintainer (role already set).
    if (currentUserProjectRole === "maintainer" || isTemporaryBoard(board)) {
        initDnD();
    }
    // Restore saved mobile tab for this project
    const initialCols = getBoardColumns(board);
    const firstColKey = initialCols[0]?.key || "backlog";
    const slug = getSlug();
    if (slug) {
        const raw = localStorage.getItem(`mobileTab_${slug}`);
        const resolved = resolveMobileTabKeyFromStorage(raw, initialCols);
        setMobileTab((resolved ?? firstColKey));
    }
    else {
        setMobileTab(firstColKey);
    }
    // Check if we're already on a board page - if so, only update board content
    // We check for the board container, not just the topbar, because projects page also has a topbar
    const existingBoardContainer = document.querySelector(".board");
    if (existingBoardContainer) {
        updateBoardContent(board, tag, search, sprintId);
        syncTopbarFromBoard(board);
        return;
    }
    // Update tag colors from board data
    const tagColors = { ...getTagColors() };
    board.tags.forEach(t => {
        if (t.color) {
            tagColors[t.name] = t.color;
        }
    });
    setTagColors(tagColors);
    // Anonymous temporary board: expiresAt set, no creator (pastebin-style). Rename + New Todo without login — see isAnonymousBoard() / backend.
    const isAnonymousTempBoard = isAnonymousBoard(board);
    const { chipsHTML } = computeBoardChipsRender(board, tag || "", sprintId ?? null);
    const showVoiceCommands = canShowVoiceCommandsForBoard(projectId, board);
    // Minimal topbar (used for temporary/anonymous boards): logo, project name, rename (if anonymous temp), New Todo, Settings
    const topbarHTML = buildTopbarHtml({
        board,
        minimalTopbar,
        search,
        searchPlaceholder,
        isMobile,
        isAnonymousTempBoard,
        currentUserProjectRole,
        showVoiceCommands,
        user: getUser(),
        backLabel,
    });
    const membersByUserId = getMembersByUserId();
    const showPointsMode = isModifiedFibonacciModeEnabled();
    const cardOpts = { tagColors, showPointsMode, selectedIds: getSelectedTodoIds() };
    app.innerHTML = `
    <div class="page">
      ${topbarHTML}

      <div class="container">
        ${buildFiltersHtml(chipsHTML)}

        <div class="mobile-board-wrapper">
          <div class="mobile-tabs" id="mobileTabs">
            ${boardCols.map((c) => {
        const { tab: tabStyle } = mobileLaneTabStyleAttrForHtml(c);
        const dk = escapeHTML(c.key);
        return `
            <button class="mobile-tab ${getMobileTab() === c.key ? "mobile-tab--active" : ""}" data-tab="${dk}"${tabStyle}><span class="mobile-tab__text">${escapeHTML(c.title)} ${getLaneDisplayCount(c.key)}</span></button>
            `;
    }).join("")}
            <div id="mobileTabDropZones">
              ${boardCols.map((c) => {
        const { drop: dropStyle } = mobileLaneTabStyleAttrForHtml(c);
        const dk = escapeHTML(c.key);
        return `<div id="tab_drop_${c.key}" class="mobile-tab-drop" data-status="${dk}"${dropStyle}></div>`;
    }).join("")}
            </div>
          </div>

          <div class="board">
          ${buildBoardColumnsHtml({
        boardCols,
        board,
        activeMobileTab: getMobileTab(),
        laneMetaByKey: getBoardLaneMeta(),
        laneDisplayCount: (key) => getLaneDisplayCount(key),
        membersByUserId,
        cardOpts,
    })}
          </div>
        </div>
      </div>
    </div>
  `;
    // Only attach event listeners for elements that exist (anonymous mode omits some)
    const brandLink = document.getElementById("brandLink");
    if (brandLink && !brandLink[BOUND_FLAG]) {
        brandLink.addEventListener("click", async () => {
            try {
                // Copy current URL to clipboard before navigating
                const currentUrl = window.location.href;
                await navigator.clipboard.writeText(currentUrl);
                // Navigate immediately, toast will show on landing page
                window.location.href = "/?copied=1";
            }
            catch (err) {
                // Fallback if clipboard API fails (e.g., insecure context)
                window.location.href = "/?copied=0";
            }
        });
        brandLink[BOUND_FLAG] = true;
    }
    const backBtn = document.getElementById("backBtn");
    if (backBtn && !backBtn[BOUND_FLAG]) {
        backBtn.addEventListener("click", () => {
            const isRelativePath = !backHref || (!backHref.startsWith("http://") && !backHref.startsWith("https://"));
            if (isRelativePath) {
                navigate(backHref || "/");
                return;
            }
            window.location.href = backHref;
        });
        backBtn[BOUND_FLAG] = true;
    }
    const projectImageBtn = document.getElementById("projectImageBtn");
    if (projectImageBtn && !projectImageBtn[BOUND_FLAG]) {
        projectImageBtn.addEventListener("click", async () => {
            await handleProjectImageUpload(projectId);
        });
        projectImageBtn[BOUND_FLAG] = true;
    }
    const renameProjectBtn = document.getElementById("renameProjectBtn");
    if (renameProjectBtn && !renameProjectBtn[BOUND_FLAG]) {
        renameProjectBtn.addEventListener("click", async () => {
            // Create a dialog for renaming (consistent with app style)
            const dialog = document.createElement("dialog");
            dialog.className = "dialog";
            dialog.innerHTML = `
        <form method="dialog" class="dialog__form" id="renameProjectForm">
          <div class="dialog__header">
            <div class="dialog__title">Rename Project</div>
            <button class="btn btn--ghost" type="button" id="renameProjectDialogClose" aria-label="Close">✕</button>
          </div>

          <label class="field">
            <div class="field__label">Project Name</div>
            <input 
              type="text" 
              id="renameProjectName" 
              class="input" 
              placeholder="Project name" 
              maxlength="200" 
              value="${escapeHTML(board.project.name)}"
              required 
              autofocus
            />
          </label>

          <div class="dialog__footer">
            <div class="spacer"></div>
            <button type="button" class="btn btn--ghost" id="renameProjectCancel">Cancel</button>
            <button type="submit" class="btn" id="renameProjectSubmit">Rename</button>
          </div>
        </form>
      `;
            document.body.appendChild(dialog);
            dialog.showModal();
            const closeBtn = document.getElementById("renameProjectDialogClose");
            const cancelBtn = document.getElementById("renameProjectCancel");
            const form = document.getElementById("renameProjectForm");
            const nameInput = document.getElementById("renameProjectName");
            const close = () => {
                document.body.removeChild(dialog);
            };
            if (closeBtn) {
                closeBtn.addEventListener("click", close);
            }
            if (cancelBtn) {
                cancelBtn.addEventListener("click", close);
            }
            dialog.addEventListener("click", (e) => {
                if (e.target === dialog) {
                    close();
                }
            });
            form.addEventListener("submit", async (e) => {
                e.preventDefault();
                const newName = nameInput.value.trim();
                if (!newName || newName === board.project.name) {
                    close();
                    return;
                }
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/projects/${projectId}`, {
                        method: "PATCH",
                        body: JSON.stringify({ name: newName }),
                    });
                    // Update the project name in the DOM immediately
                    const topbar = document.querySelector(".topbar");
                    if (topbar) {
                        // Find the .brand element that contains the project name
                        // In minimal topbar: first .brand has logo button, second has project name
                        // In regular topbar: only one .brand with project name
                        const brandElements = Array.from(topbar.querySelectorAll(".brand"));
                        // Find the .brand that doesn't contain a button (the one with project name)
                        for (const brand of brandElements) {
                            if (!brand.querySelector("button")) {
                                brand.textContent = newName;
                                break;
                            }
                        }
                        // Fallback: if all have buttons, update the last one (shouldn't happen, but safe)
                        if (brandElements.length > 0 && brandElements[brandElements.length - 1].querySelector("button")) {
                            brandElements[brandElements.length - 1].textContent = newName;
                        }
                    }
                    // Update the board state to reflect the new name
                    const currentBoard = getBoard();
                    if (currentBoard) {
                        currentBoard.project.name = newName;
                        setBoard(currentBoard);
                    }
                    close();
                    showToast("Project renamed");
                }
                catch (err) {
                    showToast(err.message);
                }
            });
        });
        renameProjectBtn[BOUND_FLAG] = true;
    }
    const newTodoBtn = document.getElementById("newTodoBtn");
    if (newTodoBtn && !newTodoBtn[BOUND_FLAG]) {
        newTodoBtn.addEventListener("click", () => openTodoDialog({ mode: "create", role: currentUserProjectRole }));
        newTodoBtn[BOUND_FLAG] = true;
    }
    bindVoiceCommandButton();
    // Setup manage members button event listener (extracted for reuse)
    const setupManageMembersButton = (projId, projectName) => {
        const btn = document.getElementById("manageMembersBtn");
        if (btn && !btn[BOUND_FLAG]) {
            btn.addEventListener("click", async () => {
                try {
                    // Remove any existing members dialog first to prevent duplicates
                    const existingDialog = document.getElementById("membersDialog");
                    if (existingDialog && existingDialog.parentNode) {
                        if (existingDialog.open) {
                            existingDialog.close();
                        }
                        document.body.removeChild(existingDialog);
                    }
                    // Bypass cache: fetch members directly so modal always shows current state.
                    // (fetchProjectMembers can return stale data; maintainers and contributors may see different lists otherwise.)
                    const currentMembers = await apiFetch(`/api/projects/${projId}/members`);
                    const members = Array.isArray(currentMembers) ? currentMembers : [];
                    const isMaintainer = currentUserProjectRole === "maintainer";
                    let available = [];
                    if (isMaintainer) {
                        available = await apiFetch(`/api/projects/${projId}/available-users`);
                        if (!Array.isArray(available))
                            available = [];
                    }
                    const currentUserId = getUser()?.id;
                    // Create dialog
                    const dialog = document.createElement("dialog");
                    dialog.id = "membersDialog";
                    dialog.className = "dialog";
                    const roleLower = (r) => String(r || "").toLowerCase();
                    const authorityRoles = ["maintainer"];
                    const isRemovableRole = (r) => ["contributor", "editor", "viewer"].includes(roleLower(r));
                    const isAuthorityRole = (r) => authorityRoles.includes(roleLower(r));
                    const renderMembersList = () => {
                        if (members.length === 0) {
                            return '<div class="muted" style="padding: 12px; text-align: center;">No members yet</div>';
                        }
                        const maintainerCount = members.filter((m) => authorityRoles.includes(roleLower(m.role))).length;
                        return `
            <div style="max-height: 200px; overflow-y: auto; margin-bottom: 16px;" id="currentMembersListContainer">
              ${members.map((m) => {
                            const role = roleLower(m.role);
                            const canRemove = isMaintainer && (isRemovableRole(role) || (isAuthorityRole(role) && maintainerCount > 1));
                            const removeBtn = canRemove
                                ? `<button type="button" class="btn btn--ghost btn--small" data-member-id="${m.userId}" data-member-name="${escapeHTML(m.name)}" title="Remove from project">Remove</button>`
                                : "";
                            const isSelf = Number(m.userId) === Number(currentUserId);
                            const isLastMaintainer = isAuthorityRole(role) && maintainerCount === 1;
                            const canDemoteSelf = isMaintainer && isSelf ? false : true;
                            const demoteDisabled = isLastMaintainer || !canDemoteSelf;
                            const roleControl = isMaintainer
                                ? `<select class="member-role-select input" data-member-id="${m.userId}" style="min-width: 120px; font-size: 0.875rem;">
                      <option value="viewer" ${role === "viewer" ? "selected" : ""} ${demoteDisabled ? "disabled" : ""}>Viewer</option>
                      <option value="contributor" ${role === "contributor" ? "selected" : ""} ${demoteDisabled ? "disabled" : ""}>Contributor</option>
                      <option value="maintainer" ${role === "maintainer" ? "selected" : ""}>Maintainer</option>
                    </select>`
                                : `<span style="text-transform: capitalize; font-size: 0.875rem; color: var(--text-muted, #6b7280);">${escapeHTML(m.role)}</span>`;
                            return `
                <div style="display: flex; justify-content: space-between; align-items: center; padding: 8px 12px; border-bottom: 1px solid var(--border, #e5e7eb);">
                  <div>
                    <div style="font-weight: 500;">${escapeHTML(m.name || String(m.userId))}</div>
                  </div>
                  <div style="display: flex; align-items: center; gap: 12px;">
                    ${roleControl}
                    ${removeBtn}
                  </div>
                </div>
              `;
                        }).join('')}
            </div>
          `;
                    };
                    dialog.innerHTML = `
          <form method="dialog" class="dialog__form" id="addMemberForm">
            <div class="dialog__header">
              <div class="dialog__title">${isMaintainer ? "Manage Members" : "Members"}</div>
              ${projectName ? `<div class="muted" style="font-size: 0.875rem; margin-top: 4px;">Project: ${escapeHTML(projectName)}</div>` : ""}
              <button class="btn btn--ghost" type="button" id="addMemberDialogClose" aria-label="Close">✕</button>
            </div>

            <div style="margin-bottom: 20px;">
              <div style="font-weight: 500; margin-bottom: 8px;">Current Members</div>
              <div id="currentMembersList">${renderMembersList()}</div>
            </div>

            ${isMaintainer ? (available.length > 0 ? `
              <div style="border-top: 1px solid var(--border, #e5e7eb); padding-top: 20px; margin-top: 20px;">
                <div style="font-weight: 500; margin-bottom: 12px;">Add New Member</div>
                <label class="field">
                  <div class="field__label">User</div>
                  <select id="addMemberUser" class="input" required>
                    <option value="">Select a user...</option>
                    ${available.map((u) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join('')}
                  </select>
                </label>

                <label class="field">
                  <div class="field__label">Role</div>
                  <select id="addMemberRole" class="input" required>
                    <option value="viewer">Viewer</option>
                    <option value="contributor" selected>Contributor</option>
                    <option value="maintainer">Maintainer</option>
                  </select>
                </label>
              </div>
            ` : '<div class="muted" style="padding: 12px; text-align: center; border-top: 1px solid var(--border, #e5e7eb); margin-top: 20px; padding-top: 20px;">All users are already members</div>') : ''}

            <div class="dialog__footer">
              <div class="spacer"></div>
              <button type="button" class="btn btn--ghost" id="addMemberCancel">Close</button>
              ${isMaintainer && available.length > 0 ? `<button type="submit" class="btn" id="addMemberSubmit">Add Member</button>` : ''}
            </div>
          </form>
        `;
                    document.body.appendChild(dialog);
                    dialog.showModal();
                    const closeBtn = document.getElementById("addMemberDialogClose");
                    const cancelBtn = document.getElementById("addMemberCancel");
                    const form = document.getElementById("addMemberForm");
                    const userSelect = document.getElementById("addMemberUser");
                    const roleSelect = document.getElementById("addMemberRole");
                    const currentMembersList = document.getElementById("currentMembersList");
                    // Store references for cleanup
                    let isClosed = false;
                    const handleMembersUpdated = (payload) => {
                        if (payload?.projectId !== projId || isClosed)
                            return;
                        fetchProjectMembers(projId).then((fresh) => {
                            if (isClosed || !Array.isArray(fresh))
                                return;
                            members.length = 0;
                            members.push(...fresh);
                            setBoardMembers(fresh);
                            if (currentMembersList) {
                                currentMembersList.innerHTML = renderMembersList();
                            }
                        }).catch(() => { });
                    };
                    on("members-updated", handleMembersUpdated);
                    const close = () => {
                        if (isClosed)
                            return; // Prevent double-closing
                        isClosed = true;
                        off("members-updated", handleMembersUpdated);
                        // Explicitly close the dialog before removing it to ensure state is updated
                        if (dialog.open) {
                            dialog.close();
                        }
                        // Remove from DOM immediately after closing
                        if (dialog.parentNode) {
                            document.body.removeChild(dialog);
                        }
                    };
                    // Handle dialog's native cancel event (from ESC key)
                    // Let the dialog close naturally via native behavior, then clean up DOM
                    dialog.addEventListener("cancel", () => {
                        // Dialog closes automatically on cancel, just clean up DOM after a brief delay
                        setTimeout(() => {
                            if (dialog.parentNode) {
                                document.body.removeChild(dialog);
                            }
                        }, 0);
                    });
                    if (closeBtn)
                        closeBtn.addEventListener("click", close);
                    if (cancelBtn)
                        cancelBtn.addEventListener("click", close);
                    dialog.addEventListener("click", (e) => {
                        if (e.target === dialog)
                            close();
                    });
                    // Delegated handler for role change (maintainers only)
                    if (currentMembersList) {
                        currentMembersList.addEventListener("change", async (e) => {
                            const select = e.target.closest("select.member-role-select");
                            if (!select)
                                return;
                            const targetUserId = parseInt(select.getAttribute("data-member-id"), 10);
                            const newRole = select.value;
                            const member = members.find((m) => Number(m.userId) === targetUserId);
                            if (!member)
                                return;
                            const memberRole = roleLower(member.role);
                            const previousRole = (memberRole === "maintainer" || memberRole === "owner") ? "maintainer" : (memberRole === "viewer" ? "viewer" : "contributor");
                            if (newRole === previousRole)
                                return; // No-op
                            if (previousRole === "maintainer" && (newRole === "contributor" || newRole === "viewer")) {
                                if (!confirm(`Demote ${escapeHTML(member.name || "this member")} to ${newRole}?`)) {
                                    select.value = previousRole;
                                    return;
                                }
                            }
                            try {
                                recordLocalMutation();
                                const result = await apiFetch(`/api/projects/${projId}/members/${targetUserId}`, {
                                    method: "PATCH",
                                    body: JSON.stringify({ role: newRole }),
                                });
                                if (Array.isArray(result)) {
                                    members.length = 0;
                                    members.push(...result);
                                    setBoardMembers(result);
                                }
                                invalidateMembersCache(projId);
                                if (currentMembersList) {
                                    currentMembersList.innerHTML = renderMembersList();
                                }
                                showToast("Role updated");
                            }
                            catch (err) {
                                select.value = previousRole;
                                showToast(err.message || "Failed to update role");
                            }
                        });
                    }
                    // Delegated handler for Remove from project (survives re-renders of member list)
                    if (currentMembersList) {
                        currentMembersList.addEventListener("click", async (e) => {
                            const removeBtn = e.target.closest("button[data-member-id]");
                            if (!removeBtn)
                                return;
                            const targetUserId = parseInt(removeBtn.getAttribute("data-member-id"), 10);
                            const name = removeBtn.getAttribute("data-member-name") || "this member";
                            if (!confirm(`Remove ${name} from this project?`))
                                return;
                            try {
                                recordLocalMutation();
                                const result = await apiFetch(`/api/projects/${projId}/members/${targetUserId}`, { method: "DELETE" });
                                if (Array.isArray(result)) {
                                    members.length = 0;
                                    members.push(...result);
                                    setBoardMembers(result);
                                }
                                invalidateMembersCache(projId);
                                if (currentMembersList) {
                                    currentMembersList.innerHTML = renderMembersList();
                                }
                                if (targetUserId === currentUserId) {
                                    close();
                                    navigate("/");
                                    return;
                                }
                                // Refetch available users so removed member reappears in Add section (if dropdown exists)
                                try {
                                    const availableUsers = await apiFetch(`/api/projects/${projId}/available-users`);
                                    available.length = 0;
                                    available.push(...(Array.isArray(availableUsers) ? availableUsers : []));
                                    const addMemberSelect = dialog.querySelector("#addMemberUser");
                                    if (addMemberSelect) {
                                        addMemberSelect.innerHTML = `
                    <option value="">Select a user...</option>
                    ${available.map((u) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join("")}
                  `;
                                    }
                                }
                                catch {
                                    // Ignore refetch errors
                                }
                                showToast("Member removed from project");
                            }
                            catch (err) {
                                showToast(err.message || "Failed to remove member");
                            }
                        });
                    }
                    if (form && available.length > 0) {
                        form.addEventListener("submit", async (e) => {
                            e.preventDefault();
                            const userId = parseInt(userSelect.value, 10);
                            const role = roleSelect.value;
                            if (!userId || !role) {
                                close();
                                return;
                            }
                            try {
                                recordLocalMutation();
                                const result = await apiFetch(`/api/projects/${projId}/members`, {
                                    method: "POST",
                                    body: JSON.stringify({ user_id: userId, role }),
                                });
                                // Update members list from API response
                                if (Array.isArray(result)) {
                                    members.length = 0;
                                    members.push(...result);
                                    setBoardMembers(result);
                                }
                                invalidateMembersCache(projId);
                                // Remove added user from available list
                                const addedUserIndex = available.findIndex((u) => u.id === userId);
                                if (addedUserIndex >= 0) {
                                    available.splice(addedUserIndex, 1);
                                }
                                // Update the UI
                                if (currentMembersList) {
                                    currentMembersList.innerHTML = renderMembersList();
                                }
                                // Update the user select dropdown
                                if (userSelect) {
                                    userSelect.innerHTML = `
                  <option value="">Select a user...</option>
                  ${available.map((u) => `<option value="${u.id}">${escapeHTML(u.name)} (${escapeHTML(u.email)})</option>`).join('')}
                `;
                                }
                                // Hide add section if no more users available
                                if (available.length === 0) {
                                    const addSection = form.querySelector('div[style*="border-top"]');
                                    if (addSection) {
                                        addSection.outerHTML = '<div class="muted" style="padding: 12px; text-align: center; border-top: 1px solid var(--border, #e5e7eb); margin-top: 20px; padding-top: 20px;">All users are already members</div>';
                                    }
                                    const submitBtn = document.getElementById("addMemberSubmit");
                                    if (submitBtn) {
                                        submitBtn.remove();
                                    }
                                }
                                showToast("Member added successfully");
                            }
                            catch (err) {
                                showToast(err.message || "Failed to add member");
                            }
                        });
                    }
                }
                catch (err) {
                    showToast(err.message || "Failed to load members");
                }
            });
            btn[BOUND_FLAG] = true;
        }
    };
    setupManageMembersButton(projectId, board.project.name);
    const deleteProjectBtn = document.getElementById("deleteProjectBtn");
    if (deleteProjectBtn && !deleteProjectBtn[BOUND_FLAG]) {
        deleteProjectBtn.addEventListener("click", async () => {
            if (!confirm("Delete this project and all its todos?"))
                return;
            try {
                recordLocalMutation();
                await apiFetch(`/api/projects/${projectId}`, { method: "DELETE" });
                navigate("/");
            }
            catch (err) {
                showToast(err.message);
            }
        });
        deleteProjectBtn[BOUND_FLAG] = true;
    }
    bindBoardFilterUi({
        reloadBoard: loadBoardBySlug,
        showError: (message) => showToast(message),
    });
    const settingsBtn = document.getElementById("settingsBtn");
    if (settingsBtn && !settingsBtn[BOUND_FLAG]) {
        settingsBtn.addEventListener("click", async () => {
            await renderSettingsModal();
            settingsDialog.showModal();
        });
        settingsBtn[BOUND_FLAG] = true;
    }
    const userAvatarBtn = document.getElementById("userAvatarBtn");
    if (userAvatarBtn && !userAvatarBtn[BOUND_FLAG]) {
        userAvatarBtn.addEventListener("click", async () => {
            setSettingsActiveTab("profile");
            await renderSettingsModal();
            settingsDialog.showModal();
        });
        userAvatarBtn[BOUND_FLAG] = true;
    }
    bindMobileTabClickHandlersIfNeeded();
    attachBoardDelegationHandlers();
    initMobileLoadMoreVisibility();
    if (currentUserProjectRole === "maintainer" || isTemporaryBoard(board)) {
        initDnD();
    }
    // Add "No results" state if search is active and no todos match
    if (search && search.trim() !== "") {
        const totalTodos = Object.values(board.columns).reduce((sum, todos) => sum + todos.length, 0);
        if (totalTodos === 0) {
            const boardEl = document.querySelector(".board");
            if (boardEl) {
                boardEl.insertAdjacentHTML("beforeend", buildNoResultsHtml(search));
            }
        }
    }
    // Initialize mobile tabs display
    updateMobileTabs();
}
// Load board by slug
export async function loadBoardBySlug(slug, tag, search, sprintId = null) {
    if (!slug) {
        throw new Error("Slug is required");
    }
    debugLog("loadBoardBySlug start", slug);
    clearPendingRealtimeRefresh();
    const requestSeq = ++boardLoadSequence;
    const requestSlug = slug;
    const requestTag = tag || "";
    const requestSearch = search || "";
    const requestSprintId = sprintId ?? null;
    // Clear stale members from prior board immediately; prevents stale data if fetch fails early.
    setBoardMembers([]);
    resetBoardFilterUiState();
    lastUpdateBoardContentBoard = null;
    const params = new URLSearchParams();
    params.set("limitPerLane", String(Math.max(getRequestedBoardLimitPerLane(requestTag, requestSearch, requestSprintId), getBoardLimitPerLaneFloor())));
    if (tag)
        params.set("tag", tag);
    if (search)
        params.set("search", search);
    if (requestSprintId)
        params.set("sprintId", requestSprintId);
    const qs = params.toString() ? `?${params.toString()}` : "";
    let board;
    try {
        board = await apiFetch(`/api/board/${slug}${qs}`);
    }
    catch (err) {
        if (err?.status === 400 && requestSprintId) {
            const url = new URL(window.location.href);
            url.searchParams.delete("sprintId");
            const newUrl = url.pathname + (url.search ? url.search : "");
            history.replaceState({}, "", newUrl);
            await loadBoardBySlug(slug, tag, search, null);
            return;
        }
        throw err;
    }
    if (requestSeq !== boardLoadSequence)
        return;
    const currentUrl = new URL(window.location.href);
    const currentPath = currentUrl.pathname.match(/^\/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)(?:\/t\/\d+)?\/?$/);
    const currentSlug = currentPath ? currentPath[1] : "";
    const currentTag = currentUrl.searchParams.get("tag") || "";
    const currentSearch = currentUrl.searchParams.get("search") || "";
    const currentSprintId = currentUrl.searchParams.get("sprintId") || null;
    if (currentSlug !== requestSlug || currentTag !== requestTag || currentSearch !== requestSearch || (currentSprintId || null) !== (requestSprintId || null))
        return;
    resetBoardLimitPerLaneFloor();
    // Defer sprints — render board first, then load in background
    clearSprintChipDataIfSlugChanged(slug);
    const effectiveSprintId = requestSprintId;
    const rendered = await bootstrapLoadedBoardView({
        board,
        slug,
        tag,
        search,
        isCurrent: () => requestSeq === boardLoadSequence && getSlug() === requestSlug,
        setResolvedRole: (role) => {
            currentUserProjectRole = role;
        },
        markMembersFetched: (projectId) => {
            lastFetchedProjectId = projectId;
        },
        renderLoadedBoard: (renderOpts) => {
            renderBoardFromData(board, renderOpts.projectId, tag || "", search || "", effectiveSprintId, renderOpts);
        },
        markLoadSuccess: (loadedSlug) => {
            markBoardLoadSucceeded(loadedSlug);
        },
    });
    if (!rendered)
        return;
    debugLog("loadBoardBySlug end (success)", slug);
    // Note: Avatars are already rendered in renderTodoCard() since members were fetched before rendering.
    // No need to call hydrateAvatarsOnCards() here.
    if (!isAnonymousBoard(board) && !hasSprintChipDataForSlug(slug)) {
        setSprintChipDataForSlug(slug, null);
        apiFetch(`/api/board/${slug}/sprints`)
            .then((sprintsResp) => {
            if (requestSeq !== boardLoadSequence)
                return;
            const sprints = normalizeSprints(sprintsResp);
            setSprintChipDataForSlug(slug, sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null);
            if (getSlug() === requestSlug) {
                updateChipsOnly(requestSprintId);
            }
        })
            .catch(() => {
            if (requestSeq !== boardLoadSequence)
                return;
            clearSprintChipData();
        });
    }
}
// Register board refresher with orchestration layer
registerBoardRefresher(async (slug, tag, search, sprintId) => {
    await loadBoardBySlug(slug, tag || null, search || null, sprintId ?? null);
});
// Register sprints-only refresher (chips update without full board reload)
registerSprintsRefresher(async (slug) => {
    if (getSlug() !== slug)
        return;
    try {
        const sprintsResp = await apiFetch(`/api/board/${slug}/sprints`);
        const sprints = normalizeSprints(sprintsResp);
        setSprintChipDataForSlug(slug, sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null);
        if (getSlug() === slug)
            updateChipsOnly(getSprintIdFromUrl());
    }
    catch {
        clearSprintChipData();
    }
});
ensureSprintSubscription();
function clearPendingHighlight() {
    if (highlightRafId !== null) {
        cancelAnimationFrame(highlightRafId);
        highlightRafId = null;
    }
    if (highlightTimeoutId !== null) {
        clearTimeout(highlightTimeoutId);
        highlightTimeoutId = null;
    }
}
export function onTodoDialogClosed() {
    clearResolverRequest();
    clearPendingHighlight();
    setOpenTodoSegment(null);
}
async function resolveTodoByLocalId(slug, localId) {
    clearResolverRequest();
    const controller = new AbortController();
    resolverController = controller;
    try {
        return await apiFetch(`/api/board/${slug}/todos/${localId}`, { signal: controller.signal });
    }
    finally {
        if (resolverController === controller) {
            resolverController = null;
        }
    }
}
async function openTodoFromPath(slug, openTodoSegment) {
    const localId = parseInt(openTodoSegment, 10);
    if (Number.isNaN(localId))
        return;
    if (isSameEditingTodo(localId))
        return;
    const todoFromBoard = findTodoInBoardByLocalId(localId);
    if (todoFromBoard) {
        await runWhileTodoDialogOpening(() => openTodoDialog({ mode: "edit", todo: todoFromBoard, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole }));
        setOpenTodoSegment(String(localId));
        scheduleCardHighlight(todoFromBoard);
        return;
    }
    try {
        const resolved = await resolveTodoByLocalId(slug, localId);
        if (isSameEditingTodo(localId))
            return;
        await runWhileTodoDialogOpening(() => openTodoDialog({ mode: "edit", todo: resolved, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole }));
        setOpenTodoSegment(String(localId));
    }
    catch (err) {
        if (err?.name === "AbortError")
            return;
        if (err?.status === 404) {
            showToast("Todo not found");
        }
        else if (err?.status === 403) {
            showToast("You don't have access to this todo");
        }
        else {
            showToast("Failed to load todo");
        }
        replaceBoardPath(slug);
        setOpenTodoSegment(null);
    }
}
// Main render function for board view
export async function renderBoard(slug, tag, search, sprintId, openTodoId = null, openTodoSegment = null, opts = {}) {
    if (!slug)
        throw new Error("Slug is required");
    debugLog("renderBoard start", slug);
    if (opts.skipLoad) {
        if (!getBoard() || getSlug() !== slug) {
            opts.skipLoad = false;
        }
    }
    if (opts.prefetchedBoard && opts.prefetchedBoard.project?.id) {
        const board = opts.prefetchedBoard;
        setBoardMembers([]);
        resetBoardFilterUiState();
        clearSprintChipDataIfSlugChanged(slug);
        const rendered = await bootstrapLoadedBoardView({
            board,
            slug,
            tag,
            search,
            isCurrent: () => getSlug() === slug,
            setResolvedRole: (role) => {
                currentUserProjectRole = role;
            },
            markMembersFetched: (projectId) => {
                lastFetchedProjectId = projectId;
            },
            renderLoadedBoard: (renderOpts) => {
                renderBoardFromData(board, renderOpts.projectId, tag || "", search || "", sprintId, renderOpts);
            },
            markLoadSuccess: (loadedSlug) => {
                markBoardLoadSucceeded(loadedSlug);
            },
        });
        if (!rendered)
            return;
        if (getSlug() === slug)
            connectBoardEvents(slug);
        // Note: Avatars are already rendered in renderTodoCard() since members were fetched before rendering.
        // No need to call hydrateAvatarsOnCards() here.
        if (!isAnonymousBoard(board) && !hasSprintChipDataForSlug(slug)) {
            setSprintChipDataForSlug(slug, null);
            apiFetch(`/api/board/${slug}/sprints`)
                .then((sprintsResp) => {
                if (getSlug() !== slug)
                    return;
                const sprints = normalizeSprints(sprintsResp);
                setSprintChipDataForSlug(slug, sprints.length > 0 ? { ...(sprintsResp || {}), sprints } : null);
                if (getSlug() === slug) {
                    updateChipsOnly(sprintId);
                }
            })
                .catch(() => {
                if (getSlug() === slug) {
                    clearSprintChipData();
                }
            });
        }
    }
    else if (!opts.skipLoad) {
        setInitialBoardLoadInFlight(slug);
        try {
            await loadBoardBySlug(slug, tag, search || null, sprintId);
        }
        finally {
            setInitialBoardLoadInFlight(null);
            if (getSlug() === slug)
                connectBoardEvents(slug);
        }
    }
    if (openTodoSegment) {
        await openTodoFromPath(slug, openTodoSegment);
        return;
    }
    setOpenTodoSegment(null);
    clearResolverRequest();
    if (getEditingTodo()) {
        const dialog = document.getElementById("todoDialog");
        if (dialog?.open) {
            dialog.close();
        }
    }
    if (openTodoId) {
        const todoId = parseInt(openTodoId, 10);
        if (!Number.isNaN(todoId)) {
            const board = getBoard();
            let opened = false;
            if (board?.columns) {
                const statuses = Object.keys(board.columns);
                let todo;
                for (const st of statuses) {
                    const list = board.columns[st] || [];
                    todo = list.find((t) => t.id === todoId);
                    if (todo)
                        break;
                }
                if (todo) {
                    openTodoDialog({ mode: "edit", todo, onNavigateToLinkedTodo: navigate, role: currentUserProjectRole });
                    opened = true;
                }
            }
            // Only clean URL when we actually opened the modal (so refresh doesn't re-open)
            if (opened) {
                const url = new URL(window.location.href);
                url.searchParams.delete("openTodoId");
                const newUrl = url.pathname + (url.search ? url.search : "");
                history.replaceState({}, "", newUrl);
            }
        }
    }
}
