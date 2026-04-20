import { columnsSpec } from '../features/drag-drop.js';
import type { BoardMember } from '../state/state.js';
import { Board, Todo } from '../types.js';
import {
  escapeHTML,
  isTemporaryBoard,
  renderAvatarContent,
  renderUserAvatar,
  sanitizeHexColor,
} from '../utils.js';

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
};

type BuildTopbarHtmlArgs = {
  board: Board;
  minimalTopbar: boolean;
  search: string;
  searchPlaceholder: string;
  isMobile: boolean;
  isAnonymousTempBoard: boolean;
  currentUserProjectRole: string | null;
  showVoiceCommands?: boolean;
  user: any;
  backLabel: string;
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
  return `<button class="btn btn--ghost voice-command-trigger" id="voiceCommandBtn" type="button" aria-label="VoiceFlow" title="VoiceFlow"><img src="/mic.svg" class="voice-command-trigger__icon" alt="" aria-hidden="true" decoding="async" width="20" height="20" /></button>`;
}

export function getBoardColumns(board: Board): BoardColumn[] {
  const order = (board as any).columnOrder as Array<{ key: string; name: string; color?: string; isDone?: boolean }> | undefined;
  if (order && order.length > 0) {
    return order.map((c) => ({ key: c.key, title: c.name, color: c.color, isDone: !!c.isDone }));
  }
  return columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "done", color: undefined }));
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
  out.push({ type: "tag", id: "", name: "All", active: activeTag === "", color: null });
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
    out.push({ type: "sprint", id: "scheduled", name: "Scheduled", active: nextSprintId === "scheduled", color: null });
    out.push({ type: "sprint", id: "unscheduled", name: "Unscheduled", active: nextSprintId === "unscheduled", color: null });
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
  if (d.type === "tag") {
    const safe = sanitizeHexColor(d.color);
    const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20;"` : "";
    return `<button class="chip ${activeClass}" data-tag="${escapeHTML(d.id)}" ${colorStyle}>${label}</button>`;
  }
  if (d.id === "__all__") {
    return `<button class="chip chip--sprint ${activeClass}" data-sprint-clear="1">${label}</button>`;
  }
  const activeSprintClass = d.isActiveSprint ? " chip--active-sprint" : "";
  const closedSprintClass = d.isClosedSprint ? " chip--closed-sprint" : "";
  const plannedSprintClass = d.isPlannedSprint ? " chip--planned-sprint" : "";
  return `<button class="chip chip--sprint${activeSprintClass}${closedSprintClass}${plannedSprintClass} ${activeClass}" data-sprint-id="${escapeHTML(d.id)}">${label}</button>`;
}

export function buildChipsHTML(data: ChipData[]): string {
  return data.map(buildChipHTML).join("");
}

