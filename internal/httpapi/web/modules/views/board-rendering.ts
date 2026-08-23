import { columnsSpec } from '../features/drag-drop.js';
import type { BoardMember } from '../state/state.js';
import { Board, NO_PRIORITY_FILTER_VALUE, PriorityTier, Todo } from '../types.js';
import {
  escapeHTML,
  isTemporaryBoard,
  renderAvatarContent,
  renderUserAvatar,
  sanitizeHexColor,
} from '../utils.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { hasI18nKey, t } from '../i18n/index.js';

export type BoardColumn = { key: string; title: string; color?: string; isDone: boolean };
export type ChipType = "tag" | "sprint";

export interface ChipData {
  type: ChipType;
  id: string;
  name: string;
  active: boolean;
  color: string | null;
  isActiveSprint?: boolean;
  isClosedSprint?: boolean;
  isPlannedSprint?: boolean;
}

export type SprintChipData = {
  sprints: { id: number; number: number; name: string; state?: string; todoCount?: number }[];
  unscheduledCount?: number;
};

export type RenderTodoCardOpts = {
  tagColors?: Record<string, string>;
  showPointsMode?: boolean;
  selectedIds?: Set<number>;
  priorityTiers?: Record<string, { name: string; color: string }>;
};

export function buildPriorityTierMap(board: Board): Record<string, { name: string; color: string }> {
  const tiers = board.priorityOrder ?? [];
  const out: Record<string, { name: string; color: string }> = {};
  for (const tier of tiers) {
    out[tier.key] = { name: tier.name, color: tier.color };
  }
  return out;
}

type BuildTopbarHtmlArgs = {
  board: Board;
  minimalTopbar: boolean;
  search: string;
  searchPlaceholder: string;
  searchPlaceholderKey?: string;
  isMobile: boolean;
  isAnonymousTempBoard: boolean;
  currentUserProjectRole: string | null;
  showVoiceCommands?: boolean;
  user: any;
  backLabel: string;
  backLabelKey?: string | null;
  wallEnabled?: boolean;
  assignee?: string | null;
  sort?: string | null;
  priority?: string | null;
  boardMembers?: BoardMember[];
};

type BuildBoardColumnsHtmlArgs = {
  boardCols: BoardColumn[];
  board: Board;
  activeMobileTab: string | null | undefined;
  laneMetaByKey: Record<string, { hasMore?: boolean; loading?: boolean } | undefined>;
  laneDisplayCount: (key: string) => number;
  membersByUserId: Record<number, BoardMember>;
  cardOpts: RenderTodoCardOpts;
};

export function renderVoiceCommandTriggerHtml(): string {
  const title = hasI18nKey("voice.title") ? t("voice.title") : "VoiceFlow";
  return `<button class="btn btn--ghost voice-command-trigger" id="voiceCommandBtn" type="button" aria-label="${escapeHTML(title)}" title="${escapeHTML(title)}"><img src="/mic.svg" class="voice-command-trigger__icon" alt="" aria-hidden="true" decoding="async" width="20" height="20" /></button>`;
}

export function getBoardColumns(board: Board): BoardColumn[] {
  const order = (board as any).columnOrder as Array<{ key: string; name: string; color?: string; isDone?: boolean }> | undefined;
  if (order && order.length > 0) {
    return order.map((c) => ({ key: c.key, title: c.name, color: c.color, isDone: !!c.isDone }));
  }
  return columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "done", color: undefined }));
}

export function visibleBoardLaneCount(board: Board): number {
  return getBoardColumns(board).length + (board.agenda?.enabled ? 1 : 0);
}

export function laneColumnTintClassAndStyle(c: { color?: string }): { extraClass: string; styleAttr: string } {
  const safe = c.color ? sanitizeHexColor(c.color) : null;
  if (!safe) return { extraClass: "", styleAttr: "" };
  return { extraClass: " col--lane-tint", styleAttr: ` style="--lane-accent:${escapeHTML(safe)};"` };
}

