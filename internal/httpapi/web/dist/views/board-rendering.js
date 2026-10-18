import { columnsSpec } from '../features/drag-drop.js';
import { formatDueDateTooltip, isOverdue } from '../dialogs/todo-due.js';
import { escapeHTML, isTemporaryBoard, renderAvatarContent, renderUserAvatar, sanitizeHexColor, } from '../utils.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import { hasI18nKey, t } from '../i18n/index.js';
import { tagIdentityKey } from '../tag-names.js';
import { buildFilterPanelSectionHtml, buildFilterPanelShellHtml, buildPriorityFilterOptions, buildSprintFilterOptions, renderFilterOptionsHtml, } from './filter-panel-markup.js';
import { buildOmniBarHtml } from './omni-tags.js';
import { renderMemberStoryCardContentHtml } from './story-card-content.js';
export function buildDoneColumnKeys(board) {
    const keys = new Set();
    for (const col of board.columnOrder ?? []) {
        if (col.isDone)
            keys.add(col.key);
    }
    return keys;
}
export function buildPriorityTierMap(board) {
    const tiers = board.priorityOrder ?? [];
    const out = {};
    for (const tier of tiers) {
        out[tier.key] = { name: tier.name, color: tier.color };
    }
    return out;
}
export function renderVoiceCommandTriggerHtml() {
    const title = hasI18nKey("voice.title") ? t("voice.title") : "VoiceFlow";
    return `<button class="btn btn--ghost voice-command-trigger" id="voiceCommandBtn" type="button" aria-label="${escapeHTML(title)}" title="${escapeHTML(title)}"><img src="/mic.svg" class="voice-command-trigger__icon" alt="" aria-hidden="true" decoding="async" width="20" height="20" /></button>`;
}
export function getBoardColumns(board) {
    const order = board.columnOrder;
    if (order && order.length > 0) {
        return order.map((c) => ({ key: c.key, title: c.name, color: c.color, isDone: !!c.isDone }));
    }
    return columnsSpec().map((c) => ({ key: c.key, title: c.title, isDone: c.key === "done", color: undefined }));
}
export function visibleBoardLaneCount(board) {
    return getBoardColumns(board).length + (board.agenda?.enabled ? 1 : 0);
}
export function laneColumnTintClassAndStyle(c) {
    const safe = c.color ? sanitizeHexColor(c.color) : null;
    if (!safe)
        return { extraClass: "", styleAttr: "" };
    return { extraClass: " col--lane-tint", styleAttr: ` style="--lane-accent:${escapeHTML(safe)};"` };
}
export function getCombinedChipData(displayTags, activeTags, lastSprintsData, activeSprintId, tagColors) {
    let nextSprintId = activeSprintId;
    if (nextSprintId === "assigned")
        nextSprintId = "scheduled";
    const activeTagNames = new Set(activeTags.map(tagIdentityKey));
    const out = [];
    out.push({ type: "tag", id: "", name: t("board.filters.all"), active: activeTags.length === 0, color: null });
    for (const t of displayTags) {
        out.push({
            type: "tag",
            id: t.name,
            name: t.name,
            active: activeTagNames.has(tagIdentityKey(t.name)),
            color: (t.color || tagColors[t.name] || null),
        });
    }
    if (lastSprintsData) {
        out.push({ type: "sprint", id: "scheduled", name: t("board.filters.scheduled"), active: nextSprintId === "scheduled", color: null });
        out.push({ type: "sprint", id: "unscheduled", name: t("board.filters.unscheduled"), active: nextSprintId === "unscheduled", color: null });
        const seenSprintIds = new Set();
        const nameCount = new Map();
        for (const s of lastSprintsData.sprints)
            nameCount.set(s.name, (nameCount.get(s.name) ?? 0) + 1);
        for (const s of lastSprintsData.sprints) {
            if (seenSprintIds.has(s.id))
                continue;
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
function buildChipHTML(d) {
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
        }
        else if (d.id === "unscheduled") {
            chipTitle = titleAttr(FIELD_TOOLTIPS.sprintFilterUnscheduled);
        }
        else if (d.isActiveSprint) {
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
export function buildChipsHTML(data) {
    return data.map(buildChipHTML).join("");
}
export function renderTodoCard(todo, columnColor, membersByUserId, opts) {
    const content = renderStoryCardContent(todo, membersByUserId, opts);
    const borderStyle = columnColor ? ` style="border-color:${escapeHTML(columnColor)}"` : "";
    const selectedClass = opts?.selectedIds?.has(todo.id) ? " card--selected" : "";
    const doneKey = (todo.columnKey || todo.status || "").toLowerCase();
    const overdue = !todo.archivedAt
        && isOverdue(todo.dueAt, opts?.doneColumnKeys?.has(doneKey) ?? false);
    let overdueWarningHTML = "";
    if (overdue && todo.dueAt) {
        const fullDate = formatDueDateTooltip(todo.dueAt);
        if (fullDate) {
            const warning = escapeHTML(t("todo.due.overdueWarning", { date: fullDate }));
            overdueWarningHTML = `<span class="card__overdue-warning" title="${escapeHTML(fullDate)}" aria-label="${warning}">!</span>`;
        }
    }
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
      ${content}
      <div class="card__top-actions">${overdueWarningHTML}${dragHandleHTML}</div>
    </button>
  `;
}
/** Maps a member todo onto the shared story-card presentation model. */
export function toMemberStoryCardInput(todo, opts) {
    const tagColors = opts?.tagColors ?? null;
    const priorityTier = todo.priorityKey ? opts?.priorityTiers?.[todo.priorityKey] : null;
    const doneKey = (todo.columnKey || todo.status || "").toLowerCase();
    return {
        localId: todo.localId,
        title: todo.title,
        tags: (todo.tags || []).map((name) => ({ name, color: tagColors ? (tagColors[name] ?? null) : null })),
        priority: priorityTier ? { name: priorityTier.name, color: priorityTier.color ?? null } : null,
        points: opts?.showPointsMode && todo.estimationPoints != null ? todo.estimationPoints : null,
        due: todo.dueAt ? { dueAt: todo.dueAt, inDoneColumn: opts?.doneColumnKeys?.has(doneKey) ?? false } : null,
    };
}
/** Shared canonical story content used by both board cards and Wall pins. */
export function renderStoryCardContent(todo, membersByUserId, opts) {
    const assignee = membersByUserId != null && todo.assigneeUserId != null ? membersByUserId[todo.assigneeUserId] : null;
    const avatarHTML = assignee
        ? `<div class="todo-avatar" title="${escapeHTML(assignee.name || assignee.email || '')}">${renderAvatarContent({ name: assignee.name, email: assignee.email, image: assignee.image })}</div>`
        : '';
    return renderMemberStoryCardContentHtml(toMemberStoryCardInput(todo, opts), { tagTone: 'accent', tooltips: true }, avatarHTML);
}
export function buildBoardColumnsHtml(args) {
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
export function buildFiltersHtml(chipsHTML, opts) {
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
function buildSearchFilterControlHtml(args) {
    const { board, search, searchPlaceholder, searchPlaceholderKey, assignee, sort, priority, boardMembers, user, sprintId, sprintData, layout, } = args;
    const filterPanelHTML = buildFilterPanelHtml(assignee ?? null, sort ?? null, boardMembers ?? [], user, priority ?? null, board.priorityOrder ?? [], {
        layout,
        sprintId: sprintId ?? null,
        sprintsEnabled: board.project.sprintsEnabled !== false,
        sprintData: sprintData ?? null,
    });
    const searchPlaceholderAttr = searchPlaceholderKey ? ` data-i18n-placeholder="${searchPlaceholderKey}"` : "";
    const clearSearchLabel = escapeHTML(t("board.actions.clearSearch"));
    return `
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
          ${filterPanelHTML}`;
}
export function buildOmniFilterRowHtml(args, opts) {
    return `<div class="filters filters--omni" data-board-filter-layout="omni">
    ${buildOmniBarHtml({
        ids: {
            pinned: 'omniPinnedTags',
            region: 'omniCandidateRegion',
            previous: 'omniCandidatePrev',
            viewport: 'omniCandidateViewport',
            next: 'omniCandidateNext',
            mobile: 'omniMobileTagPills',
        },
        previousLabel: t('board.filters.previous'),
        nextLabel: t('board.filters.next'),
        leadingHtml: opts?.searchInTopbar ? '' : `<div class="search-input-wrapper">${buildSearchFilterControlHtml({ ...args, layout: 'omni' })}</div>`,
    })}
  </div>`;
}
export function buildTopbarHtml(args) {
    const { board, minimalTopbar, search, searchPlaceholder, searchPlaceholderKey, isMobile, isAnonymousTempBoard, currentUserProjectRole, showVoiceCommands, user, backLabel, backLabelKey, wallEnabled, assignee, sort, priority, boardMembers, sprintId, boardFilterLayout = 'legacy', sprintData, } = args;
    const voiceCommandClass = showVoiceCommands ? "topbar--voice-commands-on" : "topbar--voice-commands-off";
    const voiceCommandTriggerHTML = showVoiceCommands ? renderVoiceCommandTriggerHtml() : "";
    // Scrumbaby is durable-projects-only; temp/anonymous boards never see the entry point.
    // Desktop only: to the right of the mic (voice trigger) in the topbar flex order.
    const wallButtonHTML = wallEnabled &&
        !isMobile &&
        !isTemporaryBoard(board) &&
        (currentUserProjectRole === "maintainer" || currentUserProjectRole === "contributor" || currentUserProjectRole === "viewer")
        ? `<button class="btn btn--ghost" type="button" id="wallBtn" title="${escapeHTML(t("board.actions.openWall"))}" aria-label="${escapeHTML(t("board.actions.openWall"))}" data-i18n-title="board.actions.openWall" data-i18n-aria-label="board.actions.openWall"><img src="/postit.svg" alt="" width="20" height="20" decoding="async" /></button>`
        : "";
    const backLabelAttr = backLabelKey ? ` data-i18n-text="${backLabelKey}"` : "";
    const renameProjectLabel = escapeHTML(t("board.actions.renameProject"));
    const newTodoLabel = escapeHTML(t("board.actions.newTodo"));
    const manageMembersLabel = escapeHTML(t("board.actions.manageMembers"));
    const settingsLabel = escapeHTML(t("board.actions.settings"));
    const archiveLabel = escapeHTML(hasI18nKey("board.actions.openArchive") ? t("board.actions.openArchive") : "Archive");
    const changeProjectImageLabel = escapeHTML(t("board.actions.changeProjectImage"));
    // Legacy and Omni both keep text search in the topbar (between Archive and
    // New Todo). Omni tag suggestions stay in the second-row filter surface.
    const topbarSearchLayout = boardFilterLayout === 'legacy' || boardFilterLayout === 'omni' ? boardFilterLayout : null;
    const searchControlsHTML = topbarSearchLayout
        ? `<div class="search-input-wrapper" data-board-filter-layout="${topbarSearchLayout}">${buildSearchFilterControlHtml({
            board,
            search,
            searchPlaceholder,
            searchPlaceholderKey,
            assignee,
            sort,
            priority,
            boardMembers,
            user,
            sprintId,
            sprintData,
            layout: topbarSearchLayout,
        })}</div>`
        : '';
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
        <button class="btn btn--ghost" type="button" id="archiveBtn" title="${archiveLabel}" aria-label="${archiveLabel}" data-i18n-title="board.actions.openArchive" data-i18n-aria-label="board.actions.openArchive"><img src="/archive.svg" alt="" width="20" height="20" decoding="async" /></button>
        ${searchControlsHTML}
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="${renameProjectLabel}" data-i18n-title="board.actions.renameProject" data-i18n-text="board.actions.renameProject">${renameProjectLabel}</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="${newTodoLabel}" aria-label="${newTodoLabel}" data-i18n-title="board.actions.newTodo" data-i18n-aria-label="board.actions.newTodo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--success" id="manageMembersBtn" title="${manageMembersLabel}" aria-label="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-aria-label="board.actions.manageMembers"><img src="/members.svg" alt="" width="20" height="20" decoding="async" /></button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="${settingsLabel}" data-i18n-aria-label="board.actions.settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--success" id="manageMembersBtn" title="${manageMembersLabel}" aria-label="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-aria-label="board.actions.manageMembers"><img src="/members.svg" alt="" width="20" height="20" decoding="async" /></button>` : ''}
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
        <button class="btn btn--ghost" type="button" id="archiveBtn" title="${archiveLabel}" aria-label="${archiveLabel}" data-i18n-title="board.actions.openArchive" data-i18n-aria-label="board.actions.openArchive"><img src="/archive.svg" alt="" width="20" height="20" decoding="async" /></button>
        ${searchControlsHTML}
        ${isAnonymousTempBoard ? `<button class="btn btn--ghost" id="renameProjectBtn" title="${renameProjectLabel}" data-i18n-title="board.actions.renameProject" data-i18n-text="board.actions.renameProject">${renameProjectLabel}</button>` : ''}
        ${(isTemporaryBoard(board) || currentUserProjectRole === 'maintainer') ? `<button class="btn" id="newTodoBtn" title="${newTodoLabel}" aria-label="${newTodoLabel}" data-i18n-title="board.actions.newTodo" data-i18n-aria-label="board.actions.newTodo"><img src="/new.svg" alt="" width="20" height="20" /></button>` : ''}
        ${!isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--success" id="manageMembersBtn" title="${manageMembersLabel}" aria-label="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-aria-label="board.actions.manageMembers"><img src="/members.svg" alt="" width="20" height="20" decoding="async" /></button>` : ''}
        ${!user ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="${settingsLabel}" data-i18n-aria-label="board.actions.settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${isMobile && !isAnonymousTempBoard && (currentUserProjectRole === 'maintainer' || currentUserProjectRole === 'contributor') ? `<button class="btn btn--success" id="manageMembersBtn" title="${manageMembersLabel}" aria-label="${manageMembersLabel}" data-i18n-title="board.actions.manageMembers" data-i18n-aria-label="board.actions.manageMembers"><img src="/members.svg" alt="" width="20" height="20" decoding="async" /></button>` : ''}
        ${renderUserAvatar(user)}
      </div>
    `;
}
function memberDisplayName(member) {
    return member.name || member.email || '';
}
function assigneeFilterOptionsHtml(assignee, boardMembers, user) {
    const current = assignee || "";
    const allAssigneesLabel = escapeHTML(t("board.filters.allAssignees"));
    const unassignedLabel = escapeHTML(t("board.filters.unassigned"));
    const assignedToMeLabel = escapeHTML(t("board.filters.assignedToMe"));
    const currentUserId = user && typeof user.id === "number" ? user.id : null;
    const meIsActive = current === "me" || (currentUserId != null && current === String(currentUserId));
    const optionClass = (value, active = current === value) => `search-filter-option${active ? " is-active" : ""}`;
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
function sortFilterOptionsHtml(sort) {
    const current = sort || "";
    const optionClass = (value) => `search-filter-option${current === value ? " is-active" : ""}`;
    const defaultLabel = escapeHTML(t("board.filters.defaultOrder"));
    const newestLabel = escapeHTML(t("board.filters.newestFirst"));
    const oldestLabel = escapeHTML(t("board.filters.oldestFirst"));
    const dueSoonLabel = escapeHTML(t("board.filters.dueSoon"));
    return `
      <button type="button" class="${optionClass("")}" data-sort-option="" data-i18n-text="board.filters.defaultOrder">${defaultLabel}</button>
      <button type="button" class="${optionClass("newest")}" data-sort-option="newest" data-i18n-text="board.filters.newestFirst">${newestLabel}</button>
      <button type="button" class="${optionClass("oldest")}" data-sort-option="oldest" data-i18n-text="board.filters.oldestFirst">${oldestLabel}</button>
      <button type="button" class="${optionClass("due")}" data-sort-option="due" data-i18n-text="board.filters.dueSoon">${dueSoonLabel}</button>
  `;
}
function priorityFilterOptionsHtml(priority, tiers) {
    return renderFilterOptionsHtml(buildPriorityFilterOptions(tiers, { includeNoPriority: true }), priority || "", { attr: "data-priority-option" });
}
/** `assigned` is the legacy alias of `scheduled`. */
export function normalizeSprintFilterValue(sprintId) {
    return sprintId === 'assigned' ? 'scheduled' : (sprintId || '');
}
/**
 * Omni Sprint section: only Scheduled and Unscheduled. It is shown when the
 * loaded sprint collection is non-empty, or while a sprint filter is active so
 * that filter stays visible and clearable. A numeric sprint deep link appears
 * as one extra, already-selected option (clicking it clears the filter).
 * Returns '' when the section should not exist.
 */
export function buildSprintFilterSectionHtml(sprintId, sprintData) {
    const current = normalizeSprintFilterValue(sprintId);
    const sprints = sprintData?.sprints ?? [];
    if (sprints.length === 0 && !current)
        return '';
    const options = buildSprintFilterOptions();
    if (current && !options.some((option) => option.value === current)) {
        const sprint = sprints.find((s) => String(s.number) === current);
        options.push({ value: current, label: sprint?.name ?? `#${current}`, sprintState: sprint?.state ?? '' });
    }
    return buildFilterPanelSectionHtml({
        labelKey: 'board.filters.sprint',
        optionsHtml: renderFilterOptionsHtml(options, current, { attr: 'data-sprint-option' }),
        sectionAttr: 'data-sprint-filter-section',
    });
}
// isBoardFilterActive is true whenever a non-default assignee filter, a
// non-default (manual) sort order, or a priority filter is applied. Used to
// drive the toggle's pulse animation, both on initial render and after the
// user picks an option.
export function isBoardFilterActive(assignee, sort, priority, sprintId = null, layout = 'legacy') {
    return !!assignee || !!sort || !!priority || (layout === 'omni' && !!sprintId);
}
// buildFilterPanelHtml is the single source of truth for the assignee/sort/priority
// option lists (labels + i18n + active-state) rendered inside the expandable
// search-filter popover. It replaces the old standalone <select> (assignee
// only) with a button-list panel that also carries the sort and priority
// options, dual-purposing the search input instead of adding another topbar control.
export function buildFilterPanelHtml(assignee, sort, boardMembers, user, priority = null, priorityTiers = [], options = {}) {
    const layout = options.layout ?? 'legacy';
    const sprintSection = layout === 'omni' && options.sprintsEnabled
        ? buildSprintFilterSectionHtml(options.sprintId ?? null, options.sprintData ?? null)
        : '';
    return buildFilterPanelShellHtml({
        toggleId: "searchFilterToggle",
        panelId: "searchFilterPanel",
        active: isBoardFilterActive(assignee, sort, priority, options.sprintId ?? null, layout),
        sectionsHtml: sprintSection
            + buildFilterPanelSectionHtml({ labelKey: "board.filters.assignee", optionsHtml: assigneeFilterOptionsHtml(assignee, boardMembers, user) })
            + buildFilterPanelSectionHtml({ labelKey: "board.filters.sort", optionsHtml: sortFilterOptionsHtml(sort) })
            + buildFilterPanelSectionHtml({ labelKey: "board.filters.priority", optionsHtml: priorityFilterOptionsHtml(priority, priorityTiers) }),
    });
}
export function buildNoResultsHtml(search) {
    // The legacy path created a text node from an already-escaped string.
    // Double-escape here to preserve the same visible output after switching to HTML composition.
    return `<div class="no-results">${escapeHTML(t("board.noResults", { search: escapeHTML(search) }))}</div>`;
}
