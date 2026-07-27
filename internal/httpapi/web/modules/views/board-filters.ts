import { on } from '../events.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import {
  getAssigneeFromUrl,
  getBoard,
  getSearch,
  getSlug,
  getSortFromUrl,
  getSprintIdFromUrl,
  getTag,
  getTagColors,
} from '../state/selectors.js';
import { Board } from '../types.js';
import { isAnonymousBoard, showToast } from '../utils.js';
import {
  buildChipsHTML,
  getCombinedChipData,
  isBoardFilterActive,
  type ChipData,
  type SprintChipData,
} from './board-rendering.js';

let lastDisplayChipData: ChipData[] = [];
let lastSprintsData: SprintChipData | null = null;
let lastSprintsDataSlug: string | null = null;
let lastRenderedChipsHTML = "";
let mobileTagPage = 0;
let mobileTagPageBoundaries: number[] = [];
let mobileTagPaginationResizeBound = false;
let sprintEventSubscribed = false;
let filterPanelDelegationBound = false;

const MOBILE_TAG_BREAKPOINT = 767;
const MOBILE_TAG_ROWS_PER_PAGE = 2;
const FILTER_BOUND_FLAG = Symbol('boardFiltersBound');

type ReloadBoardFn = (slug: string | null, tag: string | null, search: string | null, sprintId: string | null, assignee: string | null, sort: string | null) => Promise<void>;

let reloadBoardFn: ReloadBoardFn | null = null;
let showErrorFn: ((message: string) => void) | null = null;

function setTagParam(tag: string): void {
  const url = new URL(window.location.href);
  if (tag) url.searchParams.set("tag", tag);
  else url.searchParams.delete("tag");
  history.replaceState({}, "", url.pathname + url.search);
}

function setSprintParam(sprintId: string | null): void {
  const url = new URL(window.location.href);
  if (sprintId) url.searchParams.set("sprintId", sprintId);
  else url.searchParams.delete("sprintId");
  history.replaceState({}, "", url.pathname + url.search);
}

function setSearchParam(search: string): void {
  const url = new URL(window.location.href);
  if (search) url.searchParams.set("search", search);
  else url.searchParams.delete("search");
  history.replaceState({}, "", url.pathname + url.search);
}

function setAssigneeParam(assignee: string | null): void {
  const url = new URL(window.location.href);
  if (assignee) url.searchParams.set("assignee", assignee);
  else url.searchParams.delete("assignee");
  history.replaceState({}, "", url.pathname + url.search);
}

function setSortParam(sort: string | null): void {
  const url = new URL(window.location.href);
  if (sort) url.searchParams.set("sort", sort);
  else url.searchParams.delete("sort");
  history.replaceState({}, "", url.pathname + url.search);
}

function reloadBoardWithCurrentFilters(): void {
  if (!reloadBoardFn) return;
  reloadBoardFn(
    getSlug(),
    new URL(window.location.href).searchParams.get("tag") ?? "",
    getSearch(),
    getSprintIdFromUrl(),
    getAssigneeFromUrl(),
    getSortFromUrl(),
  ).catch((err: any) => {
    showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
  });
}

function attachChipsDelegatedHandler(): void {
  const tagChipsEl = document.getElementById("tagChips");
  if (!tagChipsEl) return;
  tagChipsEl.onclick = (e: MouseEvent) => {
    const chip = (e.target as HTMLElement).closest("[data-tag], [data-sprint-id], [data-sprint-clear]") as HTMLElement | null;
    if (!chip) return;
    const additive = e.ctrlKey || e.metaKey;
    if (chip.hasAttribute("data-tag")) {
      const nextTag = chip.getAttribute("data-tag") ?? "";
      if (additive) {
        setTagParam(nextTag);
      } else {
        setTagParam(nextTag);
        setSprintParam(null);
      }
      reloadBoardWithCurrentFilters();
    } else if (chip.hasAttribute("data-sprint-clear")) {
      if (additive) {
        setSprintParam(null);
      } else {
        setSprintParam(null);
        setTagParam("");
      }
      reloadBoardWithCurrentFilters();
    } else if (chip.hasAttribute("data-sprint-id")) {
      const nextSprint = chip.getAttribute("data-sprint-id") ?? "";
      if (additive) {
        setSprintParam(nextSprint);
      } else {
        setSprintParam(nextSprint);
        setTagParam("");
      }
      reloadBoardWithCurrentFilters();
    }
  };
}

