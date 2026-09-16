import { on } from '../events.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { normalizeBoardTodoSort, setBoardTodoSortPreference } from '../core/board-sort-preferences.js';
import { getBoardFilterLayoutPreference } from '../core/board-filter-layout-preferences.js';
import { getAssigneeFromUrl, getBoard, getPriorityFromUrl, getSlug, getSortFromUrl, getSprintIdFromUrl, getTag, getTagColors, getUser, } from '../state/selectors.js';
import { boardSprintsEnabled } from '../sprints.js';
import { escapeHTML, sanitizeHexColor, showToast } from '../utils.js';
import { buildSprintFilterSectionHtml, buildChipsHTML, getCombinedChipData, isBoardFilterActive, } from './board-rendering.js';
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
const MOBILE_TAG_BREAKPOINT = 767;
const MOBILE_TAG_ROWS_PER_PAGE = 2;
const FILTER_BOUND_FLAG = Symbol('boardFiltersBound');
let reloadBoardFn = null;
let showErrorFn = null;
function setTagParam(tag) {
    const url = new URL(window.location.href);
    if (tag)
        url.searchParams.set("tag", tag);
    else
        url.searchParams.delete("tag");
    history.replaceState({}, "", url.pathname + url.search);
}
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
    reloadBoardFn(getSlug(), urlFilter("tag") ?? "", urlFilter("search"), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
        showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
    });
}
export function cancelPendingSearchReload() {
    if (searchTimeout !== null) {
        clearTimeout(searchTimeout);
        searchTimeout = null;
    }
}
export function matchOmniTags(query, tags, appliedTag = '') {
    const needle = query.trim().toLowerCase();
    if (!needle)
        return [];
    const applied = appliedTag.toLowerCase();
    return tags
        .filter((tag) => tag.count > 0 && tag.name.toLowerCase() !== applied)
        .map((tag) => {
        const name = tag.name.toLowerCase();
        const rank = name === needle ? 0 : name.startsWith(needle) ? 1 : name.includes(needle) ? 2 : 3;
        return { tag, rank };
    })
        .filter((entry) => entry.rank < 3)
        .sort((a, b) => {
        if (a.rank !== b.rank)
            return a.rank - b.rank;
        const left = a.tag.name.toLowerCase();
        const right = b.tag.name.toLowerCase();
        return left < right ? -1 : left > right ? 1 : a.tag.name < b.tag.name ? -1 : a.tag.name > b.tag.name ? 1 : 0;
    })
        .map((entry) => entry.tag);
}
function renderOmniTagPills(board = getBoard()) {
    const pills = document.getElementById('omniTagPills');
    const input = document.getElementById('searchInput');
    if (!pills || !input || !board)
        return;
    const selectedTag = urlFilter('tag') ?? getTag() ?? '';
    const selectedFromBoard = board.tags.find((tag) => tag.name.toLocaleLowerCase() === selectedTag.toLocaleLowerCase());
    const selectedColor = selectedFromBoard?.color || getTagColors()[selectedTag];
    const selectedSafeColor = sanitizeHexColor(selectedColor);
    const selectedStyle = selectedSafeColor
        ? ` style="border-color:${escapeHTML(selectedSafeColor)};background:${escapeHTML(selectedSafeColor)}20;color:${escapeHTML(selectedSafeColor)}"`
        : '';
    const selectedHTML = selectedTag
        ? `<span class="omni-tag-pill omni-tag-pill--applied"${selectedStyle}>
        <span>${escapeHTML(selectedTag)}</span>
        <button type="button" class="omni-tag-pill__clear" data-omni-clear-tag aria-label="${escapeHTML(t('board.filters.clearTag', { name: selectedTag }))}">×</button>
      </span>`
        : '';
    const suggestionsHTML = matchOmniTags(input.value, board.tags, selectedTag).map((tag) => {
        const color = sanitizeHexColor(tag.color || getTagColors()[tag.name]);
        const style = color ? ` style="border-color:${escapeHTML(color)};background:${escapeHTML(color)}20;color:${escapeHTML(color)}"` : '';
        return `<button type="button" class="omni-tag-pill omni-tag-pill--suggestion" data-omni-tag="${escapeHTML(tag.name)}"${style}>${escapeHTML(tag.name)}</button>`;
    }).join('');
    pills.innerHTML = selectedHTML + suggestionsHTML;
}
export function updateOmniTagPills(board = getBoard()) {
    if (getBoardFilterLayoutPreference() !== 'omni')
        return;
    renderOmniTagPills(board);
}
function bindOmniTagPills() {
    const pills = document.getElementById('omniTagPills');
    if (!pills)
        return;
    pills.onkeydown = (event) => {
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
    pills.onclick = (event) => {
        const target = event.target instanceof Element ? event.target.closest('[data-omni-tag], [data-omni-clear-tag]') : null;
        if (!target)
            return;
        const input = document.getElementById('searchInput');
        if (target.hasAttribute('data-omni-clear-tag')) {
            setTagParam('');
        }
        else {
            cancelPendingSearchReload();
            const nextTag = target.getAttribute('data-omni-tag') ?? '';
            if (input)
                input.value = '';
            document.getElementById('searchClear')?.remove();
            setSearchParam('');
            setTagParam(nextTag);
        }
        renderOmniTagPills();
        input?.focus();
        reloadBoardWithCurrentFilters();
    };
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
                setTagParam(nextTag);
            }
            else {
                setTagParam(nextTag);
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
                setTagParam("");
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
                setTagParam("");
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
        reloadBoardFn(getSlug(), urlFilter('tag') ?? '', null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
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
            reloadBoardFn(getSlug(), urlFilter('tag') ?? '', trimmedValue || null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
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
function closeFilterPanel(panel, toggle) {
    panel.hidden = true;
    toggle.setAttribute("aria-expanded", "false");
}
// The panel is `position: fixed` and positioned here (rather than via CSS
// `top/right` relative to `.search-input-wrapper`) because `.topbar` sets
// `overflow-y: hidden` for its mobile chip-wrapping layout; an absolutely
// positioned descendant would be clipped to the topbar's own box.
function positionFilterPanel(panel, toggle) {
    const toggleRect = toggle.getBoundingClientRect();
    const margin = 8;
    panel.style.top = `${toggleRect.bottom + 6}px`;
    // Measure after making it visible (but off-thread of layout) so panel.offsetWidth is accurate.
    const panelWidth = panel.offsetWidth || 200;
    let left = toggleRect.right - panelWidth;
    left = Math.max(margin, Math.min(left, window.innerWidth - panelWidth - margin));
    panel.style.left = `${left}px`;
}
function openFilterPanel(panel, toggle) {
    panel.hidden = false;
    toggle.setAttribute("aria-expanded", "true");
    positionFilterPanel(panel, toggle);
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
            openFilterPanel(panel, toggle);
        }
        else {
            closeFilterPanel(panel, toggle);
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
        closeFilterPanel(panel, toggle);
        reloadBoardFn?.(getSlug(), urlFilter('tag') ?? '', urlFilter('search'), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err) => {
            showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
        });
        return;
    }
    if (!panel.hidden && !panel.contains(target)) {
        closeFilterPanel(panel, toggle);
    }
}
function handleFilterPanelDocumentKeydown(e) {
    const elements = getFilterPanelElements();
    if (!elements)
        return;
    const { toggle, panel } = elements;
    if (e.key === "Escape" && !panel.hidden) {
        closeFilterPanel(panel, toggle);
        toggle.focus();
    }
}
function handleFilterPanelWindowResize() {
    const elements = getFilterPanelElements();
    if (!elements)
        return;
    const { toggle, panel } = elements;
    if (!panel.hidden)
        positionFilterPanel(panel, toggle);
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
    const isMobile = window.matchMedia(`(max-width: ${MOBILE_TAG_BREAKPOINT}px)`).matches;
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
export function computeBoardChipsRender(board, tag, sprintId) {
    const displayTags = board.tags.filter((candidate) => candidate.count > 0 || candidate.name === tag);
    if (tag && !displayTags.some((candidate) => candidate.name === tag)) {
        displayTags.push({ name: tag, count: 0 });
    }
    const sprintData = boardSprintsEnabled(board) ? lastSprintsData : null;
    const effectiveSprintId = boardSprintsEnabled(board) ? sprintId : null;
    const combinedChipData = getCombinedChipData(displayTags, tag || "", sprintData, effectiveSprintId, getTagColors());
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
        renderOmniTagPills(board);
        return;
    }
    const { chipsHTML, chipsUnchanged } = computeBoardChipsRender(board, getTag() || "", sprintId ?? null);
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
