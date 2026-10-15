/**
 * Public read-only board view.
 *
 * Entered only after the member/temporary board route returned an
 * access-denial or not-found result and GET /api/public/board/{slug} succeeds.
 * This module is a separate boundary from views/board.ts: it never imports the
 * member editor, drag/drop, context menu, selection, bulk edit, settings,
 * Wall, Agenda, VoiceFlow, or member/realtime-bus code, and it calls only the
 * public API and the public event stream.
 */
import { app } from '../dom/elements.js';
import { t } from '../i18n/index.js';
import { showToast } from '../utils.js';
import { getAuthStatusAvailable, getMarkdownNotesEnabled, getMermaidNotesEnabled, getSlug, getUser } from '../state/selectors.js';
import { signInPath } from '../app-home.js';
import { setBoard, setBoardAccess, setProjectId } from '../state/mutations.js';
import { PUBLIC_BOARD_ACCESS } from '../state/board-access.js';
import { fetchPublicBoardSnapshot, fetchPublicLanePage, fetchPublicSprints, isPublicAbort, isPublicNotFound, normalizePublicQuery, publicBoardEventsPath, PublicBoardApiError, PUBLIC_DEFAULT_PAGE_SIZE, PUBLIC_MAX_PAGE_SIZE, PUBLIC_MAX_TAG_FILTERS, samePublicQuery, EMPTY_PUBLIC_QUERY, } from '../public-board-api.js';
import { buildPublicBoardHtml, buildPublicColumnsHtml, buildPublicFilterErrorHtml, buildPublicFilterSectionsHtml, buildPublicMobileTabsHtml, buildPublicUnavailableHtml, isPublicPanelFilterActive, isPublicQueryActive, publicPriorityMap, PUBLIC_FILTER_PANEL_ID, PUBLIC_FILTER_TOGGLE_ID, PUBLIC_CLEAR_SPRINT_ATTR, PUBLIC_PRIORITY_OPTION_ATTR, showPublicPoints, } from './public-board-rendering.js';
import { closePublicTodo, getOpenPublicTodoLocalId, openPublicTodo } from '../dialogs/public-todo.js';
import { SseConnectionManager } from '../core/sse-client.js';
import { closeFilterPopover, openFilterPopover, positionFilterPopover } from './filter-popover.js';
import { bindPublicOmni } from './public-omni.js';
/**
 * Public stream policy. The server sends comment-only heartbeats (~15s) that
 * never reach onmessage, so the data-ping watchdog is disabled. Reconnects use
 * jittered exponential backoff from 5s, and the backoff resets only after 30s
 * of stable connection, keeping attempts well under 20/minute/IP.
 */
