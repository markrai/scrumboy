import { on } from '../events.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { normalizeBoardTodoSort, setBoardTodoSortPreference } from '../core/board-sort-preferences.js';
import { getBoardFilterLayoutPreference } from '../core/board-filter-layout-preferences.js';
import { getAssigneeFromUrl, getBoard, getPriorityFromUrl, getSlug, getSortFromUrl, getSprintIdFromUrl, getTagColors, getUser, } from '../state/selectors.js';
import { getTagsFromUrl, MAX_BOARD_TAG_FILTERS, setTagParams } from '../state/board-filter-url.js';
import { boardSprintsEnabled } from '../sprints.js';
import { showToast } from '../utils.js';
import { buildSprintFilterSectionHtml, buildChipsHTML, getCombinedChipData, isBoardFilterActive, } from './board-rendering.js';
import { closeFilterPopover, openFilterPopover, positionFilterPopover } from './filter-popover.js';
import { buildOmniRowContent, compareOmniTagNames, findOmniRowElements, matchOmniTagCandidates, OMNI_MOBILE_MAX_WIDTH_PX, pageOmniCandidates, rankOmniBrowseCandidates, renderOmniRow, revealFocusedPinnedOmniPill, revealLastPinnedOmniPill, updateOmniChevrons, } from './omni-tags.js';
let lastDisplayChipData = [];
let lastSprintsData = null;
let lastSprintsDataSlug = null;
let lastRenderedChipsHTML = "";
let mobileTagPage = 0;
let mobileTagPageBoundaries = [];
let mobileTagPaginationResizeBound = false;
let sprintEventSubscribed = false;
let filterPanelDelegationBound = false;
let searchTimeout = null;
let omniCandidateResizeObserver = null;
let omniCandidateWindowResizeBound = false;
const MOBILE_TAG_ROWS_PER_PAGE = 2;
const FILTER_BOUND_FLAG = Symbol('boardFiltersBound');
let reloadBoardFn = null;
let showErrorFn = null;
function setSprintParam(sprintId) {
    const url = new URL(window.location.href);
    if (sprintId)
        url.searchParams.set("sprintId", sprintId);
    else
        url.searchParams.delete("sprintId");
    history.replaceState({}, "", url.pathname + url.search);
}
function setSearchParam(search) {
    const url = new URL(window.location.href);
    if (search)
        url.searchParams.set("search", search);
    else
        url.searchParams.delete("search");
    history.replaceState({}, "", url.pathname + url.search);
}
function setAssigneeParam(assignee) {
    const url = new URL(window.location.href);
    if (assignee)
        url.searchParams.set("assignee", assignee);
    else
        url.searchParams.delete("assignee");
    history.replaceState({}, "", url.pathname + url.search);
}
function setSortParam(sort) {
    const url = new URL(window.location.href);
    if (sort)
        url.searchParams.set("sort", sort);
    else
        url.searchParams.delete("sort");
    history.replaceState({}, "", url.pathname + url.search);
}
function setPriorityParam(priority) {
    const url = new URL(window.location.href);
    if (priority)
        url.searchParams.set("priority", priority);
    else
        url.searchParams.delete("priority");
    history.replaceState({}, "", url.pathname + url.search);
}
function urlFilter(name) {
    return new URL(window.location.href).searchParams.get(name);
}
function reloadBoardWithCurrentFilters() {
    if (!reloadBoardFn)
        return;
    reloadBoardFn(getSlug(), getTagsFromUrl(), urlFilter("search"), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
        showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
    });
}
export function cancelPendingSearchReload() {
    if (searchTimeout !== null) {
        clearTimeout(searchTimeout);
        searchTimeout = null;
    }
}
export function matchOmniTags(query, tags, appliedTags = []) {
    return matchOmniTagCandidates(query, tags.filter((tag) => tag.count > 0), appliedTags, MAX_BOARD_TAG_FILTERS);
}
function browseTimestamp(tag) {
    if (!tag.lastActiveAt)
        return Number.NEGATIVE_INFINITY;
    const parsed = Date.parse(tag.lastActiveAt);
    return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}
