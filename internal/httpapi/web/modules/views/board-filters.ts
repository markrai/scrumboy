import { on } from '../events.js';
import { apiErrorMessage, t } from '../i18n/index.js';
import { normalizeBoardTodoSort, setBoardTodoSortPreference } from '../core/board-sort-preferences.js';
import { getBoardFilterLayoutPreference } from '../core/board-filter-layout-preferences.js';
import {
  getAssigneeFromUrl,
  getBoard,
  getPriorityFromUrl,
  getSlug,
  getSortFromUrl,
  getSprintIdFromUrl,
  getTagColors,
  getUser,
} from '../state/selectors.js';
import { getTagsFromUrl, MAX_BOARD_TAG_FILTERS, setTagParams } from '../state/board-filter-url.js';
import { Board, Tag } from '../types.js';
import { boardSprintsEnabled } from '../sprints.js';
import { escapeHTML, sanitizeHexColor, showToast } from '../utils.js';
import {
  buildSprintFilterSectionHtml,
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
let searchTimeout: ReturnType<typeof setTimeout> | null = null;
let omniCandidateResizeObserver: ResizeObserver | null = null;
let omniCandidateWindowResizeBound = false;

const MOBILE_TAG_BREAKPOINT = 767;
const MOBILE_TAG_ROWS_PER_PAGE = 2;
const FILTER_BOUND_FLAG = Symbol('boardFiltersBound');

type ReloadBoardFn = (slug: string | null, tags: readonly string[], search: string | null, sprintId: string | null, assignee: string | null, sort: string | null, priority?: string | null) => Promise<void>;

let reloadBoardFn: ReloadBoardFn | null = null;
let showErrorFn: ((message: string) => void) | null = null;

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

function setPriorityParam(priority: string | null): void {
  const url = new URL(window.location.href);
  if (priority) url.searchParams.set("priority", priority);
  else url.searchParams.delete("priority");
  history.replaceState({}, "", url.pathname + url.search);
}

function urlFilter(name: string): string | null {
  return new URL(window.location.href).searchParams.get(name);
}

function reloadBoardWithCurrentFilters(): void {
  if (!reloadBoardFn) return;
  reloadBoardFn(
    getSlug(),
    getTagsFromUrl(),
    urlFilter("search"),
    getSprintIdFromUrl(),
    getAssigneeFromUrl(),
    getSortFromUrl(),
    getPriorityFromUrl(),
  ).catch((err: any) => {
    showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
  });
}

export function cancelPendingSearchReload(): void {
  if (searchTimeout !== null) {
    clearTimeout(searchTimeout);
    searchTimeout = null;
  }
}

export function matchOmniTags(query: string, tags: readonly Tag[], appliedTags: readonly string[] = []): Tag[] {
  const needle = query.trim().toLowerCase();
  if (!needle || appliedTags.length >= MAX_BOARD_TAG_FILTERS) return [];
  const applied = new Set(appliedTags.map((tag) => tag.toLocaleLowerCase()));
  return tags
    .filter((tag) => tag.count > 0 && !applied.has(tag.name.toLocaleLowerCase()))
    .map((tag) => {
      const name = tag.name.toLowerCase();
      const rank = name === needle ? 0 : name.startsWith(needle) ? 1 : name.includes(needle) ? 2 : 3;
      return { tag, rank };
    })
    .filter((entry) => entry.rank < 3)
    .sort((a, b) => {
      if (a.rank !== b.rank) return a.rank - b.rank;
      const left = a.tag.name.toLowerCase();
      const right = b.tag.name.toLowerCase();
      return left < right ? -1 : left > right ? 1 : a.tag.name < b.tag.name ? -1 : a.tag.name > b.tag.name ? 1 : 0;
    })
    .map((entry) => entry.tag);
}

function browseTimestamp(tag: Tag): number {
  if (!tag.lastActiveAt) return Number.NEGATIVE_INFINITY;
  const parsed = Date.parse(tag.lastActiveAt);
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

export function rankBrowseOmniTags(tags: readonly Tag[], pinnedTags: readonly string[] = []): Tag[] {
  if (pinnedTags.length >= MAX_BOARD_TAG_FILTERS) return [];
  const pinned = new Set(pinnedTags.map((tag) => tag.toLocaleLowerCase()));
  return tags
    .filter((tag) => tag.count > 0 && !pinned.has(tag.name.toLocaleLowerCase()))
    .sort((left, right) => {
      const leftTime = browseTimestamp(left);
      const rightTime = browseTimestamp(right);
      if (leftTime !== rightTime) return leftTime > rightTime ? -1 : 1;
      if (left.count !== right.count) return right.count - left.count;
      const leftName = left.name.toLowerCase();
      const rightName = right.name.toLowerCase();
      return leftName < rightName ? -1 : leftName > rightName ? 1 : left.name < right.name ? -1 : left.name > right.name ? 1 : 0;
    });
}

function setOmniChevronInert(button: HTMLButtonElement | null, inert: boolean): void {
  if (!button) return;
  button.disabled = inert;
  button.setAttribute('aria-hidden', inert ? 'true' : 'false');
}

export function updateOmniCandidateChevronState(): void {
  const viewport = document.getElementById('omniCandidateViewport');
  const previous = document.getElementById('omniCandidatePrev') as HTMLButtonElement | null;
  const next = document.getElementById('omniCandidateNext') as HTMLButtonElement | null;
  if (!viewport) return;
  const overflows = viewport.scrollWidth > viewport.clientWidth + 1;
  const atStart = !overflows || viewport.scrollLeft <= 1;
  const atEnd = !overflows || viewport.scrollLeft + viewport.clientWidth >= viewport.scrollWidth - 1;
  setOmniChevronInert(previous, atStart);
  setOmniChevronInert(next, atEnd);
}

function resetOmniCandidateScroll(): void {
  const viewport = document.getElementById('omniCandidateViewport');
  if (viewport) viewport.scrollLeft = 0;
  updateOmniCandidateChevronState();
}

function renderOmniTagPills(board: Board | null = getBoard()): void {
  const pinnedTags = document.getElementById('omniPinnedTags');
  const candidateViewport = document.getElementById('omniCandidateViewport');
  const candidateRegion = document.getElementById('omniCandidateRegion');
  const mobilePills = document.getElementById('omniMobileTagPills');
  const input = document.getElementById('searchInput') as HTMLInputElement | null;
  if (!pinnedTags || !candidateViewport || !candidateRegion || !mobilePills || !input || !board) return;
  const selectedTags = getTagsFromUrl();
  const selectedHTML = selectedTags.map((selectedTag) => {
    const selectedFromBoard = board.tags.find((tag) => tag.name.toLocaleLowerCase() === selectedTag.toLocaleLowerCase());
    const selectedColor = selectedFromBoard?.color || getTagColors()[selectedTag];
    const selectedSafeColor = sanitizeHexColor(selectedColor);
    const selectedStyle = selectedSafeColor
      ? ` style="border-color:${escapeHTML(selectedSafeColor)};background:${escapeHTML(selectedSafeColor)}20;color:${escapeHTML(selectedSafeColor)}"`
      : '';
    return `<span class="omni-tag-pill omni-tag-pill--applied"${selectedStyle}>
        <span>${escapeHTML(selectedTag)}</span>
        <button type="button" class="omni-tag-pill__clear" data-omni-clear-tag="${escapeHTML(selectedTag)}" aria-label="${escapeHTML(t('board.filters.clearTag', { name: selectedTag }))}">×</button>
      </span>`;
  }).join('');
  const browseMode = input.value.trim() === '';
  const candidates = browseMode
    ? rankBrowseOmniTags(board.tags, selectedTags)
    : matchOmniTags(input.value, board.tags, selectedTags);
  const candidateHTML = (items: readonly Tag[], isBrowse: boolean) => items.map((tag) => {
      const color = sanitizeHexColor(tag.color || getTagColors()[tag.name]);
      const style = color
        ? isBrowse
          ? ` style="border-color:${escapeHTML(color)}66;background:${escapeHTML(color)}0d;color:${escapeHTML(color)}"`
          : ` style="border-color:${escapeHTML(color)};background:${escapeHTML(color)}20;color:${escapeHTML(color)}"`
        : '';
      const candidateClass = isBrowse ? 'omni-tag-pill--browse' : 'omni-tag-pill--suggestion';
      return `<button type="button" class="omni-tag-pill ${candidateClass}" data-omni-tag="${escapeHTML(tag.name)}"${style}>${escapeHTML(tag.name)}</button>`;
    }).join('');
  const suggestionsHTML = candidateHTML(candidates, browseMode);
  const mobileSuggestionsHTML = browseMode ? '' : candidateHTML(matchOmniTags(input.value, board.tags, selectedTags), false);
  pinnedTags.innerHTML = selectedHTML;
  candidateViewport.innerHTML = suggestionsHTML;
  mobilePills.innerHTML = selectedHTML + mobileSuggestionsHTML;
  mobilePills.scrollLeft = 0;
  candidateRegion.classList.toggle('omni-candidate-region--after-pins', selectedTags.length > 0);
  resetOmniCandidateScroll();
}

export function updateOmniTagPills(board: Board | null = getBoard()): void {
  if (getBoardFilterLayoutPreference() !== 'omni') return;
  renderOmniTagPills(board);
}

function bindOmniTagPills(): void {
  const bar = document.querySelector<HTMLElement>('.filters--omni .omni-bar');
  const viewport = document.getElementById('omniCandidateViewport');
  const previous = document.getElementById('omniCandidatePrev') as HTMLButtonElement | null;
  const next = document.getElementById('omniCandidateNext') as HTMLButtonElement | null;
  if (!bar || !viewport) return;
  bar.onkeydown = (event: KeyboardEvent) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    const target = event.target instanceof Element
      ? event.target.closest<HTMLButtonElement>('[data-omni-tag], [data-omni-clear-tag]')
      : null;
    if (!target) return;
    event.preventDefault();
    target.click();
  };
  bar.onclick = (event: MouseEvent) => {
    const target = event.target instanceof Element ? event.target.closest('[data-omni-tag], [data-omni-clear-tag]') as HTMLElement | null : null;
    if (!target) return;
    const input = document.getElementById('searchInput') as HTMLInputElement | null;
    if (target.hasAttribute('data-omni-clear-tag')) {
      const clearTag = target.getAttribute('data-omni-clear-tag') ?? '';
      setTagParams(getTagsFromUrl().filter((tag) => tag !== clearTag));
    } else {
      cancelPendingSearchReload();
      const nextTag = target.getAttribute('data-omni-tag') ?? '';
      if (input) input.value = '';
      document.getElementById('searchClear')?.remove();
      setSearchParam('');
      setTagParams([...getTagsFromUrl(), nextTag]);
    }
    renderOmniTagPills();
    input?.focus();
    reloadBoardWithCurrentFilters();
  };

  const pageCandidates = (direction: -1 | 1) => {
    const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
    viewport.scrollBy({ left: direction * viewport.clientWidth, behavior: reduceMotion ? 'auto' : 'smooth' });
  };
  if (previous) previous.onclick = () => pageCandidates(-1);
  if (next) next.onclick = () => pageCandidates(1);
  viewport.onscroll = updateOmniCandidateChevronState;
  omniCandidateResizeObserver?.disconnect();
  if (typeof ResizeObserver !== 'undefined') {
    omniCandidateResizeObserver = new ResizeObserver(updateOmniCandidateChevronState);
    omniCandidateResizeObserver.observe(viewport);
  }
  if (!omniCandidateWindowResizeBound) {
    window.addEventListener('resize', updateOmniCandidateChevronState);
    omniCandidateWindowResizeBound = true;
  }
  renderOmniTagPills();
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
        setTagParams(nextTag ? [nextTag] : []);
      } else {
        setTagParams(nextTag ? [nextTag] : []);
        setSprintParam(null);
      }
      reloadBoardWithCurrentFilters();
    } else if (chip.hasAttribute("data-sprint-clear")) {
      if (additive) {
        setSprintParam(null);
      } else {
        setSprintParam(null);
        setTagParams([]);
      }
      reloadBoardWithCurrentFilters();
    } else if (chip.hasAttribute("data-sprint-id")) {
      const nextSprint = chip.getAttribute("data-sprint-id") ?? "";
      if (additive) {
        setSprintParam(nextSprint);
      } else {
        setSprintParam(nextSprint);
        setTagParams([]);
      }
      reloadBoardWithCurrentFilters();
    }
  };
}