export function getCombinedChipData(
  displayTags: { name: string; color?: string }[],
  activeTag: string,
  lastSprintsData: SprintChipData | null,
  activeSprintId: string | null,
  tagColors: Record<string, string>,
): ChipData[] {
  let nextSprintId = activeSprintId;
  if (nextSprintId === "assigned") nextSprintId = "scheduled";
  const out: ChipData[] = [];
  out.push({ type: "tag", id: "", name: t("board.filters.all"), active: activeTag === "", color: null });
  for (const t of displayTags) {
    out.push({
      type: "tag",
      id: t.name,
      name: t.name,
      active: activeTag === t.name,
      color: (t.color || tagColors[t.name] || null),
    });
  }
  if (lastSprintsData) {
    out.push({ type: "sprint", id: "scheduled", name: t("board.filters.scheduled"), active: nextSprintId === "scheduled", color: null });
    out.push({ type: "sprint", id: "unscheduled", name: t("board.filters.unscheduled"), active: nextSprintId === "unscheduled", color: null });
    const seenSprintIds = new Set<number>();
    const nameCount = new Map<string, number>();
    for (const s of lastSprintsData.sprints) nameCount.set(s.name, (nameCount.get(s.name) ?? 0) + 1);
    for (const s of lastSprintsData.sprints) {
      if (seenSprintIds.has(s.id)) continue;
      seenSprintIds.add(s.id);
      const label = (nameCount.get(s.name) ?? 0) > 1 ? `${s.name} (${s.number})` : s.name;
      const isActiveSprint = s.state === "ACTIVE";
      const isClosedSprint = s.state === "CLOSED";
      const isPlannedSprint = s.state === "PLANNED";
      out.push({
        type: "sprint",
        id: String(s.number),
        name: label,
        active: nextSprintId === String(s.number),
        color: null,
        isActiveSprint,
        isClosedSprint,
        isPlannedSprint,
      });
    }
  }
  return out;
}

function buildChipHTML(d: ChipData): string {
  const activeClass = d.active ? "chip--active" : "";
  const label = escapeHTML(d.name);
  const i18nAttr = d.type === "tag" && d.id === ""
    ? ' data-i18n-text="board.filters.all"'
    : d.type === "sprint" && d.id === "scheduled"
      ? ' data-i18n-text="board.filters.scheduled"'
      : d.type === "sprint" && d.id === "unscheduled"
        ? ' data-i18n-text="board.filters.unscheduled"'
        : "";
  let chipTitle = "";
  if (d.type === "sprint") {
    if (d.id === "scheduled") {
      chipTitle = titleAttr(FIELD_TOOLTIPS.sprintFilterScheduled);
    } else if (d.id === "unscheduled") {
      chipTitle = titleAttr(FIELD_TOOLTIPS.sprintFilterUnscheduled);
    } else if (d.isActiveSprint) {
      chipTitle = titleAttr(FIELD_TOOLTIPS.sprintFilterActive);
    }
  }
  if (d.type === "tag") {
    const safe = sanitizeHexColor(d.color);
    const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20;"` : "";
    return `<button class="chip ${activeClass}" data-tag="${escapeHTML(d.id)}" ${colorStyle}${i18nAttr}>${label}</button>`;
  }
  if (d.id === "__all__") {
    return `<button class="chip chip--sprint ${activeClass}" data-sprint-clear="1"${i18nAttr}>${label}</button>`;
  }
  const activeSprintClass = d.isActiveSprint ? " chip--active-sprint" : "";
  const closedSprintClass = d.isClosedSprint ? " chip--closed-sprint" : "";
  const plannedSprintClass = d.isPlannedSprint ? " chip--planned-sprint" : "";
  return `<button class="chip chip--sprint${activeSprintClass}${closedSprintClass}${plannedSprintClass} ${activeClass}" data-sprint-id="${escapeHTML(d.id)}"${chipTitle}${i18nAttr}>${label}</button>`;
}

