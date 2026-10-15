/**
 * Pure HTML builders for the public read-only board. They reuse the member
 * board's layout classes (topbar, Omni tag row, columns, cards, mobile tabs)
 * so the page looks like Scrumboy, but emit no mutation controls, drag
 * handles, selection state, member/assignee data, or global identifiers.
 * Cards are keyed only by project-local IDs (data-public-local-id) so no member
 * delegation or drag/drop code that targets data-todo-id can match them.
 */

import { escapeHTML, renderUserAvatar, sanitizeHexColor } from '../utils.js';
import { t } from '../i18n/index.js';
import {
  buildFilterPanelSectionHtml,
  buildFilterPanelShellHtml,
  buildPriorityFilterOptions,
  renderFilterOptionsHtml,
} from './filter-panel-markup.js';
import {
  buildOmniAppliedPillHtml,
  buildOmniBarHtml,
  buildOmniRowContent,
  compareOmniTagNames,
  matchOmniTagCandidates,
  rankOmniBrowseCandidates,
  type OmniRowContent,
  type OmniRowIds,
} from './omni-tags.js';
import { renderStoryCardContentHtml, type StoryCardContentInput } from './story-card-content.js';
import type { User } from '../types.js';
import { PUBLIC_MAX_TAG_FILTERS } from '../public-board-api.js';
import type {
  PublicTagSummary,
  PublicBoardQuery,
  PublicBoardSnapshot,
  PublicLaneMeta,
  PublicPriority,
  PublicSprint,
  PublicTodo,
  PublicWorkflowColumn,
} from '../public-board-api.js';

export type PublicLaneState = PublicLaneMeta & { readonly loading: boolean; readonly error: boolean };

export type PublicBoardViewModel = {
  readonly snapshot: PublicBoardSnapshot;
  readonly columns: Readonly<Record<string, readonly PublicTodo[]>>;
  readonly lanes: Readonly<Record<string, PublicLaneState>>;
  readonly query: PublicBoardQuery;
  readonly sprints: readonly PublicSprint[];
  readonly activeMobileTab: string;
  readonly isMobile: boolean;
  readonly user: User | null;
  /** Logged-out visitor in Full Mode: offer the existing sign-in flow. */
  readonly canSignIn: boolean;
};

function attrI18n(kind: 'text' | 'aria-label' | 'placeholder' | 'title', key: string): string {
  return ` data-i18n-${kind}="${escapeHTML(key)}"`;
}

export function publicPriorityMap(priorities: readonly PublicPriority[]): Record<string, PublicPriority> {
  const out: Record<string, PublicPriority> = {};
  for (const priority of priorities) out[priority.key] = priority;
  return out;
}

export function showPublicPoints(snapshot: PublicBoardSnapshot): boolean {
  return snapshot.project.estimationMode === 'MODIFIED_FIBONACCI';
}

/**
 * Maps only fields of the public DTO onto the shared story-card model. The
 * public projection has no due date or assignee, so neither is rendered.
 */
export function toPublicStoryCardInput(
  todo: PublicTodo,
  priorities: Record<string, PublicPriority>,
  showPoints: boolean,
): StoryCardContentInput {
  const priority = todo.priorityKey ? priorities[todo.priorityKey] : undefined;
  return {
    localId: todo.localId,
    title: todo.title,
    tags: todo.tags.map((tag) => ({ name: tag.name, color: tag.color })),
    priority: priority ? { name: priority.name, color: priority.color } : null,
    points: showPoints && todo.estimationPoints != null ? todo.estimationPoints : null,
    due: null,
  };
}

export function renderPublicTodoCard(
  todo: PublicTodo,
  column: PublicWorkflowColumn,
  priorities: Record<string, PublicPriority>,
  showPoints: boolean,
): string {
  // Tag accents stay on border/tint; text keeps the theme foreground for contrast.
  const content = renderStoryCardContentHtml(toPublicStoryCardInput(todo, priorities, showPoints), { tagTone: 'theme', tooltips: false });
  const border = sanitizeHexColor(column.color ?? undefined);
  return `
    <button class="card card--public" type="button" data-public-local-id="${escapeHTML(String(todo.localId))}"${border ? ` style="border-color:${border}"` : ''}>${content}</button>`;
}