function bindSearchInput(): void {
  const searchInput = document.getElementById("searchInput") as HTMLInputElement | null;
  if (!searchInput || (searchInput as any)[FILTER_BOUND_FLAG]) return;

  const handleClearClick = () => {
    cancelPendingSearchReload();
    searchInput.value = "";
    setSearchParam("");
    if (!reloadBoardFn) return;
    reloadBoardFn(getSlug(), getTagsFromUrl(), null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err: any) => {
      showErrorFn?.(apiErrorMessage(err, { fallbackKey: "board.refreshFailed" }));
    });
    updateClearButton();
    renderOmniTagPills();
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
    renderOmniTagPills();
    cancelPendingSearchReload();
    searchTimeout = setTimeout(() => {
      searchTimeout = null;
      const trimmedValue = value.trim();
      setSearchParam(trimmedValue);
      if (!reloadBoardFn) return;
      reloadBoardFn(getSlug(), getTagsFromUrl(), trimmedValue || null, getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl(), getPriorityFromUrl()).catch((err: any) => {
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
  const active = isBoardFilterActive(
    getAssigneeFromUrl(),
    getSortFromUrl(),
    getPriorityFromUrl(),
    getSprintIdFromUrl(),
    getBoardFilterLayoutPreference(),
  );
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
    ? target.closest("[data-assignee-option], [data-sort-option], [data-priority-option], [data-sprint-option]") as HTMLElement | null
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
    } else if (kind === "sort") {
      setSortParam(value);
      if (getUser()) {
        setBoardTodoSortPreference(normalizeBoardTodoSort(value));
      }
      panel.querySelectorAll("[data-sort-option]").forEach((el) => el.classList.remove("is-active"));
    } else if (kind === "priority") {
      setPriorityParam(value);
      panel.querySelectorAll("[data-priority-option]").forEach((el) => el.classList.remove("is-active"));
    } else {
      setSprintParam(value);
      panel.querySelectorAll("[data-sprint-option]").forEach((el) => el.classList.remove("is-active"));
    }
    optionEl.classList.add("is-active");

    if (value) {
      showToast(t(kind === "sort" ? "board.filters.sortedBy" : "board.filters.filteringOn", { value: label }));
    }

    updateFilterToggleActiveState(toggle);
    closeFilterPanel(panel, toggle);

    reloadBoardFn?.(
      getSlug(),
      getTagsFromUrl(),
      urlFilter('search'),
      getSprintIdFromUrl(),
      getAssigneeFromUrl(),
      getSortFromUrl(),
      getPriorityFromUrl(),
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

export function computeBoardChipsRender(board: Board, tags: readonly string[], sprintId: string | null): { chipsHTML: string; chipsUnchanged: boolean } {
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

export function bindBoardFilterUi(args: {
  reloadBoard: ReloadBoardFn;
  showError: (message: string) => void;
}): void {
  reloadBoardFn = args.reloadBoard;
  showErrorFn = args.showError;
  attachChipsDelegatedHandler();
  initMobileTagPagination();
  bindSearchInput();
  bindOmniTagPills();
  bindFilterPanel();
}

export function resetBoardFilterUiState(): void {
  lastDisplayChipData = [];
  lastRenderedChipsHTML = "";
  mobileTagPage = 0;
  mobileTagPageBoundaries = [];
  resetOmniCandidateScroll();
  omniCandidateResizeObserver?.disconnect();
  omniCandidateResizeObserver = null;
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

export function getSprintChipDataForSlug(slug: string | null): SprintChipData | null {
  return slug && slug === lastSprintsDataSlug ? lastSprintsData : null;
}

export function clearSprintChipData(): void {
  lastSprintsData = null;
  lastSprintsDataSlug = null;
}

export function updateChipsOnly(sprintId: string | null): void {
  const board = getBoard();
  if (!board) return;
  if (getBoardFilterLayoutPreference() === 'omni') {
    const section = document.querySelector('[data-sprint-filter-section]');
    if (section && boardSprintsEnabled(board)) {
      section.outerHTML = buildSprintFilterSectionHtml(sprintId, lastSprintsData);
    }
    const toggle = document.getElementById('searchFilterToggle');
    if (toggle) updateFilterToggleActiveState(toggle);
    renderOmniTagPills(board);
    return;
  }
  const { chipsHTML, chipsUnchanged } = computeBoardChipsRender(board, getTagsFromUrl(), sprintId ?? null);
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