export const PUBLIC_STREAM_POLICY = Object.freeze({
    staleAfterMs: null,
    initialBackoffMs: 5000,
    maxBackoffMs: 60000,
    jitterRatio: 0.3,
    stableAfterMs: 30000,
});
const REFRESH_DEBOUNCE_MS = 400;
const SEARCH_DEBOUNCE_MS = 350;
const MOBILE_BREAKPOINT_PX = 620;
let session = null;
let resolutionSeq = 0;
let resolutionController = null;
// ---- URL <-> query ----
export function readPublicQueryFromUrl() {
    const url = new URL(window.location.href);
    const sprintRaw = url.searchParams.get('sprintNumber');
    const sprint = sprintRaw && /^\d{1,9}$/.test(sprintRaw) ? Number(sprintRaw) : null;
    return normalizePublicQuery({
        search: url.searchParams.get('search') ?? '',
        tags: url.searchParams.getAll('tag'),
        sprintNumber: sprint,
        priority: url.searchParams.get('priority'),
    });
}
function writePublicQueryToUrl(query) {
    const url = new URL(window.location.href);
    for (const key of ['search', 'tag', 'sprintNumber', 'priority'])
        url.searchParams.delete(key);
    if (query.search)
        url.searchParams.set('search', query.search);
    for (const tag of query.tags)
        url.searchParams.append('tag', tag);
    if (query.sprintNumber != null)
        url.searchParams.set('sprintNumber', String(query.sprintNumber));
    if (query.priority)
        url.searchParams.set('priority', query.priority);
    history.replaceState(history.state ?? {}, '', url.pathname + url.search + url.hash);
}
function boardPathWithQuery(slug) {
    return `/${slug}${window.location.search}`;
}
function storyPathWithQuery(slug, localId) {
    return `/${slug}/t/${localId}${window.location.search}`;
}
// ---- Session state ----
function lanesFromSnapshot(snapshot) {
    const lanes = {};
    for (const column of snapshot.workflow) {
        const meta = snapshot.columnsMeta[column.key];
        lanes[column.key] = { ...meta, loading: false, error: false };
    }
    return lanes;
}
function columnsFromSnapshot(snapshot) {
    const columns = {};
    for (const column of snapshot.workflow)
        columns[column.key] = [...(snapshot.columns[column.key] ?? [])];
    return columns;
}
function applySnapshot(s, snapshot) {
    s.snapshot = snapshot;
    s.columns = columnsFromSnapshot(snapshot);
    s.lanes = lanesFromSnapshot(snapshot);
    if (!snapshot.workflow.some((column) => column.key === s.activeMobileTab)) {
        s.activeMobileTab = snapshot.workflow[0]?.key ?? '';
    }
}
function viewModel(s) {
    return {
        snapshot: s.snapshot,
        columns: s.columns,
        lanes: s.lanes,
        query: s.query,
        sprints: s.sprints,
        activeMobileTab: s.activeMobileTab,
        isMobile: typeof window !== 'undefined' && window.innerWidth <= MOBILE_BREAKPOINT_PX,
        user: getUser(),
        canSignIn: !getUser() && getAuthStatusAvailable(),
    };
}
function isCurrent(s) {
    return session === s;
}
function teardown(s) {
    s.stream?.stop();
    s.stream = null;
    if (s.refreshTimer !== null)
        clearTimeout(s.refreshTimer);
    s.refreshTimer = null;
    if (s.searchTimer !== null)
        clearTimeout(s.searchTimer);
    s.searchTimer = null;
    s.snapshotController?.abort();
    s.snapshotController = null;
    for (const controller of s.laneControllers.values())
        controller.abort();
    s.laneControllers.clear();
    s.sprintsSeq++;
    s.sprintsController?.abort();
    s.sprintsController = null;
    s.refreshQueued = false;
    s.listeners.abort();
    s.pageScope.abort();
    s.omni = null;
}
/** Ends any public board view and its stream. Safe to call repeatedly. */
export function stopPublicBoard() {
    resolutionSeq++;
    resolutionController?.abort();
    resolutionController = null;
    const s = session;
    session = null;
    if (s) {
        teardown(s);
        closePublicTodo();
        setBoardAccess(null);
    }
}
export function isPublicBoardSessionFor(slug) {
    return !!slug && !!session && session.slug === slug;
}
// ---- Rendering ----
function captureListScroll(root) {
    const out = new Map();
    root.querySelectorAll('[data-public-list]').forEach((el) => {
        out.set(el.getAttribute('data-public-list') ?? '', el.scrollTop);
    });
    return out;
}
function restoreListScroll(root, scroll) {
    root.querySelectorAll('[data-public-list]').forEach((el) => {
        const top = scroll.get(el.getAttribute('data-public-list') ?? '');
        if (top)
            el.scrollTop = top;
    });
}
/** Updates only the regions whose markup changed, preserving focus and lane scroll. */
function renderBoardRegions(s) {
    if (!isCurrent(s))
        return;
    const view = viewModel(s);
    const boardEl = app.querySelector('[data-public-board]');
    const tabsEl = app.querySelector('#publicMobileTabs');
    if (!boardEl || !tabsEl || !s.omni) {
        renderFull(s);
        return;
    }
    const columns = boardRegionHtml(s, view);
    if (columns !== s.rendered.columns) {
        const scroll = captureListScroll(boardEl);
        boardEl.innerHTML = columns;
        restoreListScroll(boardEl, scroll);
        s.rendered.columns = columns;
    }
    const tabs = buildPublicMobileTabsHtml(view);
    if (tabs !== s.rendered.tabs) {
        tabsEl.innerHTML = tabs;
        s.rendered.tabs = tabs;
    }
    s.omni.render();
    syncFilterPanel(s);
    syncNoResults(s);
}
// ---- Omni tags ----
function searchInputEl() {
    return app.querySelector('#publicSearchInput');
}
/** Label for an active sprint-number deep link: the sprint name once known, else its number. */
function sprintFilterLabel(s) {
    const n = s.query.sprintNumber;
    if (n == null)
        return null;
    return s.sprints.find((sprint) => sprint.number === n)?.name ?? `#${n}`;
}
function omniState(s) {
    return {
        tags: s.snapshot.tags,
        selected: s.query.tags,
        input: searchInputEl()?.value ?? s.query.search,
        sprintLabel: sprintFilterLabel(s),
    };
}
function cancelPendingSearch(s) {
    if (s.searchTimer !== null)
        clearTimeout(s.searchTimer);
    s.searchTimer = null;
}
/**
 * Pins a suggested tag: only a published tag (canonical name), not already
 * selected, within the public tag limit. Consumes the search term and commits
 * search and tags together, so exactly one snapshot reload runs even when a
 * debounced search was still pending.
 */