export function renderTodoCard(
  t: Todo,
  columnColor?: string,
  membersByUserId?: Record<number, BoardMember>,
  opts?: RenderTodoCardOpts,
): string {
  const showPoints = !!opts?.showPointsMode && t.estimationPoints != null;
  const tagColors = opts?.tagColors ?? null;
  const tags = (t.tags || [])
    .map((tagName) => {
      const tagColor = tagColors ? (tagColors[tagName] ?? null) : null;
      const safe = sanitizeHexColor(tagColor);
      const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
      return `<span class="tag" ${colorStyle}>${escapeHTML(tagName)}</span>`;
    })
    .join("");
  const borderStyle = columnColor ? ` style="border-color:${escapeHTML(columnColor)}"` : "";
  const assignee = membersByUserId != null && t.assigneeUserId != null ? membersByUserId[t.assigneeUserId] : null;
  const avatarHTML = assignee
    ? `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`
    : '';
  const pointsHTML = showPoints ? `<span class="card__points" aria-label="Estimation points">${t.estimationPoints}</span>` : "";
  const footerContent = pointsHTML + avatarHTML;
  const selectedClass = opts?.selectedIds?.has(t.id) ? " card--selected" : "";
  return `
    <button class="card card--${t.status.toLowerCase()}${selectedClass}"${borderStyle} data-todo-id="${t.id}" data-todo-local-id="${t.localId}"${t.assigneeUserId != null ? ` data-assignee-user-id="${t.assigneeUserId}"` : ""} id="todo_${t.id}" type="button">
      <div class="card__content">
        <div class="card__title-row">
          <span class="card__id-inline">#${t.localId}</span>
          <span class="card__title">${escapeHTML(t.title)}</span>
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
      <div class="card__drag-handle" aria-label="Drag to reorder">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
          <circle cx="4" cy="3" r="1.5"/>
          <circle cx="4" cy="8" r="1.5"/>
          <circle cx="4" cy="13" r="1.5"/>
          <circle cx="12" cy="3" r="1.5"/>
          <circle cx="12" cy="8" r="1.5"/>
          <circle cx="12" cy="13" r="1.5"/>
        </svg>
      </div>
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
      return `
          <section class="col ${isMobileActive ? "col--mobile-active" : ""}${laneTint.extraClass}" data-column="${dk}"${laneTint.styleAttr}>
            <div class="col__head col__head--${c.key.toLowerCase()}" ${c.color ? `style="background:${escapeHTML(c.color)};"` : ""}>
              <span class="col__title">${escapeHTML(c.title)}</span>
              <span class="col__count" data-count-for="${dk}">${laneDisplayCount(c.key)}</span>
            </div>
            <div class="col__list" data-status="${dk}" id="list_${c.key}">
              ${todos.map((t) => renderTodoCard(t, c.color, membersByUserId, cardOpts)).join("")}
            </div>
            ${showLoadMore ? `<div class="col__load-more" data-load-more="${dk}"><button class="btn btn--ghost btn--small col__load-more--desktop" type="button">Load more</button><span class="col__load-more--mobile" role="button" tabindex="0" aria-label="Load more">▼</span></div>` : ""}
          </section>
        `;
    })
    .join("");
}

export function buildFiltersHtml(chipsHTML: string, opts?: { innerOnly?: boolean }): string {
  const inner = `
    <div class="filters__label">Tags:</div>
    <div class="chips-wrapper">
      <div class="chips-viewport">
        <div class="chips" id="tagChips">${chipsHTML}</div>
      </div>
      <div class="chips-nav" id="chipsNav" aria-hidden="true">
        <button type="button" class="chips-nav__prev" aria-label="Previous tags">‹</button>
        <button type="button" class="chips-nav__next" aria-label="Next tags">›</button>
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
    isMobile,
    isAnonymousTempBoard,
    currentUserProjectRole,
    showVoiceCommands,
    user,
    backLabel,
  } = args;
  const voiceCommandClass = showVoiceCommands ? "topbar--voice-commands-on" : "topbar--voice-commands-off";
  const voiceCommandTriggerHTML = showVoiceCommands ? renderVoiceCommandTriggerHtml() : "";

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
        <div class="search-input-wrapper">
          <input
            type="text"
            id="searchInput"
            class="search-input"
            placeholder="${searchPlaceholder}"
            value="${escapeHTML(search || "")}"
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="Clear search" title="Clear search">✕</button>` : ''}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="Rename project">Rename</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="New Todo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="Settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${renderUserAvatar(user)}
      </div>
    `;
  }

  return `
      <div class="topbar ${voiceCommandClass}">
        <button class="btn btn--ghost" id="backBtn">${escapeHTML(backLabel)}</button>
        ${isAnonymousTempBoard
          ? (board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" style="width: 32px; height: 32px; pointer-events: none; flex-shrink: 0;" />` : `<span class="project-image-topbar-placeholder" style="width: 32px; height: 32px; flex-shrink: 0;">📷</span>`)
          : `<button class="project-image-topbar-btn" id="projectImageBtn" title="Change project image">
            ${board.project.image ? `<img src="${escapeHTML(board.project.image)}" alt="" class="project-image-topbar" />` : `<span class="project-image-topbar-placeholder">📷</span>`}
          </button>`}
        <div class="brand">${escapeHTML(board.project.name)}</div>
        <div class="spacer"></div>
        ${voiceCommandTriggerHTML}
        <div class="search-input-wrapper">
          <input
            type="text"
            id="searchInput"
            class="search-input"
            placeholder="${searchPlaceholder}"
            value="${escapeHTML(search || "")}"
          />
          ${search && search.trim() !== "" ? `<button class="search-clear" id="searchClear" aria-label="Clear search" title="Clear search">✕</button>` : ''}
        </div>
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="Rename project">Rename</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="New Todo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isAnonymousTempBoard && currentUserProjectRole === 'maintainer' ? `<button class="btn btn--danger" id="deleteProjectBtn" title="Delete Project"><img src="/trash.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="Settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--ghost" id="manageMembersBtn" title="Manage members">Members</button>` : ''}
        ${renderUserAvatar(user)}
      </div>
    `;
}

export function buildNoResultsHtml(search: string): string {
  // The legacy path created a text node from an already-escaped string.
  // Double-escape here to preserve the same visible output after switching to HTML composition.
  return `<div class="no-results">No todos found matching "${escapeHTML(escapeHTML(search))}"</div>`;
}
