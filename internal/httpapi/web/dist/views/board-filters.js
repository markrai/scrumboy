import { on } from '../events.js';
import { getBoard, getSearch, getSlug, getSprintIdFromUrl, getTag, getTagColors, } from '../state/selectors.js';
import { isAnonymousBoard } from '../utils.js';
import { buildChipsHTML, getCombinedChipData, } from './board-rendering.js';
let lastDisplayChipData = [];
let lastSprintsData = null;
let lastSprintsDataSlug = null;
let lastRenderedChipsHTML = "";
let mobileTagPage = 0;
let mobileTagPageBoundaries = [];
let mobileTagPaginationResizeBound = false;
let sprintEventSubscribed = false;
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
function reloadBoardWithCurrentFilters() {
    if (!reloadBoardFn)
        return;
    reloadBoardFn(getSlug(), new URL(window.location.href).searchParams.get("tag") ?? "", getSearch(), getSprintIdFromUrl()).catch((err) => {
        showErrorFn?.(err?.message || String(err));
    });
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
    let searchTimeout = null;
    const handleClearClick = () => {
        searchInput.value = "";
        setSearchParam("");
        if (!reloadBoardFn)
            return;
        reloadBoardFn(getSlug(), getTag(), null, getSprintIdFromUrl()).catch((err) => {
            showErrorFn?.(err?.message || String(err));
        });
        updateClearButton();
    };
    const updateClearButton = () => {
        const clearBtn = document.getElementById("searchClear");
        const wrapper = searchInput.closest(".search-input-wrapper");
        if (!wrapper)
            return;
        const hasValue = searchInput.value.trim() !== "";
        if (hasValue && !clearBtn) {
            const btn = document.createElement("button");
            btn.className = "search-clear";
            btn.id = "searchClear";
            btn.setAttribute("aria-label", "Clear search");
            btn.setAttribute("title", "Clear search");
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
        clearTimeout(searchTimeout);
        searchTimeout = setTimeout(() => {
            const trimmedValue = value.trim();
            setSearchParam(trimmedValue);
            if (!reloadBoardFn)
                return;
            reloadBoardFn(getSlug(), getTag(), trimmedValue || null, getSprintIdFromUrl()).catch((err) => {
                showErrorFn?.(err?.message || String(err));
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
    const isAnonymousTempBoard = isAnonymousBoard(board);
    const displayTags = isAnonymousTempBoard
        ? board.tags.filter((t) => t.count > 0)
        : board.tags;
    const combinedChipData = getCombinedChipData(displayTags, tag || "", lastSprintsData, sprintId ?? null, getTagColors());
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
export function clearSprintChipData() {
    lastSprintsData = null;
    lastSprintsDataSlug = null;
}
export function updateChipsOnly(sprintId) {
    const board = getBoard();
    if (!board)
        return;
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