function addTag(s, raw) {
    if (!isCurrent(s))
        return;
    const key = raw.toLocaleLowerCase();
    const tag = s.snapshot.tags.find((candidate) => candidate.name.toLocaleLowerCase() === key);
    if (!tag || s.query.tags.length >= PUBLIC_MAX_TAG_FILTERS)
        return;
    if (s.query.tags.some((existing) => existing.toLocaleLowerCase() === key))
        return;
    cancelPendingSearch(s);
    const input = searchInputEl();
    if (input)
        input.value = '';
    app.querySelector('#publicSearchClear')?.setAttribute('hidden', '');
    setQuery(s, { search: '', tags: [...s.query.tags, tag.name] });
    input?.focus();
}
/**
 * Unpins one selected tag, keeping every other filter. Any typed-but-pending
 * search is committed in the same query change (one reload, not two).
 */
function removeTag(s, raw) {
    if (!isCurrent(s))
        return;
    const key = raw.toLocaleLowerCase();
    if (!s.query.tags.some((existing) => existing.toLocaleLowerCase() === key))
        return;
    const input = searchInputEl();
    const pending = s.searchTimer !== null;
    cancelPendingSearch(s);
    setQuery(s, {
        ...(pending && input ? { search: input.value } : {}),
        tags: s.query.tags.filter((existing) => existing.toLocaleLowerCase() !== key),
    });
    input?.focus();
}
// ---- Filter popover (sprint / priority) ----
function filterPanelElements() {
    const toggle = app.querySelector(`#${PUBLIC_FILTER_TOGGLE_ID}`);
    const panel = app.querySelector(`#${PUBLIC_FILTER_PANEL_ID}`);
    return toggle && panel ? { toggle, panel } : null;
}
function focusedPanelOption(panel) {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement) || !panel.contains(active))
        return null;
    const attr = PUBLIC_PRIORITY_OPTION_ATTR;
    return active.hasAttribute(attr) ? { attr, value: active.getAttribute(attr) ?? '' } : null;
}
/**
 * Brings the popover's sections, selection state, and toggle in line with the
 * session (query, priorities) without touching the search
 * input or the rest of the topbar. An open popover stays open; a focused
 * option keeps focus while it still exists.
 */