function laneLoadMoreHtml(key: string, lane: PublicLaneState): string {
  const dk = escapeHTML(key);
  if (lane.loading) {
    return `<div class="col__load-more visible" data-public-load-more-state="loading" role="status"><span class="muted"${attrI18n('text', 'publicBoard.loading')}>${escapeHTML(t('publicBoard.loading'))}</span></div>`;
  }
  if (lane.error) {
    return `<div class="col__load-more visible"><button class="btn btn--ghost btn--small" type="button" data-public-load-more="${dk}"${attrI18n('text', 'board.loadMoreFailed')}>${escapeHTML(t('board.loadMoreFailed'))}</button></div>`;
  }
  if (!lane.hasMore || !lane.nextCursor) return '';
  const label = escapeHTML(t('board.loadMore'));
  return `<div class="col__load-more visible"><button class="btn btn--ghost btn--small col__load-more--desktop" type="button" data-public-load-more="${dk}"${attrI18n('text', 'board.loadMore')}>${label}</button><button class="col__load-more--mobile public-board__load-more-mobile" type="button" data-public-load-more="${dk}" aria-label="${label}"${attrI18n('aria-label', 'board.loadMore')}>▼</button></div>`;
}

export function buildPublicColumnsHtml(view: PublicBoardViewModel): string {
  const priorities = publicPriorityMap(view.snapshot.priorities);
  const showPoints = showPublicPoints(view.snapshot);
  return view.snapshot.workflow
    .map((column) => {
      const todos = view.columns[column.key] ?? [];
      const lane = view.lanes[column.key] ?? { hasMore: false, nextCursor: null, totalCount: todos.length, loading: false, error: false };
      const tint = sanitizeHexColor(column.color ?? undefined);
      const dk = escapeHTML(column.key);
      const cards = todos.length > 0
        ? todos.map((todo) => renderPublicTodoCard(todo, column, priorities, showPoints)).join('')
        : `<div class="muted public-board__empty-lane"${attrI18n('text', 'publicBoard.emptyLane')}>${escapeHTML(t('publicBoard.emptyLane'))}</div>`;
      return `
        <section class="col${view.activeMobileTab === column.key ? ' col--mobile-active' : ''}${tint ? ' col--lane-tint' : ''}" data-public-column="${dk}"${tint ? ` style="--lane-accent:${tint};"` : ''} aria-label="${escapeHTML(column.name)}">
          <div class="col__head"${tint ? ` style="background:${tint};"` : ''}>
            <span class="col__title">${escapeHTML(column.name)}</span>
            <span class="col__count">${lane.totalCount}</span>
          </div>
          <div class="col__list" data-public-list="${dk}">${cards}</div>
          ${laneLoadMoreHtml(column.key, lane)}
        </section>`;
    })
    .join('');
}

export function buildPublicMobileTabsHtml(view: PublicBoardViewModel): string {
  return view.snapshot.workflow
    .map((column) => {
      const safe = sanitizeHexColor(column.color ?? undefined);
      const style = safe ? ` style="--lane-color:${safe};--lane-shadow:${safe}d9;background:${safe};color:#ffffff;"` : '';
      const active = view.activeMobileTab === column.key;
      const count = view.lanes[column.key]?.totalCount ?? 0;
      return `<button class="mobile-tab${active ? ' mobile-tab--active' : ''}" type="button" role="tab" aria-selected="${active ? 'true' : 'false'}" data-public-tab="${escapeHTML(column.key)}"${style}><span class="mobile-tab__text">${escapeHTML(column.name)} ${count}</span></button>`;
    })
    .join('');
}

/** Public Omni row IDs and attributes, distinct from the member board's omni* IDs and data-omni-* hooks. */
export const PUBLIC_OMNI_IDS: OmniRowIds = Object.freeze({
  pinned: 'publicOmniPinnedTags',
  region: 'publicOmniCandidateRegion',
  previous: 'publicOmniCandidatePrev',
  viewport: 'publicOmniCandidateViewport',
  next: 'publicOmniCandidateNext',
  mobile: 'publicOmniMobileTagPills',
});
export const PUBLIC_OMNI_TAG_ATTR = 'data-public-omni-tag';
export const PUBLIC_OMNI_CLEAR_ATTR = 'data-public-omni-clear-tag';
/** Clear button of the pinned pill shown for a sprint-number deep link. */
export const PUBLIC_CLEAR_SPRINT_ATTR = 'data-public-clear-sprint';

/**
 * Stable Omni shell for the public tag filter. Rendered once per page; only its
 * pinned, candidate, and mobile regions are updated afterwards.
 */