export function buildChipsHTML(data: ChipData[]): string {
  return data.map(buildChipHTML).join("");
}

export function renderTodoCard(
  todo: Todo,
  columnColor?: string,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): string {
  const showPoints = !!opts?.showPointsMode && todo.estimationPoints != null;
  const tagColors = opts?.tagColors ?? null;
  const tags = (todo.tags || [])
    .map((tagName) => {
      const tagColor = tagColors ? (tagColors[tagName] ?? null) : null;
      const safe = sanitizeHexColor(tagColor);
      const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
      return `<span class="tag" ${colorStyle}>${escapeHTML(tagName)}</span>`;
    })
    .join("");
  const borderStyle = columnColor ? ` style="border-color:${escapeHTML(columnColor)}"` : "";
  const assignee = membersByUserId != null && todo.assigneeUserId != null ? membersByUserId[todo.assigneeUserId] : null;
  const avatarHTML = assignee
    ? `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`
    : '';
  const pointsHTML = showPoints
    ? `<span class="card__points"${titleAttr(FIELD_TOOLTIPS.estimationPoints)} aria-label="${escapeHTML(t("todo.fields.estimationPoints"))}" data-i18n-aria-label="todo.fields.estimationPoints">${todo.estimationPoints}</span>`
    : "";
  const priorityTier = todo.priorityKey ? opts?.priorityTiers?.[todo.priorityKey] : null;
  const priorityColor = sanitizeHexColor(priorityTier?.color ?? null);
  const priorityStyle = priorityColor
    ? ` style="border-color:${priorityColor}; background:${priorityColor}20; color:${priorityColor};"`
    : "";
  const priorityHTML = priorityTier
    ? `<span class="card__priority"${priorityStyle}${titleAttr(FIELD_TOOLTIPS.priority)} aria-label="${escapeHTML(t("todo.fields.priority"))}: ${escapeHTML(priorityTier.name)}">${escapeHTML(priorityTier.name)}</span>`
    : "";
  const footerContent = priorityHTML + pointsHTML + avatarHTML;
  const selectedClass = opts?.selectedIds?.has(todo.id) ? " card--selected" : "";
  const dragHandleHTML = `
      <div class="card__drag-handle" aria-label="${escapeHTML(t("board.todo.dragCard"))}" data-i18n-aria-label="board.todo.dragCard">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
          <circle cx="4" cy="3" r="1.5"/>
          <circle cx="4" cy="8" r="1.5"/>
          <circle cx="4" cy="13" r="1.5"/>
          <circle cx="12" cy="3" r="1.5"/>
          <circle cx="12" cy="8" r="1.5"/>
          <circle cx="12" cy="13" r="1.5"/>
        </svg>
      </div>
    `;
  return `
    <button class="card card--${todo.status.toLowerCase()}${selectedClass}"${borderStyle} data-todo-id="${todo.id}" data-todo-local-id="${todo.localId}"${todo.assigneeUserId != null ? ` data-assignee-user-id="${todo.assigneeUserId}"` : ""} id="todo_${todo.id}" type="button">
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${todo.localId}</span>
          <span class="card__title">${escapeHTML(todo.title)}</span>
        </div>
        ${tags || footerContent ? `
  <div class="card__tags">
    <span class="card__tags-list">
      ${tags}
    </span>
    <span class="card__badges">
      ${footerContent}
    </span>
  </div>
` : ""}
      </div>
      ${dragHandleHTML}
    </button>
  `;
}