function syncFilterPanel(s) {
    if (!isCurrent(s))
        return;
    const elements = filterPanelElements();
    if (!elements)
        return;
    const { toggle, panel } = elements;
    const sections = buildPublicFilterSectionsHtml(s);
    const hadFocus = panel.contains(document.activeElement) || toggle === document.activeElement;
    if (sections !== s.rendered.panel) {
        const focused = focusedPanelOption(panel);
        panel.innerHTML = sections;
        s.rendered.panel = sections;
        if (focused) {
            const same = Array.from(panel.querySelectorAll(`[${focused.attr}]`))
                .find((el) => el.getAttribute(focused.attr) === focused.value);
            (same ?? toggle).focus();
        }
    }
    const empty = sections === '';
    if (empty && !panel.hidden)
        closeFilterPopover(panel, toggle);
    toggle.hidden = empty;
    toggle.classList.toggle('search-filter-toggle--active', isPublicPanelFilterActive(s.query));
    if (empty && hadFocus)
        app.querySelector('#publicSearchInput')?.focus();
    if (!panel.hidden)
        positionFilterPopover(panel, toggle);
}
/** Keeps the search input and its clear button in line with s.query after URL navigation. */
function syncSearchInput(s) {
    const input = app.querySelector('#publicSearchInput');
    if (!input)
        return;
    if (normalizePublicQuery({ search: input.value }).search !== s.query.search)
        input.value = s.query.search;
    app.querySelector('#publicSearchClear')?.toggleAttribute('hidden', input.value.trim() === '');
}
/** Only values the panel offers are accepted: All or a published priority key. */
function parsePanelPriority(s, raw) {
    if (raw === '')
        return null;
    return s.snapshot.priorities.some((priority) => priority.key === raw) ? raw : undefined;
}
/** Handles clicks on the toggle and on public options; returns true when consumed. */
function handleFilterPanelClick(s, target) {
    const elements = filterPanelElements();
    if (!elements)
        return false;
    const { toggle, panel } = elements;
    if (toggle.contains(target)) {
        if (panel.hidden)
            openFilterPopover(panel, toggle);
        else
            closeFilterPopover(panel, toggle);
        return true;
    }
    if (!panel.contains(target))
        return false;
    const priorityEl = target.closest(`[${PUBLIC_PRIORITY_OPTION_ATTR}]`);
    const priority = priorityEl ? parsePanelPriority(s, priorityEl.getAttribute(PUBLIC_PRIORITY_OPTION_ATTR) ?? '') : undefined;
    if (priority === undefined)
        return true;
    closeFilterPopover(panel, toggle);
    toggle.focus();
    setQuery(s, { priority });
    return true;
}
/** Outside click, Escape, and resize for the popover, scoped to this session. */
function bindSessionListeners(s) {
    const { signal } = s.listeners;
    document.addEventListener('click', (event) => {
        if (!isCurrent(s))
            return;
        const elements = filterPanelElements();
        if (!elements || elements.panel.hidden)
            return;
        const target = event.target;
        if (target instanceof Node && (elements.panel.contains(target) || elements.toggle.contains(target)))
            return;
        closeFilterPopover(elements.panel, elements.toggle);
    }, { signal });
    document.addEventListener('keydown', (event) => {
        if (!isCurrent(s) || event.key !== 'Escape')
            return;
        const elements = filterPanelElements();
        if (!elements || elements.panel.hidden)
            return;
        event.preventDefault();
        closeFilterPopover(elements.panel, elements.toggle);
        elements.toggle.focus();
    }, { signal });
    window.addEventListener('resize', () => {
        if (!isCurrent(s))
            return;
        const elements = filterPanelElements();
        if (elements && !elements.panel.hidden)
            positionFilterPopover(elements.panel, elements.toggle);
    }, { signal });
}
function syncNoResults(s) {
    const container = app.querySelector('[data-public-board-page] .container');
    if (!container)
        return;
    const existing = container.querySelector('.public-board__no-results');
    const total = s.snapshot.workflow.reduce((sum, column) => sum + (s.lanes[column.key]?.totalCount ?? 0), 0);
    const show = !s.filterError && total === 0 && isPublicQueryActive(s.query);
    if (show && !existing) {
        const el = document.createElement('div');
        el.className = 'empty public-board__no-results';
        el.setAttribute('role', 'status');
        const title = document.createElement('div');
        title.className = 'empty__title';
        title.setAttribute('data-i18n-text', 'publicBoard.empty');
        title.textContent = t('publicBoard.empty');
        el.append(title);
        container.querySelector('.mobile-board-wrapper')?.before(el);
    }
    else if (!show && existing) {
        existing.remove();
    }
}
function boardRegionHtml(s, view) {
    return s.filterError ? buildPublicFilterErrorHtml() : buildPublicColumnsHtml(view);
}
function renderFull(s) {
    if (!isCurrent(s))
        return;
    const view = viewModel(s);
    app.innerHTML = buildPublicBoardHtml(view);
    const columns = boardRegionHtml(s, view);
    if (s.filterError) {
        const boardEl = app.querySelector('[data-public-board]');
        if (boardEl)
            boardEl.innerHTML = columns;
        syncNoResults(s);
    }
    s.rendered = {
        columns,
        tabs: buildPublicMobileTabsHtml(view),
        panel: buildPublicFilterSectionsHtml(view),
    };
    bindPageHandlers(s);
    // Page-scoped Omni controller: its listeners and observer end with this page.
    s.pageScope.abort();
    s.pageScope = new AbortController();
    const omniRoot = app.querySelector('[data-public-omni]');
    s.omni = omniRoot
        ? bindPublicOmni({
            root: omniRoot,
            signal: s.pageScope.signal,
            getState: () => omniState(s),
            onAdd: (tag) => addTag(s, tag),
            onRemove: (tag) => removeTag(s, tag),
        })
        : null;
}
function bindPageHandlers(s) {
    const page = app.querySelector('[data-public-board-page]');
    if (!page)
        return;
    // All handlers are bound on this freshly created page element; they are read-only.
    page.addEventListener('click', (event) => {
        if (!isCurrent(s))
            return;
        const target = event.target;
        if (handleFilterPanelClick(s, target))
            return;
        if (target.closest(`[${PUBLIC_CLEAR_SPRINT_ATTR}]`)) {
            setQuery(s, { sprintNumber: null });
            searchInputEl()?.focus();
            return;
        }
        const card = target.closest('[data-public-local-id]');
        if (card) {
            const localId = Number(card.getAttribute('data-public-local-id'));
            if (Number.isSafeInteger(localId) && localId > 0)
                openStoryFromBoard(s, localId);
            return;
        }
        if (target.closest('[data-public-retry]')) {
            void reloadSnapshot(s, 'filter');
            return;
        }
        const more = target.closest('[data-public-load-more]');
        if (more) {
            void loadMore(s, more.getAttribute('data-public-load-more') ?? '');
            return;
        }
        const tab = target.closest('[data-public-tab]');
        if (tab) {
            s.activeMobileTab = tab.getAttribute('data-public-tab') ?? s.activeMobileTab;
            renderBoardRegions(s);
            return;
        }
        if (target.closest('#publicSearchClear')) {
            const input = page.querySelector('#publicSearchInput');
            if (input)
                input.value = '';
            page.querySelector('#publicSearchClear')?.setAttribute('hidden', '');
            cancelPendingSearch(s);
            setQuery(s, { ...s.query, search: '' });
            // The committed query may be unchanged (search still debouncing), so refresh suggestions directly.
            s.omni?.render();
            input?.focus();
            return;
        }
        if (target.closest('#publicSignInBtn')) {
            startSignIn();
        }
    });
    page.addEventListener('input', (event) => {
        if (!isCurrent(s))
            return;
        const target = event.target;
        if (target.id !== 'publicSearchInput')
            return;
        page.querySelector('#publicSearchClear')?.toggleAttribute('hidden', target.value.trim() === '');
        // Suggestions follow the live input immediately; the query commits after the debounce.
        s.omni?.render();
        if (s.searchTimer !== null)
            clearTimeout(s.searchTimer);
        s.searchTimer = setTimeout(() => {
            s.searchTimer = null;
            if (!isCurrent(s))
                return;
            setQuery(s, { ...s.query, search: target.value });
        }, SEARCH_DEBOUNCE_MS);
    });
}
/**
 * Existing sign-in flow; afterwards ordinary member-first resolution runs again
 * for the same path, filters, and story deep link.
 */