/** Member Browse order: most recently active first, then count, then name. */
function compareMemberBrowseTags(left, right) {
    const leftTime = browseTimestamp(left);
    const rightTime = browseTimestamp(right);
    if (leftTime !== rightTime)
        return leftTime > rightTime ? -1 : 1;
    if (left.count !== right.count)
        return right.count - left.count;
    return compareOmniTagNames(left.name, right.name);
}
export function rankBrowseOmniTags(tags, pinnedTags = []) {
    return rankOmniBrowseCandidates(tags.filter((tag) => tag.count > 0), pinnedTags, MAX_BOARD_TAG_FILTERS, compareMemberBrowseTags);
}
const MEMBER_OMNI_IDS = {
    pinned: 'omniPinnedTags',
    region: 'omniCandidateRegion',
    previous: 'omniCandidatePrev',
    viewport: 'omniCandidateViewport',
    next: 'omniCandidateNext',
    mobile: 'omniMobileTagPills',
};
export function updateOmniCandidateChevronState() {
    const viewport = document.getElementById(MEMBER_OMNI_IDS.viewport);
    if (!viewport)
        return;
    updateOmniChevrons(viewport, document.getElementById(MEMBER_OMNI_IDS.previous), document.getElementById(MEMBER_OMNI_IDS.next));
}
function resetOmniCandidateScroll() {
    const viewport = document.getElementById(MEMBER_OMNI_IDS.viewport);
    if (viewport)
        viewport.scrollLeft = 0;
    updateOmniCandidateChevronState();
}
export function revealLastPinnedOmniTag() {
    const pinned = document.getElementById(MEMBER_OMNI_IDS.pinned);
    if (pinned)
        revealLastPinnedOmniPill(pinned);
}
function handleOmniPinnedFocusIn(event) {
    const pinned = document.getElementById(MEMBER_OMNI_IDS.pinned);
    if (pinned)
        revealFocusedPinnedOmniPill(pinned, event.target);
}
function syncOmniDesktopRailLayout() {
    updateOmniCandidateChevronState();
    revealLastPinnedOmniTag();
}
function renderOmniTagPills(board = getBoard()) {
    const elements = findOmniRowElements(document, MEMBER_OMNI_IDS);
    const input = document.getElementById('searchInput');
    if (!elements || !input || !board)
        return;
    const selectedTags = getTagsFromUrl();
    const applied = selectedTags.map((selectedTag) => {
        const selectedFromBoard = board.tags.find((tag) => tag.name.toLocaleLowerCase() === selectedTag.toLocaleLowerCase());
        return { name: selectedTag, color: selectedFromBoard?.color || getTagColors()[selectedTag] };
    });
    const browseMode = input.value.trim() === '';
    const candidates = browseMode
        ? rankBrowseOmniTags(board.tags, selectedTags)
        : matchOmniTags(input.value, board.tags, selectedTags);
    renderOmniRow(elements, buildOmniRowContent({
        applied,
        candidates: candidates.map((tag) => ({ name: tag.name, color: tag.color || getTagColors()[tag.name] })),
        browse: browseMode,
        attrs: { tag: 'data-omni-tag', clear: 'data-omni-clear-tag' },
        tone: 'accent',
        clearLabel: (name) => t('board.filters.clearTag', { name }),
    }));
}
export function updateOmniTagPills(board = getBoard()) {
    if (getBoardFilterLayoutPreference() !== 'omni')
        return;
    renderOmniTagPills(board);
}
function bindOmniTagPills() {
    // Anchored on a member-only ID: the shared .omni-bar class also appears on the public board.
    const bar = document.getElementById(MEMBER_OMNI_IDS.pinned)?.closest('.filters--omni .omni-bar') ?? null;
    const viewport = document.getElementById(MEMBER_OMNI_IDS.viewport);
    const previous = document.getElementById(MEMBER_OMNI_IDS.previous);
    const next = document.getElementById(MEMBER_OMNI_IDS.next);
    if (!bar || !viewport)
        return;
    bar.onkeydown = (event) => {
        if (event.key !== 'Enter' && event.key !== ' ')
            return;
        const target = event.target instanceof Element
            ? event.target.closest('[data-omni-tag], [data-omni-clear-tag]')
            : null;
        if (!target)
            return;
        event.preventDefault();
        target.click();
    };
    bar.onclick = (event) => {
        const target = event.target instanceof Element ? event.target.closest('[data-omni-tag], [data-omni-clear-tag]') : null;
        if (!target)
            return;
        const input = document.getElementById('searchInput');
        if (target.hasAttribute('data-omni-clear-tag')) {
            const clearTag = target.getAttribute('data-omni-clear-tag') ?? '';
            setTagParams(getTagsFromUrl().filter((tag) => tag !== clearTag));
        }
        else {
            cancelPendingSearchReload();
            const nextTag = target.getAttribute('data-omni-tag') ?? '';
            if (input)
                input.value = '';
            document.getElementById('searchClear')?.remove();
            setSearchParam('');
            setTagParams([...getTagsFromUrl(), nextTag]);
        }
        renderOmniTagPills();
        input?.focus();
        reloadBoardWithCurrentFilters();
    };
    if (previous)
        previous.onclick = () => pageOmniCandidates(viewport, -1);
    if (next)
        next.onclick = () => pageOmniCandidates(viewport, 1);
    viewport.onscroll = updateOmniCandidateChevronState;
    const pinned = document.getElementById(MEMBER_OMNI_IDS.pinned);
    if (pinned) {
        pinned.removeEventListener('focusin', handleOmniPinnedFocusIn);
        pinned.addEventListener('focusin', handleOmniPinnedFocusIn);
    }
    omniCandidateResizeObserver?.disconnect();
    if (typeof ResizeObserver !== 'undefined') {
        omniCandidateResizeObserver = new ResizeObserver(syncOmniDesktopRailLayout);
        omniCandidateResizeObserver.observe(viewport);
        if (pinned)
            omniCandidateResizeObserver.observe(pinned);
    }
    if (!omniCandidateWindowResizeBound) {
        window.addEventListener('resize', syncOmniDesktopRailLayout);
        omniCandidateWindowResizeBound = true;
    }
    renderOmniTagPills();
}
function attachChipsDelegatedHandler() {
    const tagChipsEl = document.getElementById("tagChips");
    if (!tagChipsEl)
        return;
    tagChipsEl.onclick = (e) => {
        const chip = e.target.closest("[data-tag], [data-sprint-id], [data-sprint-clear]");
        if (!chip)
            return;
        const additive = e.ctrlKey || e.metaKey;
        if (chip.hasAttribute("data-tag")) {
            const nextTag = chip.getAttribute("data-tag") ?? "";
            if (additive) {
                setTagParams(nextTag ? [nextTag] : []);
            }
            else {
                setTagParams(nextTag ? [nextTag] : []);
                setSprintParam(null);
            }
            reloadBoardWithCurrentFilters();
        }
        else if (chip.hasAttribute("data-sprint-clear")) {
            if (additive) {
                setSprintParam(null);
            }
            else {
                setSprintParam(null);
                setTagParams([]);
            }
            reloadBoardWithCurrentFilters();
        }
        else if (chip.hasAttribute("data-sprint-id")) {
            const nextSprint = chip.getAttribute("data-sprint-id") ?? "";
            if (additive) {
                setSprintParam(nextSprint);
            }
            else {
                setSprintParam(nextSprint);
                setTagParams([]);
            }
            reloadBoardWithCurrentFilters();
        }
    };
}
function bindSearchInput() {
    const searchInput = document.getElementById("searchInput");
    if (!searchInput || searchInput[FILTER_BOUND_FLAG])
        return;
    const handleClearClick = () => {
        cancelPendingSearchReload();
        searchInput.value = "";
        setSearchParam("");
        if (!reloadBoardFn)
            return;
        reloadBoardFn(getSlug(), getTagsFromUrl(), null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
            showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
        });
        updateClearButton();
        renderOmniTagPills();
    };
    const updateClearButton = () => {
        const clearBtn = document.getElementById("searchClear");
        const wrapper = searchInput.closest(".search-input-wrapper");
        if (!wrapper)
            return;
        const hasValue = searchInput.value.trim() !== "";
        if (hasValue && !clearBtn) {
            const clearSearchLabel = t("board.actions.clearSearch");
            const btn = document.createElement("button");
            btn.className = "search-clear";
            btn.id = "searchClear";
            btn.setAttribute("aria-label", clearSearchLabel);
            btn.setAttribute("data-i18n-aria-label", "board.actions.clearSearch");
            btn.setAttribute("title", clearSearchLabel);
            btn.setAttribute("data-i18n-title", "board.actions.clearSearch");
            btn.textContent = "✕";
            btn.addEventListener("click", handleClearClick);
            wrapper.appendChild(btn);
        }
        else if (!hasValue && clearBtn) {
            clearBtn.remove();
        }
    };
    searchInput.addEventListener("input", (e) => {
        const input = e.target;
        const value = input.value;
        updateClearButton();
        renderOmniTagPills();
        cancelPendingSearchReload();
        searchTimeout = setTimeout(() => {
            searchTimeout = null;
            const trimmedValue = value.trim();
            setSearchParam(trimmedValue);
            if (!reloadBoardFn)
                return;
            reloadBoardFn(getSlug(), getTagsFromUrl(), trimmedValue || null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
                showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
            });
        }, 300);
    });
    const existingClearBtn = document.getElementById("searchClear");
    if (existingClearBtn) {
        existingClearBtn.addEventListener("click", handleClearClick);
    }
    updateClearButton();
    searchInput[FILTER_BOUND_FLAG] = true;
}
// updateFilterToggleActiveState toggles the CSS class that drives the slow
// pulse/glow @keyframes animation on the chevron whenever a non-default
// assignee filter or sort order is currently applied (from the URL).
function updateFilterToggleActiveState(toggle) {
    const active = isBoardFilterActive(getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl(), getSprintIdFromUrl(), getBoardFilterLayoutPreference());
    toggle.classList.toggle("search-filter-toggle--active", active);
}
function getFilterPanelElements() {
    const toggle = document.getElementById("searchFilterToggle");
    const panel = document.getElementById("searchFilterPanel");
    return toggle && panel ? { toggle, panel } : null;
}
function handleFilterPanelDocumentClick(e) {
    const elements = getFilterPanelElements();
    const target = e.target;
    if (!elements || !(target instanceof Node))
        return;
    const { toggle, panel } = elements;
    if (toggle.contains(target)) {
        if (panel.hidden) {
            openFilterPopover(panel, toggle);
        }
        else {
            closeFilterPopover(panel, toggle);
        }
        return;
    }
    const optionEl = target instanceof Element
        ? target.closest("[data-assignee-option], [data-sort-option], [data-priority-option], [data-sprint-option]")
        : null;
    if (optionEl && panel.contains(optionEl)) {
        const kind = optionEl.hasAttribute("data-assignee-option")
            ? "assignee"
            : optionEl.hasAttribute("data-sort-option")
                ? "sort"
                : optionEl.hasAttribute("data-priority-option")
                    ? "priority"
                    : "sprint";
        const attr = kind === "assignee"
            ? "data-assignee-option"
            : kind === "sort"
                ? "data-sort-option"
                : kind === "priority"
                    ? "data-priority-option"
                    : "data-sprint-option";
        const value = optionEl.getAttribute(attr) || null;
        const label = optionEl.textContent?.trim() || "";
        if (kind === "assignee") {
            setAssigneeParam(value);
            panel.querySelectorAll("[data-assignee-option]").forEach((el) => el.classList.remove("is-active"));
        }
        else if (kind === "sort") {
            setSortParam(value);
            if (getUser()) {
                setBoardTodoSortPreference(normalizeBoardTodoSort(value));
            }
            panel.querySelectorAll("[data-sort-option]").forEach((el) => el.classList.remove("is-active"));
        }
        else if (kind === "priority") {
            setPriorityParam(value);
            panel.querySelectorAll("[data-priority-option]").forEach((el) => el.classList.remove("is-active"));
        }
        else {
            setSprintParam(value);
            panel.querySelectorAll("[data-sprint-option]").forEach((el) => el.classList.remove("is-active"));
        }
        optionEl.classList.add("is-active");
        if (value) {
            showToast(t(kind === "sort" ? "board.filters.sortedBy" : "board.filters.filteringOn", { value: label }));
        }
        updateFilterToggleActiveState(toggle);
        closeFilterPopover(panel, toggle);
        reloadBoardFn?.(getSlug(), getTagsFromUrl(), urlFilter('search'), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
            showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
        });
        return;
    }
    if (!panel.hidden && !panel.contains(target)) {
        closeFilterPopover(panel, toggle);
    }
}
function handleFilterPanelDocumentKeydown(e) {
    const elements = getFilterPanelElements();
    if (!elements)
        return;
    const { toggle, panel } = elements;
    if (e.key === "Escape" && !panel.hidden) {
        closeFilterPopover(panel, toggle);
        toggle.focus();
    }
}
function handleFilterPanelWindowResize() {
    const elements = getFilterPanelElements();
    if (!elements)
        return;
    const { toggle, panel } = elements;
    if (!panel.hidden)
        positionFilterPopover(panel, toggle);
}
function ensureFilterPanelDelegation() {
    if (filterPanelDelegationBound)
        return;
    filterPanelDelegationBound = true;
    document.addEventListener("click", handleFilterPanelDocumentClick);
    document.addEventListener("keydown", handleFilterPanelDocumentKeydown);
    window.addEventListener("resize", handleFilterPanelWindowResize);
}
// bindFilterPanel wires the search input's expandable filter popover: opening
// on toggle click, closing on outside click/Escape, and a delegated click
// handler for the assignee/sort option buttons that updates the URL, reloads
// the board, and shows a brief "Filtering: X" / "Sorted: X" toast (only when
// picking a real filter/sort, not when clearing back to the neutral option).
function bindFilterPanel() {
    const elements = getFilterPanelElements();
    if (!elements)
        return;
    const { toggle } = elements;
    updateFilterToggleActiveState(toggle);
    ensureFilterPanelDelegation();
}
function initMobileTagPagination() {
    const tagChipsEl = document.getElementById("tagChips");
    const chipsNav = document.getElementById("chipsNav");
    if (!tagChipsEl || !chipsNav)
        return;
    const isMobile = window.matchMedia(`(max-width: ${OMNI_MOBILE_MAX_WIDTH_PX}px)`).matches;
    if (!mobileTagPaginationResizeBound) {
        mobileTagPaginationResizeBound = true;
        let resizeTimeout = null;
        window.addEventListener("resize", () => {
            if (resizeTimeout)
                clearTimeout(resizeTimeout);
            resizeTimeout = setTimeout(() => {
                mobileTagPage = 0;
                initMobileTagPagination();
            }, 150);
        });
    }
    if (!isMobile) {
        chipsNav.classList.remove("is-visible");
        chipsNav.setAttribute("aria-hidden", "true");
        attachChipsDelegatedHandler();
        return;
    }
    if (lastDisplayChipData.length <= 1) {
        chipsNav.classList.remove("is-visible");
        chipsNav.setAttribute("aria-hidden", "true");
        attachChipsDelegatedHandler();
        return;
    }
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData);
    const chipEls = Array.from(tagChipsEl.querySelectorAll(".chip"));
    if (chipEls.length === 0) {
        return;
    }
    const rects = chipEls.map((el) => el.getBoundingClientRect());
    const rowTolerance = 2;
    const rows = [];
    let currentRow = 0;
    let lastTop = rects[0].top;
    for (let i = 0; i < rects.length; i++) {
        if (Math.abs(rects[i].top - lastTop) > rowTolerance) {
            currentRow++;
            lastTop = rects[i].top;
        }
        rows[i] = currentRow;
    }
    const numRows = currentRow + 1;
    mobileTagPageBoundaries = [0];
    for (let p = 1; p * MOBILE_TAG_ROWS_PER_PAGE < numRows; p++) {
        const rowStart = p * MOBILE_TAG_ROWS_PER_PAGE;
        const idx = chipEls.findIndex((_, i) => rows[i] >= rowStart);
        if (idx >= 0)
            mobileTagPageBoundaries.push(idx);
    }
    mobileTagPageBoundaries.push(chipEls.length);
    const numPages = mobileTagPageBoundaries.length - 1;
    if (numPages <= 1) {
        chipsNav.classList.remove("is-visible");
        chipsNav.setAttribute("aria-hidden", "true");
        attachChipsDelegatedHandler();
        return;
    }
    mobileTagPage = 0;
    const start = mobileTagPageBoundaries[mobileTagPage];
    const end = mobileTagPageBoundaries[mobileTagPage + 1];
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(start, end));
    chipsNav.classList.add("is-visible");
    chipsNav.setAttribute("aria-hidden", "false");
    const prevBtn = chipsNav.querySelector(".chips-nav__prev");
    const nextBtn = chipsNav.querySelector(".chips-nav__next");
    prevBtn?.replaceWith(prevBtn.cloneNode(true));
    nextBtn?.replaceWith(nextBtn.cloneNode(true));
    const newPrev = chipsNav.querySelector(".chips-nav__prev");
    const newNext = chipsNav.querySelector(".chips-nav__next");
    if (newPrev)
        newPrev.disabled = mobileTagPage === 0;
    if (newNext)
        newNext.disabled = mobileTagPage === numPages - 1;
    newPrev?.addEventListener("click", () => {
        if (mobileTagPage <= 0)
            return;
        mobileTagPage--;
        const s = mobileTagPageBoundaries[mobileTagPage];
        const e = mobileTagPageBoundaries[mobileTagPage + 1];
        tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
        if (newPrev)
            newPrev.disabled = mobileTagPage === 0;
        if (newNext)
            newNext.disabled = mobileTagPage === numPages - 1;
    });
    newNext?.addEventListener("click", () => {
        if (mobileTagPage >= numPages - 1)
            return;
        mobileTagPage++;
        const s = mobileTagPageBoundaries[mobileTagPage];
        const e = mobileTagPageBoundaries[mobileTagPage + 1];
        tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
        if (newPrev)
            newPrev.disabled = mobileTagPage === 0;
        if (newNext)
            newNext.disabled = mobileTagPage === numPages - 1;
    });
    attachChipsDelegatedHandler();
}
export function computeBoardChipsRender(board, tags, sprintId) {
    const selected = new Set(tags.map((tag) => tag.toLocaleLowerCase()));
    const displayTags = board.tags.filter((candidate) => candidate.count > 0 || selected.has(candidate.name.toLocaleLowerCase()));
    for (const tag of tags) {
        if (!displayTags.some((candidate) => candidate.name.toLocaleLowerCase() === tag.toLocaleLowerCase())) {
            displayTags.push({ name: tag, count: 0 });
        }
    }
    const sprintData = boardSprintsEnabled(board) ? lastSprintsData : null;
    const effectiveSprintId = boardSprintsEnabled(board) ? sprintId : null;
    const combinedChipData = getCombinedChipData(displayTags, tags, sprintData, effectiveSprintId, getTagColors());
    lastDisplayChipData = combinedChipData;
    const chipsHTML = buildChipsHTML(combinedChipData);
    const chipsUnchanged = chipsHTML === lastRenderedChipsHTML;
    lastRenderedChipsHTML = chipsHTML;
    return { chipsHTML, chipsUnchanged };
}
export function bindBoardFilterUi(args) {
    reloadBoardFn = args.reloadBoard;
    showErrorFn = args.showError;
    attachChipsDelegatedHandler();
    initMobileTagPagination();
    bindSearchInput();
    bindOmniTagPills();
    bindFilterPanel();
}
export function resetBoardFilterUiState() {
    lastDisplayChipData = [];
    lastRenderedChipsHTML = "";
    mobileTagPage = 0;
    mobileTagPageBoundaries = [];
    resetOmniCandidateScroll();
    omniCandidateResizeObserver?.disconnect();
    omniCandidateResizeObserver = null;
}
export function clearSprintChipDataIfSlugChanged(slug) {
    if (slug !== lastSprintsDataSlug) {
        lastSprintsData = null;
    }
}
export function hasSprintChipDataForSlug(slug) {
    return lastSprintsDataSlug === slug;
}
export function setSprintChipDataForSlug(slug, data) {
    lastSprintsDataSlug = slug;
    lastSprintsData = data;
}
export function getSprintChipDataForSlug(slug) {
    return slug && slug === lastSprintsDataSlug ? lastSprintsData : null;
}
export function clearSprintChipData() {
    lastSprintsData = null;
    lastSprintsDataSlug = null;
}
export function updateChipsOnly(sprintId) {
    const board = getBoard();
    if (!board)
        return;
    if (getBoardFilterLayoutPreference() === 'omni') {
        const section = document.querySelector('[data-sprint-filter-section]');
        if (section && boardSprintsEnabled(board)) {
            section.outerHTML = buildSprintFilterSectionHtml(sprintId, lastSprintsData);
        }
        const toggle = document.getElementById('searchFilterToggle');
        if (toggle)
            updateFilterToggleActiveState(toggle);
        return;
    }
    const { chipsHTML, chipsUnchanged } = computeBoardChipsRender(board, getTagsFromUrl(), sprintId ?? null);
    if (chipsUnchanged)
        return;
    const tagChipsEl = document.getElementById("tagChips");
    if (tagChipsEl) {
        tagChipsEl.innerHTML = chipsHTML;
        bindBoardFilterUi({
            reloadBoard: reloadBoardFn ?? (async () => { }),
            showError: showErrorFn ?? (() => { }),
        });
    }
}
export function notifySprintStateChanged(sprintId, newState) {
    if (!lastSprintsData || getSlug() !== lastSprintsDataSlug)
        return;
    const id = Number(sprintId);
    const sprint = lastSprintsData.sprints.find((s) => s.id === id);
    if (!sprint)
        return;
    if (sprint.state === newState)
        return;
    sprint.state = newState;
    updateChipsOnly(getSprintIdFromUrl());
}
export function ensureSprintSubscription() {
    if (sprintEventSubscribed)
        return;
    sprintEventSubscribed = true;
    on("sprint-updated", (payload) => {
        if (payload && payload.sprintId != null && (payload.state === "ACTIVE" || payload.state === "CLOSED")) {
            notifySprintStateChanged(payload.sprintId, payload.state);
        }
    });
}