export function buildPublicOmniRowHtml(): string {
  return `
    <div class="filters filters--omni public-board-omni" data-public-omni>
      ${buildOmniBarHtml({ ids: PUBLIC_OMNI_IDS, previousLabel: t('board.filters.previous'), nextLabel: t('board.filters.next') })}
    </div>`;
}

/** Public Browse order: most active first, then name. The public API exposes no recency. */
export function comparePublicBrowseTags(left: PublicTagSummary, right: PublicTagSummary): number {
  if (left.activeCount !== right.activeCount) return right.activeCount - left.activeCount;
  return compareOmniTagNames(left.name, right.name);
}

/**
 * Pinned pills for every selected tag (including tags no longer in the
 * vocabulary, so they stay removable), plus Browse candidates for an empty
 * input or matches for the typed input. Only tags with active stories are
 * offered. Text keeps the theme color; tag colors tint border and background.
 */
export function buildPublicOmniContent(args: {
  readonly tags: readonly PublicTagSummary[];
  readonly selected: readonly string[];
  readonly input: string;
  /** Label of an active sprint-number deep link; pinned first so it stays removable. */
  readonly sprintLabel?: string | null;
}): OmniRowContent {
  const byName = new Map(args.tags.map((tag) => [tag.name.toLocaleLowerCase(), tag]));
  const selectable = args.tags.filter((tag) => tag.activeCount > 0);
  const browse = args.input.trim() === '';
  const candidates = browse
    ? rankOmniBrowseCandidates(selectable, args.selected, PUBLIC_MAX_TAG_FILTERS, comparePublicBrowseTags)
    : matchOmniTagCandidates(args.input, selectable, args.selected, PUBLIC_MAX_TAG_FILTERS);
  const content = buildOmniRowContent({
    applied: args.selected.map((name) => ({ name, color: byName.get(name.toLocaleLowerCase())?.color ?? null })),
    candidates,
    browse,
    attrs: { tag: PUBLIC_OMNI_TAG_ATTR, clear: PUBLIC_OMNI_CLEAR_ATTR },
    tone: 'theme',
    clearLabel: (name) => t('board.filters.clearTag', { name }),
  });
  if (!args.sprintLabel) return content;
  // Clearing returns the board to all sprints; the attribute value is not read.
  const sprintPill = buildOmniAppliedPillHtml(
    { name: `${t('board.filters.sprint')}: ${args.sprintLabel}`, color: null },
    { clearAttr: PUBLIC_CLEAR_SPRINT_ATTR, clearLabel: t('board.filters.allSprints'), tone: 'theme' },
  );
  return {
    ...content,
    pinnedHtml: sprintPill + content.pinnedHtml,
    mobileHtml: sprintPill + content.mobileHtml,
    hasPins: true,
  };
}

/** Public filter popover ids and option attributes, distinct from the member panel's. */
export const PUBLIC_FILTER_TOGGLE_ID = 'publicFilterToggle';
export const PUBLIC_FILTER_PANEL_ID = 'publicFilterPanel';
export const PUBLIC_PRIORITY_OPTION_ATTR = 'data-public-priority-option';

/**
 * The public filter popover offers only Priority: All plus named priority
 * tiers. Never Sprint, Scheduled/Unscheduled, No Priority, assignee, or sort.
 * A sprint-number deep link is shown as a pinned Omni pill instead.
 * Returns '' when the board has no priority tiers.
 */
export function buildPublicFilterSectionsHtml(view: Pick<PublicBoardViewModel, 'snapshot' | 'query'>): string {
  if (view.snapshot.priorities.length === 0) return '';
  const options = buildPriorityFilterOptions(view.snapshot.priorities, { includeNoPriority: false });
  return buildFilterPanelSectionHtml({
    labelKey: 'board.filters.priority',
    optionsHtml: renderFilterOptionsHtml(options, view.query.priority ?? '', { attr: PUBLIC_PRIORITY_OPTION_ATTR, ariaPressed: true }),
    sectionAttr: 'data-public-priority-section',
  });
}

/** Drives the toggle's active pulse: a priority filter narrows the board. */
export function isPublicPanelFilterActive(query: PublicBoardQuery): boolean {
  return !!query.priority;
}