export function buildBoardColumnsHtml(args: BuildBoardColumnsHtmlArgs): string {
  const { boardCols, board, activeMobileTab, laneMetaByKey, laneDisplayCount, membersByUserId, cardOpts } = args;
  return boardCols
    .map((c) => {
      const todos = board.columns[c.key] || [];
      const isMobileActive = activeMobileTab === c.key;
      const laneMeta = laneMetaByKey[c.key];
      const showLoadMore = laneMeta?.hasMore && !laneMeta?.loading;
      const laneTint = laneColumnTintClassAndStyle(c);
      const dk = escapeHTML(c.key);
      const loadMoreLabel = escapeHTML(t("board.loadMore"));
      return `
          <section class="col ${isMobileActive ? "col--mobile-active" : ""}${laneTint.extraClass}" data-column="${dk}"${laneTint.styleAttr}>
            <div class="col__head col__head--${c.key.toLowerCase()}" ${c.color ? `style="background:${escapeHTML(c.color)};"` : ""}>
              <span class="col__title">${escapeHTML(c.title)}</span>
              <span class="col__count" data-count-for="${dk}">${laneDisplayCount(c.key)}</span>
            </div>
            <div class="col__list" data-status="${dk}" id="list_${c.key}">
              ${todos.map((t) => renderTodoCard(t, c.color, membersByUserId, cardOpts)).join("")}
            </div>
            ${showLoadMore ? `<div class="col__load-more" data-load-more="${dk}"><button class="btn btn--ghost btn--small col__load-more--desktop" type="button" data-i18n-text="board.loadMore">${loadMoreLabel}</button><span class="col__load-more--mobile" role="button" tabindex="0" aria-label="${loadMoreLabel}" data-i18n-aria-label="board.loadMore">▼</span></div>` : ""}
          </section>
        `;
    })
    .join("");
}

export function buildFiltersHtml(chipsHTML: string, opts?: { innerOnly?: boolean }): string {
  const inner = `
    <div class="filters__label" data-i18n-text="board.filters.label">${escapeHTML(t("board.filters.label"))}</div>
    <div class="chips-wrapper">
      <div class="chips-viewport">
        <div class="chips" id="tagChips">${chipsHTML}</div>
      </div>
      <div class="chips-nav" id="chipsNav" aria-hidden="true">
        <button type="button" class="chips-nav__prev" aria-label="${escapeHTML(t("board.filters.previous"))}" data-i18n-aria-label="board.filters.previous">‹</button>
        <button type="button" class="chips-nav__next" aria-label="${escapeHTML(t("board.filters.next"))}" data-i18n-aria-label="board.filters.next">›</button>
      </div>
    </div>
  `;
  return opts?.innerOnly ? inner : `<div class="filters">${inner}</div>`;
}