function startSignIn() {
    window.location.assign(signInPath(window.location.pathname + window.location.search));
}
// ---- Filters, refresh, pagination ----
function setQuery(s, next) {
    if (!isCurrent(s))
        return;
    const query = normalizePublicQuery({ ...s.query, ...next });
    if (samePublicQuery(query, s.query))
        return;
    s.query = query;
    writePublicQueryToUrl(query);
    s.omni?.render();
    syncFilterPanel(s);
    void reloadSnapshot(s, 'filter');
}
function loadedPageSize(s) {
    let max = PUBLIC_DEFAULT_PAGE_SIZE;
    for (const key of Object.keys(s.columns))
        max = Math.max(max, s.columns[key].length);
    return Math.min(PUBLIC_MAX_PAGE_SIZE, max);
}
/**
 * Replaces the board data with a fresh snapshot for the current filters.
 * Every replacement increments dataEpoch, aborting in-flight snapshot and lane
 * requests, so pages from an older query or project are never merged.
 */
async function reloadSnapshot(s, kind) {
    if (!isCurrent(s))
        return;
    const epoch = ++s.dataEpoch;
    s.snapshotController?.abort();
    for (const controller of s.laneControllers.values())
        controller.abort();
    s.laneControllers.clear();
    const controller = new AbortController();
    s.snapshotController = controller;
    const query = s.query;
    try {
        const snapshot = await fetchPublicBoardSnapshot(s.slug, query, {
            signal: controller.signal,
            limitPerLane: kind === 'refresh' ? loadedPageSize(s) : PUBLIC_DEFAULT_PAGE_SIZE,
        });
        if (!isCurrent(s) || epoch !== s.dataEpoch)
            return;
        s.filterError = false;
        applySnapshot(s, snapshot);
        renderBoardRegions(s);
        syncSprintVocabulary(s, kind);
    }
    catch (err) {
        if (!isCurrent(s) || epoch !== s.dataEpoch || isPublicAbort(err))
            return;
        if (isPublicNotFound(err)) {
            enterUnavailable(s);
            return;
        }
        if (err instanceof PublicBoardApiError && err.kind === 'invalid_request' && isPublicQueryActive(query)) {
            // A filter the board no longer supports: fall back to the unfiltered view.
            s.query = EMPTY_PUBLIC_QUERY;
            writePublicQueryToUrl(s.query);
            syncSearchInput(s);
            renderBoardRegions(s);
            void reloadSnapshot(s, 'filter');
            return;
        }
        if (kind === 'filter') {
            // The displayed cards belong to the previous query; never present them
            // as matching the newly selected filters. Show an error with Retry.
            s.filterError = true;
            renderBoardRegions(s);
        }
        // A failed realtime refresh keeps the current content: it still matches
        // the current filters, and realtime or the next action retries.
    }
    finally {
        if (s.snapshotController === controller)
            s.snapshotController = null;
    }
}
/** Coalesced realtime/reconnect refresh: at most one in flight plus one queued. */
function requestRefresh(s) {
    if (!isCurrent(s) || s.refreshTimer !== null)
        return;
    s.refreshTimer = setTimeout(() => {
        s.refreshTimer = null;
        void runRefresh(s);
    }, REFRESH_DEBOUNCE_MS);
}
async function runRefresh(s) {
    if (!isCurrent(s))
        return;
    if (s.refreshInFlight) {
        s.refreshQueued = true;
        return;
    }
    s.refreshInFlight = true;
    try {
        await reloadSnapshot(s, 'refresh');
    }
    finally {
        s.refreshInFlight = false;
    }
    if (s.refreshQueued && isCurrent(s)) {
        s.refreshQueued = false;
        requestRefresh(s);
    }
}
async function loadMore(s, key) {
    if (!isCurrent(s))
        return;
    const lane = s.lanes[key];
    if (!lane || lane.loading || !lane.nextCursor)
        return;
    const epoch = s.dataEpoch;
    const cursor = lane.nextCursor;
    const query = s.query;
    s.lanes[key] = { ...lane, loading: true, error: false };
    renderBoardRegions(s);
    const controller = new AbortController();
    s.laneControllers.get(key)?.abort();
    s.laneControllers.set(key, controller);
    try {
        const page = await fetchPublicLanePage(s.slug, key, query, cursor, { signal: controller.signal, limit: PUBLIC_DEFAULT_PAGE_SIZE });
        if (!isCurrent(s) || epoch !== s.dataEpoch)
            return;
        const seen = new Set((s.columns[key] ?? []).map((todo) => todo.localId));
        const additions = page.items.filter((todo) => !seen.has(todo.localId));
        s.columns[key] = [...(s.columns[key] ?? []), ...additions];
        s.lanes[key] = { hasMore: page.hasMore, nextCursor: page.nextCursor, totalCount: page.totalCount, loading: false, error: false };
        renderBoardRegions(s);
    }
    catch (err) {
        if (!isCurrent(s) || epoch !== s.dataEpoch || isPublicAbort(err))
            return;
        if (isPublicNotFound(err)) {
            enterUnavailable(s);
            return;
        }
        s.lanes[key] = { ...lane, loading: false, error: true };
        renderBoardRegions(s);
    }
    finally {
        if (s.laneControllers.get(key) === controller)
            s.laneControllers.delete(key);
    }
}
/**
 * Keeps the public sprint vocabulary consistent with the latest snapshot.
 * The public stream carries no reason, so every successful realtime refresh
 * may reflect a sprint create/rename/delete; refreshes are already coalesced,
 * so this adds at most one /sprints request per refresh. Filter reloads do not
 * change the vocabulary and only fetch after sprints become enabled.
 */