export function buildPublicTopbarHtml(view: PublicBoardViewModel): string {
  const placeholderKey = view.isMobile ? 'board.search.placeholder.mobile' : 'board.search.placeholder.desktop';
  const clearLabel = escapeHTML(t('board.actions.clearSearch'));
  const badgeAria = escapeHTML(t('publicBoard.badgeAria'));
  const sectionsHtml = buildPublicFilterSectionsHtml(view);
  return `
    <div class="topbar">
      <div class="brand public-board__project-name">${escapeHTML(view.snapshot.project.name)}</div>
      <span class="public-board-badge" role="note" aria-label="${badgeAria}"${attrI18n('aria-label', 'publicBoard.badgeAria')}><span aria-hidden="true"${attrI18n('text', 'publicBoard.badge')}>${escapeHTML(t('publicBoard.badge'))}</span></span>
      <div class="spacer"></div>
      <div class="search-input-wrapper">
        <input type="search" id="publicSearchInput" class="search-input" autocomplete="off" value="${escapeHTML(view.query.search)}" placeholder="${escapeHTML(t(placeholderKey))}" aria-label="${escapeHTML(t(placeholderKey))}"${attrI18n('placeholder', placeholderKey)}${attrI18n('aria-label', placeholderKey)} />
        <button class="search-clear" id="publicSearchClear" type="button" aria-label="${clearLabel}" title="${clearLabel}"${attrI18n('aria-label', 'board.actions.clearSearch')}${view.query.search ? '' : ' hidden'}>✕</button>
        ${buildFilterPanelShellHtml({
          toggleId: PUBLIC_FILTER_TOGGLE_ID,
          panelId: PUBLIC_FILTER_PANEL_ID,
          active: isPublicPanelFilterActive(view.query),
          sectionsHtml,
          toggleHidden: sectionsHtml === '',
        })}
      </div>
      ${view.canSignIn ? `<button class="btn btn--ghost public-board__sign-in" type="button" id="publicSignInBtn"${attrI18n('text', 'auth.signIn.title')}>${escapeHTML(t('auth.signIn.title'))}</button>` : ''}
      ${view.user ? renderUserAvatar(view.user) : ''}
    </div>`;
}

export function isPublicQueryActive(query: PublicBoardQuery): boolean {
  return !!query.search || query.tags.length > 0 || query.sprintNumber != null || !!query.priority;
}

export function buildPublicBoardHtml(view: PublicBoardViewModel): string {
  const total = view.snapshot.workflow.reduce((sum, column) => sum + (view.lanes[column.key]?.totalCount ?? 0), 0);
  const noResults = total === 0 && isPublicQueryActive(view.query)
    ? `<div class="empty public-board__no-results" role="status"><div class="empty__title"${attrI18n('text', 'publicBoard.empty')}>${escapeHTML(t('publicBoard.empty'))}</div></div>`
    : '';
  return `
    <div class="page page--public-board" data-public-board-page>
      ${buildPublicTopbarHtml(view)}
      <div class="container">
        ${buildPublicOmniRowHtml()}
        ${noResults}
        <div class="mobile-board-wrapper">
          <div class="mobile-tabs" id="publicMobileTabs" role="tablist">${buildPublicMobileTabsHtml(view)}</div>
          <div class="board" data-public-board>${buildPublicColumnsHtml(view)}</div>
        </div>
      </div>
    </div>`;
}

/**
 * Board-region replacement shown when a filter change could not be loaded.
 * Cards from the previous query are removed so they are never presented as
 * matching the newly selected filters.
 */
export function buildPublicFilterErrorHtml(): string {
  return `
    <div class="empty public-board__filter-error" role="alert" data-public-filter-error>
      <div class="empty__title"${attrI18n('text', 'publicBoard.loadFailed')}>${escapeHTML(t('publicBoard.loadFailed'))}</div>
      <button class="btn" type="button" data-public-retry${attrI18n('text', 'common.retry')}>${escapeHTML(t('common.retry'))}</button>
    </div>`;
}

/** Generic, content-free state used after revocation or authoritative not-found. */
export function buildPublicUnavailableHtml(): string {
  return `
    <div class="page page--public-board" data-public-board-page data-public-board-state="unavailable">
      <div class="topbar">
        <div class="brand"><img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" /></div>
        <div class="spacer"></div>
        <button class="btn" type="button" id="publicHomeBtn"${attrI18n('text', 'notFound.home')}>${escapeHTML(t('notFound.home'))}</button>
      </div>
      <div class="empty" role="status">
        <div class="empty__title"${attrI18n('text', 'publicBoard.unavailable.title')}>${escapeHTML(t('publicBoard.unavailable.title'))}</div>
        <p${attrI18n('text', 'publicBoard.unavailable.body')}>${escapeHTML(t('publicBoard.unavailable.body'))}</p>
      </div>
    </div>`;
}