export function buildTopbarHtml(args: BuildTopbarHtmlArgs): string {
  const {
    board,
    minimalTopbar,
    search,
    searchPlaceholder,
    searchPlaceholderKey,
    isMobile,
    isAnonymousTempBoard,
    currentUserProjectRole,
    showVoiceCommands,
    user,
    backLabel,
    backLabelKey,
    wallEnabled,
    assignee,
    sort,
    priority,
    boardMembers,
  } = args;
  const filterPanelHTML = buildFilterPanelHtml(assignee ?? null, sort ?? null, boardMembers ?? [], user, priority ?? null, board.priorityOrder ?? []);
  const voiceCommandClass = showVoiceCommands ? "topbar--voice-commands-on" : "topbar--voice-commands-off";
  const voiceCommandTriggerHTML = showVoiceCommands ? renderVoiceCommandTriggerHtml() : "";
  // Scrumbaby is durable-projects-only; temp/anonymous boards never see the entry point.
  // Desktop only: to the right of the mic (voice trigger) in the topbar flex order.
  const wallButtonHTML =
    wallEnabled &&
    !isMobile &&
    !isTemporaryBoard(board) &&
    (currentUserProjectRole === "maintainer" || currentUserProjectRole === "contributor")
      ? `<button class="btn btn--ghost" type="button" id="wallBtn" title="${escapeHTML(t("board.actions.openWall"))}" aria-label="${escapeHTML(t("board.actions.openWall"))}" data-i18n-title="board.actions.openWall" data-i18n-aria-label="board.actions.openWall"><img src="/postit.svg" alt="" width="20" height="20" decoding="async" /></button>`
      : "";
  const searchPlaceholderAttr = searchPlaceholderKey ? ` data-i18n-placeholder="${searchPlaceholderKey}"` : "";
  const backLabelAttr = backLabelKey ? ` data-i18n-text="${backLabelKey}"` : "";
  const clearSearchLabel = escapeHTML(t("board.actions.clearSearch"));
  const renameProjectLabel = escapeHTML(t("board.actions.renameProject"));
  const newTodoLabel = escapeHTML(t("board.actions.newTodo"));
  const manageMembersLabel = escapeHTML(t("board.actions.manageMembers"));
  const settingsLabel = escapeHTML(t("board.actions.settings"));
  const changeProjectImageLabel = escapeHTML(t("board.actions.changeProjectImage"));
  const deleteProjectLabel = escapeHTML(t("board.actions.deleteProject"));

  if (minimalTopbar) {
    return `
      <div class="topbar ${voiceCommandClass}">
        <div class="brand">
          <button class="brand-link" id="brandLink" style="background: none; border: none; padding: 0; cursor: pointer;">
            <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
          </button>
        </div>
        ${isAnonymousTempBoard
          ? (board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" style="width: 32px; height: 32px; pointer-events: none; flex-shrink: 0;" />` : `<span class="project-image-topbar-placeholder" style="width: 32px; height: 32px; flex-shrink: 0;">📷</span>`)
          : ''}
        <div class="brand">${escapeHTML(board.project.name)}</div>
        <div class="spacer"></div>
        ${voiceCommandTriggerHTML}
        ${wallButtonHTML}
        <div class="search-input-wrapper">
          <input
            type="text"
            id="searchInput"
            class="search-input"
            placeholder="${searchPlaceholder}"
            ${searchPlaceholderAttr}
            value="${escapeHTML(search || "")}"
            ${titleAttr(FIELD_TOOLTIPS.boardSearch)}
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="${clearSearchLabel}" title="${clearSearchLabel}" data-i18n-aria-label="board.actions.clearSearch" data-i18n-title="board.actions.clearSearch">✕</button>` : ''}
          ${filterPanelHTML}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="${renameProjectLabel}" data-i18n-title="board.actions.renameProject" data-i18n-text="board.actions.renameProject">${renameProjectLabel}</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="${newTodoLabel}" aria-label="${newTodoLabel}" data-i18n-title="board.actions.newTodo" data-i18n-aria-label="board.actions.newTodo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-text="board.actions.manageMembers">${manageMembersLabel}</button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="${settingsLabel}" data-i18n-aria-label="board.actions.settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-text="board.actions.manageMembers">${manageMembersLabel}</button>` : ''}
        ${renderUserAvatar(user)}
      </div>
    `;
  }

  return `
      <div class="topbar ${voiceCommandClass}">
        <button class="btn btn--ghost" id="backBtn"${backLabelAttr}>${escapeHTML(backLabel)}</button>
        ${isAnonymousTempBoard
          ? (board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" style="width: 32px; height: 32px; pointer-events: none; flex-shrink: 0;" />` : `<span class="project-image-topbar-placeholder" style="width: 32px; height: 32px; flex-shrink: 0;">📷</span>`)
          : `<button class="project-image-topbar-btn" id="projectImageBtn" title="${changeProjectImageLabel}" data-i18n-title="board.actions.changeProjectImage">
            ${board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" />` : `<span class="project-image-topbar-placeholder">📷</span>`}
          </button>`}
        <div class="brand">${escapeHTML(board.project.name)}</div>
        <div class="spacer"></div>
        ${voiceCommandTriggerHTML}
        ${wallButtonHTML}
        <div class="search-input-wrapper">
          <input
            type="text"
            id="searchInput"
            class="search-input"
            placeholder="${searchPlaceholder}"
            ${searchPlaceholderAttr}
            value="${escapeHTML(search || "")}"
            ${titleAttr(FIELD_TOOLTIPS.boardSearch)}
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="${clearSearchLabel}" title="${clearSearchLabel}" data-i18n-aria-label="board.actions.clearSearch" data-i18n-title="board.actions.clearSearch">✕</button>` : ''}
          ${filterPanelHTML}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="${renameProjectLabel}" data-i18n-title="board.actions.renameProject" data-i18n-text="board.actions.renameProject">${renameProjectLabel}</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="${newTodoLabel}" aria-label="${newTodoLabel}" data-i18n-title="board.actions.newTodo" data-i18n-aria-label="board.actions.newTodo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isAnonymousTempBoard && currentUserProjectRole === 'maintainer' ? `<button class="btn btn--danger" id="deleteProjectBtn" title="${deleteProjectLabel}" aria-label="${deleteProjectLabel}" data-i18n-title="board.actions.deleteProject" data-i18n-aria-label="board.actions.deleteProject"><img src="/trash.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-text="board.actions.manageMembers">${manageMembersLabel}</button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="${settingsLabel}" data-i18n-aria-label="board.actions.settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-text="board.actions.manageMembers">${manageMembersLabel}</button>` : ''}
        ${renderUserAvatar(user)}
      </div>
    `;
}

function memberDisplayName(member: BoardMember): string {
  return member.name || member.email || '';
}

function assigneeFilterOptionsHtml(assignee: string | null, boardMembers: BoardMember[], user: any): string {
  const current = assignee || "";
  const allAssigneesLabel = escapeHTML(t("board.filters.allAssignees"));
  const unassignedLabel = escapeHTML(t("board.filters.unassigned"));
  const assignedToMeLabel = escapeHTML(t("board.filters.assignedToMe"));
  const currentUserId = user && typeof user.id === "number" ? user.id : null;
  const meIsActive = current === "me" || (currentUserId != null && current === String(currentUserId));
  const optionClass = (value: string, active = current === value) => `search-filter-option${active ? " is-active" : ""}`;
  const meOption = user
    ? `<button type="button" class="${optionClass("me", meIsActive)}" data-assignee-option="me" data-i18n-text="board.filters.assignedToMe">${assignedToMeLabel}</button>`
    : "";
  const memberOptions = boardMembers
    .filter((m) => currentUserId == null || m.userId !== currentUserId)
    .map((m) => {
      const value = String(m.userId);
      return `<button type="button" class="${optionClass(value)}" data-assignee-option="${escapeHTML(value)}">${escapeHTML(memberDisplayName(m))}</button>`;
    })
    .join("");
  return `
      <button type="button" class="${optionClass("")}" data-assignee-option="" data-i18n-text="board.filters.allAssignees">${allAssigneesLabel}</button>
      <button type="button" class="${optionClass("unassigned")}" data-assignee-option="unassigned" data-i18n-text="board.filters.unassigned">${unassignedLabel}</button>
      ${meOption}
      ${memberOptions}
  `;
}

function sortFilterOptionsHtml(sort: string | null): string {
  const current = sort || "";
  const optionClass = (value: string) => `search-filter-option${current === value ? " is-active" : ""}`;
  const defaultLabel = escapeHTML(t("board.filters.defaultOrder"));
  const newestLabel = escapeHTML(t("board.filters.newestFirst"));
  const oldestLabel = escapeHTML(t("board.filters.oldestFirst"));
  return `
      <button type="button" class="${optionClass("")}" data-sort-option="" data-i18n-text="board.filters.defaultOrder">${defaultLabel}</button>
      <button type="button" class="${optionClass("newest")}" data-sort-option="newest" data-i18n-text="board.filters.newestFirst">${newestLabel}</button>
      <button type="button" class="${optionClass("oldest")}" data-sort-option="oldest" data-i18n-text="board.filters.oldestFirst">${oldestLabel}</button>
  `;
}

function priorityFilterOptionsHtml(priority: string | null, tiers: PriorityTier[]): string {
  const current = priority || "";
  const optionClass = (value: string) => `search-filter-option${current === value ? " is-active" : ""}`;
  const allPrioritiesLabel = escapeHTML(t("board.filters.allPriorities"));
  const noPriorityLabel = escapeHTML(t("board.filters.noPriority"));
  const tierOptions = tiers
    .map((tier) => {
      const safe = sanitizeHexColor(tier.color);
      const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20;"` : "";
      return `<button type="button" class="${optionClass(tier.key)}" data-priority-option="${escapeHTML(tier.key)}" ${colorStyle}>${escapeHTML(tier.name)}</button>`;
    })
    .join("");
  return `
      <button type="button" class="${optionClass("")}" data-priority-option="" data-i18n-text="board.filters.allPriorities">${allPrioritiesLabel}</button>
      <button type="button" class="${optionClass(NO_PRIORITY_FILTER_VALUE)}" data-priority-option="${NO_PRIORITY_FILTER_VALUE}" data-i18n-text="board.filters.noPriority">${noPriorityLabel}</button>
      ${tierOptions}
  `;
}