function syncSprintVocabulary(s, kind) {
    if (!isCurrent(s))
        return;
    if (!s.snapshot.project.sprintsEnabled) {
        s.sprintsSeq++;
        s.sprintsController?.abort();
        s.sprintsController = null;
        s.sprintsLoaded = false;
        if (s.sprints.length > 0) {
            s.sprints = [];
            renderBoardRegions(s);
        }
        if (s.query.sprintNumber != null)
            clearSprintFilter(s);
        return;
    }
    if (kind === 'filter' && s.sprintsLoaded)
        return;
    void loadSprints(s);
}
async function loadSprints(s) {
    const seq = ++s.sprintsSeq;
    s.sprintsController?.abort();
    const controller = new AbortController();
    s.sprintsController = controller;
    try {
        const sprints = await fetchPublicSprints(s.slug, { signal: controller.signal });
        if (!isCurrent(s) || seq !== s.sprintsSeq || !s.snapshot.project.sprintsEnabled)
            return;
        s.sprints = sprints;
        s.sprintsLoaded = true;
        const active = s.query.sprintNumber;
        if (active != null && !sprints.some((sprint) => sprint.number === active)) {
            clearSprintFilter(s);
            return;
        }
        renderBoardRegions(s);
    }
    catch (err) {
        if (!isCurrent(s) || seq !== s.sprintsSeq || isPublicAbort(err))
            return;
        if (isPublicNotFound(err)) {
            enterUnavailable(s);
            return;
        }
        // Transient: keep the previous vocabulary; the next refresh retries.
    }
    finally {
        if (s.sprintsController === controller)
            s.sprintsController = null;
    }
}
/**
 * The active sprint no longer exists (or sprints were disabled). The server
 * would keep filtering by that number while the control can no longer show
 * it, so drop only the sprint filter, tell the visitor, and reload.
 */