function bindSearchInput(): void {
  const searchInput = document.getElementById("searchInput") as HTMLInputElement | null;
  if (!searchInput || (searchInput as any)[FILTER_BOUND_FLAG]) return;

  let searchTimeout: ReturnType<typeof setTimeout> | null = null;

  const handleClearClick = () => {
    searchInput.value = "";
    setSearchParam("");
    if (!reloadBoardFn) return;
    reloadBoardFn(getSlug(), getTag(), null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl()).catch((err: any) => {
      showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
    });
    updateClearButton();
  };

  const updateClearButton = () => {
    const clearBtn = document.getElementById("searchClear");
    const wrapper = searchInput.closest(".search-input-wrapper");
    if (!wrapper) return;
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
    } else if (!hasValue && clearBtn) {
      clearBtn.remove();
    }
  };

  searchInput.addEventListener("input", (e) => {
    const input = e.target as HTMLInputElement;
    const value = input.value;
    updateClearButton();
    clearTimeout(searchTimeout);
    searchTimeout = setTimeout(() => {
      const trimmedValue = value.trim();
      setSearchParam(trimmedValue);
      if (!reloadBoardFn) return;
      reloadBoardFn(getSlug(), getTag(), trimmedValue || null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl()).catch((err: any) => {
        showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
      });
    }, 300);
  });

  const existingClearBtn = document.getElementById("searchClear");
  if (existingClearBtn) {
    existingClearBtn.addEventListener("click", handleClearClick);
  }

  updateClearButton();
  (searchInput as any)[FILTER_BOUND_FLAG] = true;
}

function closeFilterPanel(panel: HTMLElement, toggle: HTMLElement): void {
  panel.hidden = true;
  toggle.setAttribute("aria-expanded", "false");
}

// The panel is `position: fixed` and positioned here (rather than via CSS
// `top/right` relative to `.search-input-wrapper`) because `.topbar` sets
// `overflow-y: hidden` for its mobile chip-wrapping layout; an absolutely
// positioned descendant would be clipped to the topbar's own box.
function positionFilterPanel(panel: HTMLElement, toggle: HTMLElement): void {
  const toggleRect = toggle.getBoundingClientRect();
  const margin = 8;
  panel.style.top = `${toggleRect.bottom + 6}px`;
  // Measure after making it visible (but off-thread of layout) so panel.offsetWidth is accurate.
  const panelWidth = panel.offsetWidth || 200;
  let left = toggleRect.right - panelWidth;
  left = Math.max(margin, Math.min(left, window.innerWidth - panelWidth - margin));
  panel.style.left = `${left}px`;
}

function openFilterPanel(panel: HTMLElement, toggle: HTMLElement): void {
  panel.hidden = false;
  toggle.setAttribute("aria-expanded", "true");
  positionFilterPanel(panel, toggle);
}

// updateFilterToggleActiveState toggles the CSS class that drives the slow
// pulse/glow @keyframes animation on the chevron whenever a non-default
// assignee filter or sort order is currently applied (from the URL).
function updateFilterToggleActiveState(toggle: HTMLElement): void {
  const active = isBoardFilterActive(getAssigneeFromUrl(), getSortFromUrl());
  toggle.classList.toggle("search-filter-toggle--active", active);
}

function getFilterPanelElements(): { toggle: HTMLButtonElement; panel: HTMLElement } | null {
  const toggle = document.getElementById("searchFilterToggle") as HTMLButtonElement | null;
  const panel = document.getElementById("searchFilterPanel") as HTMLElement | null;
  return toggle && panel ? { toggle, panel } : null;
}