// isBoardFilterActive is true whenever a non-default assignee filter, a
// non-default (manual) sort order, or a priority filter is applied. Used to
// drive the toggle's pulse animation, both on initial render and after the
// user picks an option.
export function isBoardFilterActive(assignee: string | null, sort: string | null, priority: string | null): boolean {
  return !!assignee || !!sort || !!priority;
}

// buildFilterPanelHtml is the single source of truth for the assignee/sort/priority
// option lists (labels + i18n + active-state) rendered inside the expandable
// search-filter popover. It replaces the old standalone <select> (assignee
// only) with a button-list panel that also carries the sort and priority
// options, dual-purposing the search input instead of adding another topbar control.
export function buildFilterPanelHtml(
  assignee: string | null,
  sort: string | null,
  boardMembers: BoardMember[],
  user: any,
  priority: string | null = null,
  priorityTiers: PriorityTier[] = [],
): string {
  const filtersLabel = escapeHTML(t("board.filters.openFilters"));
  const toggleActiveClass = isBoardFilterActive(assignee, sort, priority) ? " search-filter-toggle--active" : "";
  return `
    <button
      type="button"
      class="search-filter-toggle${toggleActiveClass}"
      id="searchFilterToggle"
      aria-expanded="false"
      aria-haspopup="true"
      aria-label="${filtersLabel}"
      title="${filtersLabel}"
      data-i18n-aria-label="board.filters.openFilters"
      data-i18n-title="board.filters.openFilters"
    >
      <svg width="10" height="6" viewBox="0 0 10 6" fill="none" aria-hidden="true" focusable="false">
        <path d="M1 1L5 5L9 1" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </button>
    <div class="search-filter-panel" id="searchFilterPanel" hidden>
      <div class="search-filter-panel__section">
        <div class="search-filter-panel__label" data-i18n-text="board.filters.assignee">${escapeHTML(t("board.filters.assignee"))}</div>
        ${assigneeFilterOptionsHtml(assignee, boardMembers, user)}
      </div>
      <div class="search-filter-panel__section">
        <div class="search-filter-panel__label" data-i18n-text="board.filters.sort">${escapeHTML(t("board.filters.sort"))}</div>
        ${sortFilterOptionsHtml(sort)}
      </div>
      <div class="search-filter-panel__section">
        <div class="search-filter-panel__label" data-i18n-text="board.filters.priority">${escapeHTML(t("board.filters.priority"))}</div>
        ${priorityFilterOptionsHtml(priority, priorityTiers)}
      </div>
    </div>
  `;
}

export function buildNoResultsHtml(search: string): string {
  // The legacy path created a text node from an already-escaped string.
  // Double-escape here to preserve the same visible output after switching to HTML composition.
  return `<div class="no-results">${escapeHTML(t("board.noResults", { search: escapeHTML(search) }))}</div>`;
}