function clearSprintFilter(s) {
    if (!isCurrent(s) || s.query.sprintNumber == null)
        return;
    showToast(t('publicBoard.sprintFilterCleared'));
    setQuery(s, { sprintNumber: null });
    renderBoardRegions(s);
}
// ---- Story detail ----
function dialogContext(s) {
    return {
        slug: s.slug,
        priorities: publicPriorityMap(s.snapshot.priorities),
        sprints: s.sprints,
        showPoints: showPublicPoints(s.snapshot),
        markdownEnabled: getMarkdownNotesEnabled(),
        mermaidEnabled: getMermaidNotesEnabled(),
        onNavigateToStory: (localId) => openStoryFromBoard(s, localId),
        onSignIn: !getUser() && getAuthStatusAvailable() ? startSignIn : undefined,
        onClosedByUser: () => {
            if (!isCurrent(s))
                return;
            if (/^\/[^/]+\/t\/\d+\/?$/.test(window.location.pathname)) {
                history.replaceState(history.state ?? {}, '', boardPathWithQuery(s.slug));
            }
        },
    };
}
function openStoryFromBoard(s, localId) {
    if (!isCurrent(s))
        return;
    const target = storyPathWithQuery(s.slug, localId);
    if (window.location.pathname + window.location.search !== target) {
        history.pushState({}, '', target);
    }
    void openStory(s, localId);
}
async function openStory(s, localId) {
    const result = await openPublicTodo(dialogContext(s), localId);
    if (!isCurrent(s)) {
        closePublicTodo();
        return;
    }
    if (result === 'unavailable' || result === 'failed') {
        showToast(t(result === 'unavailable' ? 'publicBoard.story.unavailable' : 'publicBoard.story.loadFailed'));
        history.replaceState(history.state ?? {}, '', boardPathWithQuery(s.slug));
    }
}
function parseLocalIdSegment(segment) {
    if (!segment || !/^\d{1,15}$/.test(segment))
        return null;
    const n = Number(segment);
    return Number.isSafeInteger(n) && n > 0 ? n : null;
}
// ---- Realtime and revocation ----
function handleStreamMessage(s, data) {
    if (!isCurrent(s) || typeof data !== 'string')
        return;
    let type;
    try {
        type = JSON.parse(data)?.type;
    }
    catch {
        return;
    }
    if (type === 'refresh_needed') {
        requestRefresh(s);
    }
    else if (type === 'access_revoked') {
        enterUnavailable(s);
    }
}
function connectStream(s) {
    const manager = new SseConnectionManager(publicBoardEventsPath(s.slug), {
        label: 'public-board',
        onOpen: () => {
            if (!isCurrent(s))
                return;
            // Events are not replayed across reconnects; catch up once per reconnect.
            if (s.streamOpenedOnce)
                requestRefresh(s);
            s.streamOpenedOnce = true;
        },
        onMessage: (event) => handleStreamMessage(s, event.data),
        // A failed stream is not proof of unpublication; a coalesced public read
        // decides (authoritative 404 → unavailable, otherwise keep content).
        onTransportError: () => {
            if (isCurrent(s))
                requestRefresh(s);
        },
    }, PUBLIC_STREAM_POLICY);
    s.stream = manager;
    manager.open();
}
/**
 * Terminal for this view: stops the stream, cancels pending work, clears all
 * public content and story details, and shows a generic unavailable state.
 * Nothing reconnects until fresh navigation resolves the board again.
 */