function handleFilterPanelDocumentClick(e: MouseEvent): void {
  const elements = getFilterPanelElements();
  const target = e.target;
  if (!elements || !(target instanceof Node)) return;
  const { toggle, panel } = elements;

  if (toggle.contains(target)) {
    if (panel.hidden) {
      openFilterPanel(panel, toggle);
    } else {
      closeFilterPanel(panel, toggle);
    }
    return;
  }

  const optionEl = target instanceof Element
    ? target.closest("[data-assignee-option], [data-sort-option]") as HTMLElement | null
    : null;
  if (optionEl && panel.contains(optionEl)) {
    const isAssignee = optionEl.hasAttribute("data-assignee-option");
    const attr = isAssignee ? "data-assignee-option" : "data-sort-option";
    const value = optionEl.getAttribute(attr) || null;
    const label = optionEl.textContent?.trim() || "";

    if (isAssignee) {
      setAssigneeParam(value);
      panel.querySelectorAll("[data-assignee-option]").forEach((el) => el.classList.remove("is-active"));
    } else {
      setSortParam(value);
      panel.querySelectorAll("[data-sort-option]").forEach((el) => el.classList.remove("is-active"));
    }
    optionEl.classList.add("is-active");

    if (value) {
      showToast(t(isAssignee ? "board.filters.filteringOn" : "board.filters.sortedBy", { value: label }));
    }

    updateFilterToggleActiveState(toggle);
    closeFilterPanel(panel, toggle);

    reloadBoardFn?.(
      getSlug(),
      getTag(),
      getSearch() || null,
      getSprintIdFromUrl(),
      getAssigneeFromUrl(),
      getSortFromUrl(),
    ).catch((err: any) => {
      showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
    });
    return;
  }

  if (!panel.hidden && !panel.contains(target)) {
    closeFilterPanel(panel, toggle);
  }
}

function handleFilterPanelDocumentKeydown(e: KeyboardEvent): void {
  const elements = getFilterPanelElements();
  if (!elements) return;
  const { toggle, panel } = elements;
  if (e.key === "Escape" && !panel.hidden) {
    closeFilterPanel(panel, toggle);
    toggle.focus();
  }
}

function handleFilterPanelWindowResize(): void {
  const elements = getFilterPanelElements();
  if (!elements) return;
  const { toggle, panel } = elements;
  if (!panel.hidden) positionFilterPanel(panel, toggle);
}

