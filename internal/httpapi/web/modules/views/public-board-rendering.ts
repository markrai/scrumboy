/**
 * Pure HTML builders for the public read-only board. They reuse the member
 * board's layout classes (topbar, filters, chips, columns, cards, mobile tabs)
 * so the page looks like Scrumboy, but emit no mutation controls, drag
 * handles, selection state, member/assignee data, or global identifiers.
 * Cards are keyed only by project-local IDs (data-public-local-id) so no member
 * delegation or drag/drop code that targets data-todo-id can match them.
 */

import { escapeHTML, renderUserAvatar, sanitizeHexColor } from '../utils.js';
import { t } from '../i18n/index.js';
import type { User } from '../types.js';
import type {
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

function colorStyle(color: string | null, withText = false): string {
  const safe = sanitizeHexColor(color ?? undefined);
  if (!safe) return '';
  return ` style="border-color: ${safe}; background: ${safe}20;${withText ? ` color: ${safe};` : ''}"`;
}

export function publicPriorityMap(priorities: readonly PublicPriority[]): Record<string, PublicPriority> {
  const out: Record<string, PublicPriority> = {};
  for (const priority of priorities) out[priority.key] = priority;
  return out;
}

export function showPublicPoints(snapshot: PublicBoardSnapshot): boolean {
  return snapshot.project.estimationMode === 'MODIFIED_FIBONACCI';
}

export function renderPublicTodoCard(
  todo: PublicTodo,
  column: PublicWorkflowColumn,
  priorities: Record<string, PublicPriority>,
  showPoints: boolean,
): string {
  const tags = todo.tags
    .map((tag) => `<span class="tag"${colorStyle(tag.color, true)}>${escapeHTML(tag.name)}</span>`)
    .join('');
  const priority = todo.priorityKey ? priorities[todo.priorityKey] : undefined;
  const priorityHTML = priority
    ? `<span class="card__priority"${colorStyle(priority.color, true)} aria-label="${escapeHTML(t('todo.fields.priority'))}: ${escapeHTML(priority.name)}">${escapeHTML(priority.name)}</span>`
    : '';
  const pointsHTML = showPoints && todo.estimationPoints != null
    ? `<span class="card__points" aria-label="${escapeHTML(t('todo.fields.estimationPoints'))}"${attrI18n('aria-label', 'todo.fields.estimationPoints')}>${todo.estimationPoints}</span>`
    : '';
  const footer = priorityHTML + pointsHTML;
  const border = sanitizeHexColor(column.color ?? undefined);
  return `
    <button class="card card--public" type="button" data-public-local-id="${todo.localId}"${border ? ` style="border-color:${border}"` : ''}>
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${todo.localId}</span>
          <span class="card__title">${escapeHTML(todo.title)}</span>
        </div>
        ${tags || footer ? `<div class="card__tags"><span class="card__tags-list">${tags}</span><span class="card__badges">${footer}</span></div>` : ''}
      </div>
    </button>`;
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

function sprintLabel(sprint: PublicSprint, duplicateName: boolean): string {
  return duplicateName ? `${sprint.name} (${sprint.number})` : sprint.name;
}

export function buildPublicFiltersHtml(view: PublicBoardViewModel): string {
  const activeTags = new Set(view.query.tags.map((tag) => tag.toLocaleLowerCase()));
  const allLabel = escapeHTML(t('board.filters.all'));
  const chips = [
    `<button class="chip${view.query.tags.length === 0 ? ' chip--active' : ''}" type="button" data-public-tag="" aria-pressed="${view.query.tags.length === 0 ? 'true' : 'false'}"${attrI18n('text', 'board.filters.all')}>${allLabel}</button>`,
    ...view.snapshot.tags.map((tag) => {
      const active = activeTags.has(tag.name.toLocaleLowerCase());
      return `<button class="chip${active ? ' chip--active' : ''}" type="button" data-public-tag="${escapeHTML(tag.name)}" aria-pressed="${active ? 'true' : 'false'}"${colorStyle(tag.color)}>${escapeHTML(tag.name)}</button>`;
    }),
  ].join('');

  let sprintSelect = '';
  if (view.snapshot.project.sprintsEnabled && view.sprints.length > 0) {
    const nameCount = new Map<string, number>();
    for (const sprint of view.sprints) nameCount.set(sprint.name, (nameCount.get(sprint.name) ?? 0) + 1);
    const options = view.sprints
      .map((sprint) => `<option value="${sprint.number}"${view.query.sprintNumber === sprint.number ? ' selected' : ''}>${escapeHTML(sprintLabel(sprint, (nameCount.get(sprint.name) ?? 0) > 1))}</option>`)
      .join('');
    sprintSelect = `<label class="public-board-filter"><span class="public-board-sr-only"${attrI18n('text', 'board.filters.sprint')}>${escapeHTML(t('board.filters.sprint'))}</span><select class="search-input public-board-filter__select" id="publicSprintFilter"><option value=""${attrI18n('text', 'board.filters.allSprints')}>${escapeHTML(t('board.filters.allSprints'))}</option>${options}</select></label>`;
  }

  let prioritySelect = '';
  if (view.snapshot.priorities.length > 0) {
    const options = view.snapshot.priorities
      .map((priority) => `<option value="${escapeHTML(priority.key)}"${view.query.priority === priority.key ? ' selected' : ''}>${escapeHTML(priority.name)}</option>`)
      .join('');
    prioritySelect = `<label class="public-board-filter"><span class="public-board-sr-only"${attrI18n('text', 'board.filters.priority')}>${escapeHTML(t('board.filters.priority'))}</span><select class="search-input public-board-filter__select" id="publicPriorityFilter"><option value=""${attrI18n('text', 'board.filters.allPriorities')}>${escapeHTML(t('board.filters.allPriorities'))}</option>${options}</select></label>`;
  }

  return `
    <div class="filters public-board-filters">
      <div class="filters__label"${attrI18n('text', 'board.filters.label')}>${escapeHTML(t('board.filters.label'))}</div>
      <div class="chips-wrapper"><div class="chips-viewport"><div class="chips" id="publicTagChips">${chips}</div></div></div>
      ${sprintSelect}
      ${prioritySelect}
    </div>`;
}

export function buildPublicTopbarHtml(view: PublicBoardViewModel): string {
  const placeholderKey = view.isMobile ? 'board.search.placeholder.mobile' : 'board.search.placeholder.desktop';
  const clearLabel = escapeHTML(t('board.actions.clearSearch'));
  const badgeAria = escapeHTML(t('publicBoard.badgeAria'));
  return `
    <div class="topbar">
      <div class="brand public-board__project-name">${escapeHTML(view.snapshot.project.name)}</div>
      <span class="public-board-badge" role="note" aria-label="${badgeAria}"${attrI18n('aria-label', 'publicBoard.badgeAria')}><span aria-hidden="true"${attrI18n('text', 'publicBoard.badge')}>${escapeHTML(t('publicBoard.badge'))}</span></span>
      <div class="spacer"></div>
      <div class="search-input-wrapper">
        <input type="search" id="publicSearchInput" class="search-input" autocomplete="off" value="${escapeHTML(view.query.search)}" placeholder="${escapeHTML(t(placeholderKey))}" aria-label="${escapeHTML(t(placeholderKey))}"${attrI18n('placeholder', placeholderKey)}${attrI18n('aria-label', placeholderKey)} />
        <button class="search-clear" id="publicSearchClear" type="button" aria-label="${clearLabel}" title="${clearLabel}"${attrI18n('aria-label', 'board.actions.clearSearch')}${view.query.search ? '' : ' hidden'}>✕</button>
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
        ${buildPublicFiltersHtml(view)}
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