function enterUnavailable(s) {
    if (!isCurrent(s))
        return;
    session = null;
    teardown(s);
    closePublicTodo();
    s.columns = {};
    s.lanes = {};
    setBoardAccess(null);
    app.innerHTML = buildPublicUnavailableHtml();
    app.querySelector('#publicHomeBtn')?.addEventListener('click', () => window.location.assign('/'));
}
// ---- Entry points ----
/**
 * Attempts the public projection for a slug whose member/temporary read was
 * denied or not found. Returns 'not-public' for any public failure so the
 * caller keeps the existing unavailable/sign-in behavior.
 */
export async function resolvePublicBoard(input) {
    stopPublicBoard();
    const seq = resolutionSeq;
    const controller = new AbortController();
    resolutionController = controller;
    let query = readPublicQueryFromUrl();
    let snapshot;
    try {
        try {
            snapshot = await fetchPublicBoardSnapshot(input.slug, query, { signal: controller.signal });
        }
        catch (err) {
            if (!(err instanceof PublicBoardApiError) || err.kind !== 'invalid_request' || !isPublicQueryActive(query))
                throw err;
            // Unsupported filter in the URL: drop it rather than hiding the board.
            query = EMPTY_PUBLIC_QUERY;
            writePublicQueryToUrl(query);
            snapshot = await fetchPublicBoardSnapshot(input.slug, query, { signal: controller.signal });
        }
    }
    catch {
        return seq === resolutionSeq && getSlug() === input.slug ? 'not-public' : 'stale';
    }
    finally {
        if (resolutionController === controller)
            resolutionController = null;
    }
    if (seq !== resolutionSeq || getSlug() !== input.slug)
        return 'stale';
    // The router has already stopped member-board events for this slug; clear
    // any member board state so no member-only code path sees a board.
    setBoard(null);
    setProjectId(null);
    setBoardAccess(PUBLIC_BOARD_ACCESS);
    const s = {
        slug: input.slug,
        snapshot,
        columns: {},
        lanes: {},
        query,
        sprints: [],
        activeMobileTab: snapshot.workflow[0]?.key ?? '',
        dataEpoch: 0,
        stream: null,
        streamOpenedOnce: false,
        refreshTimer: null,
        refreshInFlight: false,
        refreshQueued: false,
        snapshotController: null,
        laneControllers: new Map(),
        searchTimer: null,
        sprintsLoaded: false,
        sprintsSeq: 0,
        sprintsController: null,
        filterError: false,
        listeners: new AbortController(),
        pageScope: new AbortController(),
        omni: null,
        rendered: { columns: '', tabs: '', panel: '' },
    };
    applySnapshot(s, snapshot);
    session = s;
    renderFull(s);
    bindSessionListeners(s);
    connectStream(s);
    syncSprintVocabulary(s, 'initial');
    const localId = parseLocalIdSegment(input.openTodoSegment);
    if (localId !== null)
        await openStory(s, localId);
    return 'rendered';
}
/** In-board navigation (filters in the URL, story deep links, back/forward). */
export async function applyPublicBoardRoute(input) {
    const s = session;
    if (!s || s.slug !== input.slug)
        return;
    const query = readPublicQueryFromUrl();
    if (!samePublicQuery(query, s.query)) {
        // The URL wins over a pending keystroke; the topbar and input are kept.
        if (s.searchTimer !== null)
            clearTimeout(s.searchTimer);
        s.searchTimer = null;
        s.query = query;
        syncSearchInput(s);
        renderBoardRegions(s);
        void reloadSnapshot(s, 'filter');
    }
    const localId = parseLocalIdSegment(input.openTodoSegment);
    if (localId === null) {
        closePublicTodo();
    }
    else if (getOpenPublicTodoLocalId() !== localId) {
        await openStory(s, localId);
    }
}
export function __getPublicBoardSessionForTest() {
    return session;
}
export function __requestPublicRefreshForTest() {
    if (session)
        requestRefresh(session);
}