function ensureFilterPanelDelegation(): void {
  if (filterPanelDelegationBound) return;
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
function bindFilterPanel(): void {
  const elements = getFilterPanelElements();
  if (!elements) return;
  const { toggle } = elements;
  updateFilterToggleActiveState(toggle);
  ensureFilterPanelDelegation();
}

function initMobileTagPagination(): void {
  const tagChipsEl = document.getElementById("tagChips");
  const chipsNav = document.getElementById("chipsNav");
  if (!tagChipsEl || !chipsNav) return;

  const isMobile = window.matchMedia(`(max-width: ${MOBILE_TAG_BREAKPOINT}px)`).matches;

  if (!mobileTagPaginationResizeBound) {
    mobileTagPaginationResizeBound = true;
    let resizeTimeout: ReturnType<typeof setTimeout> | null = null;
    window.addEventListener("resize", () => {
      if (resizeTimeout) clearTimeout(resizeTimeout);
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

  const chipEls = Array.from(tagChipsEl.querySelectorAll<HTMLElement>(".chip"));
  if (chipEls.length === 0) {
    return;
  }

  const rects = chipEls.map((el) => el.getBoundingClientRect());
  const rowTolerance = 2;
  const rows: number[] = [];
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
    if (idx >= 0) mobileTagPageBoundaries.push(idx);
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

  const prevBtn = chipsNav.querySelector(".chips-nav__prev") as HTMLButtonElement | null;
  const nextBtn = chipsNav.querySelector(".chips-nav__next") as HTMLButtonElement | null;
  prevBtn?.replaceWith(prevBtn.cloneNode(true));
  nextBtn?.replaceWith(nextBtn.cloneNode(true));
  const newPrev = chipsNav.querySelector(".chips-nav__prev") as HTMLButtonElement | null;
  const newNext = chipsNav.querySelector(".chips-nav__next") as HTMLButtonElement | null;
  if (newPrev) newPrev.disabled = mobileTagPage === 0;
  if (newNext) newNext.disabled = mobileTagPage === numPages - 1;

  newPrev?.addEventListener("click", () => {
    if (mobileTagPage <= 0) return;
    mobileTagPage--;
    const s = mobileTagPageBoundaries[mobileTagPage];
    const e = mobileTagPageBoundaries[mobileTagPage + 1];
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
    if (newPrev) newPrev.disabled = mobileTagPage === 0;
    if (newNext) newNext.disabled = mobileTagPage === numPages - 1;
  });
  newNext?.addEventListener("click", () => {
    if (mobileTagPage >= numPages - 1) return;
    mobileTagPage++;
    const s = mobileTagPageBoundaries[mobileTagPage];
    const e = mobileTagPageBoundaries[mobileTagPage + 1];
    tagChipsEl.innerHTML = buildChipsHTML(lastDisplayChipData.slice(s, e));
    if (newPrev) newPrev.disabled = mobileTagPage === 0;
    if (newNext) newNext.disabled = mobileTagPage === numPages - 1;
  });

  attachChipsDelegatedHandler();
}

export function computeBoardChipsRender(board: Board, tag: string, sprintId: string | null): { chipsHTML: string; chipsUnchanged: boolean } {
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

export function bindBoardFilterUi(args: {
  reloadBoard: ReloadBoardFn;
  showError: (message: string) => void;
}): void {
  reloadBoardFn = args.reloadBoard;
  showErrorFn = args.showError;
  attachChipsDelegatedHandler();
  initMobileTagPagination();
  bindSearchInput();
  bindFilterPanel();
}

export function resetBoardFilterUiState(): void {
  lastDisplayChipData = [];
  lastRenderedChipsHTML = "";
  mobileTagPage = 0;
  mobileTagPageBoundaries = [];
}

export function clearSprintChipDataIfSlugChanged(slug: string): void {
  if (slug !== lastSprintsDataSlug) {
    lastSprintsData = null;
  }
}

export function hasSprintChipDataForSlug(slug: string): boolean {
  return lastSprintsDataSlug === slug;
}

export function setSprintChipDataForSlug(slug: string, data: SprintChipData | null): void {
  lastSprintsDataSlug = slug;
  lastSprintsData = data;
}

export function clearSprintChipData(): void {
  lastSprintsData = null;
  lastSprintsDataSlug = null;
}

export function updateChipsOnly(sprintId: string | null): void {
  const board = getBoard();
  if (!board) return;
  const { chipsHTML, chipsUnchanged } = computeBoardChipsRender(board, getTag() || "", sprintId ?? null);
  if (chipsUnchanged) return;
  const tagChipsEl = document.getElementById("tagChips");
  if (tagChipsEl) {
    tagChipsEl.innerHTML = chipsHTML;
    bindBoardFilterUi({
      reloadBoard: reloadBoardFn ?? (async () => {}),
      showError: showErrorFn ?? (() => {}),
    });
  }
}

export function notifySprintStateChanged(sprintId: number | string, newState: 'ACTIVE' | 'CLOSED'): void {
  if (!lastSprintsData || getSlug() !== lastSprintsDataSlug) return;
  const id = Number(sprintId);
  const sprint = lastSprintsData.sprints.find((s) => s.id === id);
  if (!sprint) return;
  if (sprint.state === newState) return;
  sprint.state = newState;
  updateChipsOnly(getSprintIdFromUrl());
}

export function ensureSprintSubscription(): void {
  if (sprintEventSubscribed) return;
  sprintEventSubscribed = true;
  on("sprint-updated", (payload: { sprintId?: number | string; state?: string } | undefined) => {
    if (payload && payload.sprintId != null && (payload.state === "ACTIVE" || payload.state === "CLOSED")) {
      notifySprintStateChanged(payload.sprintId, payload.state);
    }
  });
}
