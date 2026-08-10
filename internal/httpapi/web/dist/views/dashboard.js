import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { formatDate, formatLongDateWithWeekday, I18N_LOCALE_CHANGED, t } from '../i18n/index.js';
import { navigate } from '../router.js';
import { escapeHTML, renderUserAvatar, sanitizeHexColor } from '../utils.js';
import { getDashboardLoading, getDashboardNextCursor, getDashboardSummary, getDashboardTodos, getDashboardTodoSort, getProjects, getUser, } from '../state/selectors.js';
import { appendDashboardTodos, setDashboardLoading, setDashboardNextCursor, setDashboardTodoSort, setProjects, setProjectsTab, setSettingsActiveTab, setDashboardSummary, setDashboardTodos, } from '../state/mutations.js';
import { ingestProjectsFromApp } from '../core/notifications.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { temporaryBoardsNavLabelKey } from '../nav-labels.js';
const BOUND_FLAG = Symbol('bound');
const DASHBOARD_MOBILE_BREAKPOINT = 767;
let dashboardI18nBound = false;
function dashboardBoardOrderLabelKey(width = typeof window !== 'undefined' ? window.innerWidth : DASHBOARD_MOBILE_BREAKPOINT + 1) {
    return width <= DASHBOARD_MOBILE_BREAKPOINT
        ? 'dashboard.sort.board.short'
        : 'dashboard.sort.board.long';
}
function dashboardTodosQueryString() {
    let q = 'limit=20';
    if (getDashboardTodoSort() === 'board') {
        q += '&sort=board';
    }
    return q;
}
function isDashboardLoadingShell() {
    return getDashboardLoading() && !getDashboardSummary() && getDashboardTodos().length === 0;
}
function rerenderDashboardForLocaleChange() {
    if (!document.querySelector('.page--dashboard')) {
        return;
    }
    if (isDashboardLoadingShell()) {
        renderLoadingShell();
        return;
    }
    renderDashboardContent();
}
function ensureDashboardI18nBinding() {
    if (dashboardI18nBound) {
        return;
    }
    dashboardI18nBound = true;
    document.addEventListener(I18N_LOCALE_CHANGED, () => {
        rerenderDashboardForLocaleChange();
    });
}
function formatSprintDateRange(startAt, endAt) {
    return `${formatDate(startAt, { month: "short", day: "numeric" })} – ${formatDate(endAt, { month: "short", day: "numeric" })}`;
}
function renderDashboardPanelHeader() {
    const sort = getDashboardTodoSort();
    const boardLabelKey = dashboardBoardOrderLabelKey();
    return `
          <div class="panel__header panel__header--dashboard">
            <div class="panel__title" data-i18n-text="dashboard.title">${escapeHTML(t("dashboard.title"))}</div>
            <div class="dashboard-sort">
              <label class="dashboard-sort__label" for="dashboardTodoSort" data-i18n-text="dashboard.sort.label">${escapeHTML(t("dashboard.sort.label"))}</label>
              <select id="dashboardTodoSort" class="dashboard-sort__select" aria-describedby="dashboardSortHint" title="${escapeHTML(t("dashboard.sort.hint"))}" data-i18n-title="dashboard.sort.hint">
                <option value="activity" ${sort === 'activity' ? 'selected' : ''} data-i18n-text="dashboard.sort.activity">${escapeHTML(t("dashboard.sort.activity"))}</option>
                <option value="board" ${sort === 'board' ? 'selected' : ''} data-i18n-text="${boardLabelKey}">${escapeHTML(t(boardLabelKey))}</option>
              </select>
            </div>
          </div>
          <p id="dashboardSortHint" class="dashboard-sort__hint muted" data-i18n-text="dashboard.sort.hint">${escapeHTML(t("dashboard.sort.hint"))}</p>`;
}
function renderTopTabs() {
    const projects = getProjects() || [];
    const durableProjects = projects.filter((p) => !p.expiresAt);
    const temporaryBoards = projects.filter((p) => !!p.expiresAt);
    const temporaryLabelKey = temporaryBoardsNavLabelKey();
    return `
    <div class="chips" style="margin-top: 10px;">
      <button class="chip chip--active" id="dashboardTabBtn" type="button" data-i18n-text="dashboard.tabs.dashboard">${escapeHTML(t("dashboard.tabs.dashboard"))}</button>
      <button class="chip" id="projectsTabBtn" type="button">
        <span class="dashboard-tab__label" data-i18n-text="dashboard.tabs.projects">${escapeHTML(t("dashboard.tabs.projects"))}</span>
        <span class="chip__count">${durableProjects.length}</span>
      </button>
      <button class="chip" id="temporaryTabBtn" type="button">
        <span class="dashboard-tab__label" data-i18n-text="${temporaryLabelKey}">${escapeHTML(t(temporaryLabelKey))}</span>
        <span class="chip__count">${temporaryBoards.length}</span>
      </button>
    </div>
  `;
}
function bindAvatarButton() {
    const userAvatarBtn = document.getElementById('userAvatarBtn');
    if (!userAvatarBtn || userAvatarBtn[BOUND_FLAG]) {
        return;
    }
    userAvatarBtn.addEventListener('click', async () => {
        setSettingsActiveTab('profile');
        await renderSettingsModal();
        settingsDialog.showModal();
    });
    userAvatarBtn[BOUND_FLAG] = true;
}
function renderLoadingShell() {
    app.innerHTML = `
    <div class="page page--dashboard">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        ${renderUserAvatar(getUser())}
      </div>
      <div class="container">
        <div class="panel">
          ${renderDashboardPanelHeader()}
          ${renderTopTabs()}
          <div class="list">
            <div class="list__item"><div class="muted" data-i18n-text="dashboard.loading.assignedTodos">${escapeHTML(t("dashboard.loading.assignedTodos"))}</div></div>
            <div class="list__item"><div class="muted" data-i18n-text="dashboard.loading.assignedTodos">${escapeHTML(t("dashboard.loading.assignedTodos"))}</div></div>
            <div class="list__item"><div class="muted" data-i18n-text="dashboard.loading.assignedTodos">${escapeHTML(t("dashboard.loading.assignedTodos"))}</div></div>
          </div>
        </div>
      </div>
    </div>
  `;
    bindTopNav();
    bindDashboardSort();
    bindAvatarButton();
}
function renderDashboardContent() {
    const summary = getDashboardSummary();
    const todos = getDashboardTodos();
    const nextCursor = getDashboardNextCursor();
    const loading = getDashboardLoading();
    const totalStoryPoints = summary?.totalAssignedStoryPoints ?? 0;
    const assignedSplit = summary?.assignedSplit ?? null;
    const wipCount = summary?.wipCount ?? 0;
    const wipInProgressCount = summary?.wipInProgressCount ?? 0;
    const wipTestingCount = summary?.wipTestingCount ?? 0;
    const sprintCompletion = summary?.sprintCompletion ?? null;
    const sprintCompletionAllUsers = summary?.sprintCompletionAllUsers ?? null;
    const weeklyThroughput = summary?.weeklyThroughput ?? [];
    const avgLeadTimeDays = summary?.avgLeadTimeDays ?? null;
    const oldestWip = summary?.oldestWip ?? null;
    const hasTodos = todos.length > 0;
    const projectsByProjectId = new Map();
    for (const p of summary?.projects ?? []) {
        projectsByProjectId.set(p.projectId, p);
    }
    const leftColumnMarkup = hasTodos
        ? `<div class="dashboard-todo-groups">${renderDashboardTodoGroups(todos, projectsByProjectId)}</div>
       ${nextCursor
            ? `<div class="dashboard-load-more" data-dashboard-load-more>
                <button class="btn btn--ghost btn--small dashboard-load-more__desktop" id="dashboardLoadMoreBtn" type="button" ${loading ? 'disabled' : ''}>
                  <span data-i18n-text="${loading ? 'dashboard.loadMore.loading' : 'dashboard.loadMore.action'}">${escapeHTML(t(loading ? 'dashboard.loadMore.loading' : 'dashboard.loadMore.action'))}</span>
                </button>
                <span class="dashboard-load-more__mobile" id="dashboardLoadMoreMobile" role="button" tabindex="0" aria-busy="${loading ? 'true' : 'false'}" aria-label="${escapeHTML(t(loading ? 'dashboard.loadMore.loadingAria' : 'dashboard.loadMore.action'))}" data-i18n-aria-label="${loading ? 'dashboard.loadMore.loadingAria' : 'dashboard.loadMore.action'}" ${loading ? 'data-loading="1"' : ''}>\u25BC</span>
              </div>`
            : ''}`
        : `<div class="muted" style="margin-top: 48px;" data-i18n-text="dashboard.empty.assignedTodos">${escapeHTML(t("dashboard.empty.assignedTodos"))}</div>`;
    const storiesPct = sprintCompletion && sprintCompletion.totalStories > 0
        ? Math.round((sprintCompletion.doneStories / sprintCompletion.totalStories) * 100)
        : 0;
    const pointsPct = sprintCompletion && sprintCompletion.totalPoints > 0
        ? Math.round((sprintCompletion.donePoints / sprintCompletion.totalPoints) * 100)
        : 0;
    const storiesPctAll = sprintCompletionAllUsers && sprintCompletionAllUsers.totalStories > 0
        ? Math.round((sprintCompletionAllUsers.doneStories / sprintCompletionAllUsers.totalStories) * 100)
        : 0;
    const pointsPctAll = sprintCompletionAllUsers && sprintCompletionAllUsers.totalPoints > 0
        ? Math.round((sprintCompletionAllUsers.donePoints / sprintCompletionAllUsers.totalPoints) * 100)
        : 0;
    const maxThroughput = weeklyThroughput.length > 0
        ? Math.max(...weeklyThroughput.map((p) => Math.max(p.stories, p.points)), 1)
        : 1;
    const throughputBars = weeklyThroughput
        .map((p) => {
        const h = maxThroughput > 0 ? (Math.max(p.stories, p.points) / maxThroughput) * 100 : 0;
        return `<div class="dashboard-throughput__bar" style="--bar-height: ${h}%" title="${escapeHTML(t("dashboard.throughput.barTitle", { weekStart: p.weekStart, stories: p.stories, points: p.points }))}"></div>`;
    })
        .join('');
    const sprintAssignedRow = assignedSplit != null
        ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.assigned"))}</span>
        <span class="dashboard-stats__value">${escapeHTML(t("dashboard.stats.pointsTodos", { points: assignedSplit.sprintPoints, todos: assignedSplit.sprintStories }))}</span>
      </div>`
        : '';
    const completionRateRow = sprintCompletion && (sprintCompletion.totalStories > 0 || sprintCompletion.totalPoints > 0)
        ? `<div class="dashboard-stats__row dashboard-stats__row--progress">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.yourCompletion"))}</span>
        <span class="dashboard-stats__value">${escapeHTML(t("dashboard.stats.storiesPoints", { stories: storiesPct, points: pointsPct }))}</span>
        <div class="dashboard-stats__progress-wrap">
          <div class="dashboard-stats__progress-bar" role="progressbar" aria-valuenow="${sprintCompletion.doneStories}" aria-valuemin="0" aria-valuemax="${sprintCompletion.totalStories}" style="--progress: ${sprintCompletion.totalStories > 0 ? (sprintCompletion.doneStories / sprintCompletion.totalStories) * 100 : 0}%"></div>
        </div>
      </div>`
        : '';
    const completionRateAllUsersRow = sprintCompletionAllUsers && (sprintCompletionAllUsers.totalStories > 0 || sprintCompletionAllUsers.totalPoints > 0)
        ? `<div class="dashboard-stats__row dashboard-stats__row--progress dashboard-stats__row--progress-team">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.teamCompletion"))}</span>
        <span class="dashboard-stats__value">${escapeHTML(t("dashboard.stats.storiesPoints", { stories: storiesPctAll, points: pointsPctAll }))}</span>
        <div class="dashboard-stats__progress-wrap">
          <div class="dashboard-stats__progress-bar" role="progressbar" aria-valuenow="${sprintCompletionAllUsers.doneStories}" aria-valuemin="0" aria-valuemax="${sprintCompletionAllUsers.totalStories}" style="--progress: ${sprintCompletionAllUsers.totalStories > 0 ? (sprintCompletionAllUsers.doneStories / sprintCompletionAllUsers.totalStories) * 100 : 0}%"></div>
        </div>
      </div>`
        : '';
    const workloadAssignedRow = assignedSplit != null
        ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.totalAssigned"))}</span>
        <span class="dashboard-stats__value dashboard-stats__value--assigned"><span class="dashboard-stats__value--total">${escapeHTML(t("dashboard.stats.totalPrefix"))}</span> ${escapeHTML(t("dashboard.stats.pointsTodos", { points: assignedSplit.backlogPoints + assignedSplit.sprintPoints, todos: assignedSplit.backlogStories + assignedSplit.sprintStories }))}</span>
      </div>`
        : `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.totalAssigned"))}</span>
        <span class="dashboard-stats__value">${escapeHTML(t("dashboard.stats.pointsOnly", { points: totalStoryPoints }))}</span>
      </div>`;
    const showLegacyWipSplit = wipInProgressCount > 0 || wipTestingCount > 0;
    const wipRow = `<div class="dashboard-stats__row">
    <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.wip"))}</span>
    <span class="dashboard-stats__value">${showLegacyWipSplit ? `<span class="dashboard-stats__wip-in-progress">${escapeHTML(t("dashboard.stats.inProgress"))}</span>: ${wipInProgressCount} · <span class="dashboard-stats__wip-testing">${escapeHTML(t("dashboard.stats.testing"))}</span>: ${wipTestingCount}` : wipCount}</span>
  </div>`;
    const oldestWipRow = oldestWip
        ? `<div class="dashboard-stats__row dashboard-stats__row--wip ${oldestWip.ageDays > 7 ? 'dashboard-stats__row--wip-warning' : ''}">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.oldestInProgress"))}</span>
        <div class="dashboard-stats__wip-link list__item list__item--clickable" data-open-board="${escapeHTML(oldestWip.projectSlug)}" data-open-todo-local-id="${oldestWip.localId}" role="button" tabindex="0">
          ${escapeHTML(t("dashboard.stats.oldestWipValue", { localId: oldestWip.localId, title: oldestWip.title, ageDays: oldestWip.ageDays, projectName: oldestWip.projectName }))}
        </div>
      </div>`
        : '';
    const leadTimeRow = `<div class="dashboard-stats__row">
    <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.avgLeadTime"))}</span>
    <span class="dashboard-stats__value dashboard-stats__value--num">${avgLeadTimeDays != null ? escapeHTML(t("dashboard.stats.leadTimeValue", { days: avgLeadTimeDays.toFixed(1) })) : '—'}</span>
  </div>`;
    const throughputRow = weeklyThroughput.length > 0
        ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">${escapeHTML(t("dashboard.stats.throughputLast4Weeks"))}</span>
        <div class="dashboard-throughput">${throughputBars}</div>
      </div>`
        : '';
    const currentSprintCardRows = [sprintAssignedRow, completionRateRow, completionRateAllUsersRow].filter(Boolean).join('');
    const workloadCardRows = [workloadAssignedRow, wipRow, oldestWipRow].filter(Boolean).join('');
    const flowCardRows = [leadTimeRow, throughputRow].filter(Boolean).join('');
    const hasActiveSprint = (summary?.projects ?? []).some((project) => project.activeSprint != null);
    const currentSprintSection = hasActiveSprint
        ? `<div class="dashboard-stats__section">
        <span class="dashboard-stats__label" data-i18n-text="dashboard.stats.currentSprint">${escapeHTML(t("dashboard.stats.currentSprint"))}</span>
        <div class="dashboard-stats__section-card">${currentSprintCardRows}</div>
      </div>`
        : '';
    const contentMarkup = `
    <div class="dashboard-content">
      <div class="dashboard-project-groups-wrap">
        ${leftColumnMarkup}
      </div>
      <div class="dashboard-stats">
        ${currentSprintSection}
        <div class="dashboard-stats__section dashboard-stats__section--spaced">
          <span class="dashboard-stats__label" data-i18n-text="dashboard.stats.yourWorkload">${escapeHTML(t("dashboard.stats.yourWorkload"))}</span>
          <div class="dashboard-stats__section-card">${workloadCardRows}</div>
        </div>
        <div class="dashboard-stats__section dashboard-stats__section--spaced">
          <span class="dashboard-stats__label" data-i18n-text="dashboard.stats.yourFlow">${escapeHTML(t("dashboard.stats.yourFlow"))}</span>
          <div class="dashboard-stats__section-card">${flowCardRows}</div>
        </div>
      </div>
    </div>
  `;
    app.innerHTML = `
    <div class="page page--dashboard">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        ${renderUserAvatar(getUser())}
      </div>
      <div class="container">
        <div class="panel">
          ${renderDashboardPanelHeader()}
          ${renderTopTabs()}
          ${contentMarkup}
        </div>
      </div>
    </div>
  `;
    bindTopNav();
    bindLoadMore();
    bindDashboardSort();
    bindAvatarButton();
}
function hexToRgba(hex, alpha) {
    const h = hex && hex.length === 7 && hex.startsWith("#") ? hex : "#888888";
    const r = parseInt(h.slice(1, 3), 16);
    const g = parseInt(h.slice(3, 5), 16);
    const b = parseInt(h.slice(5, 7), 16);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}
/** Group todos by project, preserving order of first occurrence of each project. */
function groupTodosByProject(todos) {
    const byId = new Map();
    const order = [];
    for (const t of todos) {
        if (!byId.has(t.projectId)) {
            byId.set(t.projectId, { projectName: t.projectName, projectSlug: t.projectSlug, projectImage: t.projectImage, dominantColor: t.projectDominantColor || "#888888", todos: [] });
            order.push(t.projectId);
        }
        byId.get(t.projectId).todos.push(t);
    }
    return order.map((pid) => {
        const g = byId.get(pid);
        return { projectId: pid, projectName: g.projectName, projectSlug: g.projectSlug, projectImage: g.projectImage, dominantColor: g.dominantColor, todos: g.todos };
    });
}
function formatSprintTooltipDateRange(startAt, endAt) {
    return `${formatLongDateWithWeekday(startAt)} - ${formatLongDateWithWeekday(endAt)}`;
}
function renderDashboardTodoGroups(todos, projectsByProjectId) {
    const groups = groupTodosByProject(todos);
    const isLight = document.documentElement.getAttribute("data-theme") === "light";
    const alpha = isLight ? 0.08 : 0.2;
    return groups
        .map((group) => {
        const project = projectsByProjectId?.get(group.projectId);
        const sprintSections = project?.sprintSections ?? [{ name: t("dashboard.sprint.unscheduled") }];
        const sectionIdSet = new Set(sprintSections.filter((s) => s.id != null).map((s) => s.id));
        const todosBySection = new Map();
        for (const todo of group.todos) {
            const key = todo.sprintId != null && sectionIdSet.has(todo.sprintId) ? todo.sprintId : null;
            const arr = todosBySection.get(key) ?? [];
            arr.push(todo);
            todosBySection.set(key, arr);
        }
        const sectionParts = [];
        for (const section of sprintSections) {
            const sectionKey = section.id ?? null;
            const sectionTodos = todosBySection.get(sectionKey) ?? [];
            if (sectionTodos.length === 0)
                continue;
            const tabLabel = section.startAt != null && section.startAt > 0 && section.endAt != null && section.endAt > 0
                ? `${escapeHTML(section.name)} · ${escapeHTML(formatSprintDateRange(section.startAt, section.endAt))}`
                : escapeHTML(section.name);
            const isDesktop = typeof window !== "undefined" && window.matchMedia("(min-width: 768px)").matches;
            const hasDates = section.startAt != null && section.startAt > 0 && section.endAt != null && section.endAt > 0;
            const tabTitle = isDesktop && hasDates
                ? `${escapeHTML(section.name)}\n${escapeHTML(formatSprintTooltipDateRange(section.startAt, section.endAt))}`
                : escapeHTML(section.name);
            const sectionKind = section.state === "ACTIVE" ? "active" : section.id != null ? "sprint" : "unscheduled";
            sectionParts.push(`
    <div class="dashboard-project-group__section" data-sprint-section="${sectionKind}">
      <div class="dashboard-project-group__tab dashboard-project-group__tab--sprint" title="${tabTitle}">${tabLabel}</div>
      <div class="list">
        ${sectionTodos.map((todo) => renderDashboardTodo(todo)).join("")}
      </div>
    </div>`);
        }
        const imgSrc = group.projectImage ? escapeHTML(group.projectImage) : "";
        const namePart = imgSrc
            ? `<img class="dashboard-project-group__tab-img" src="${imgSrc}" alt="" aria-hidden="true" /><span class="dashboard-project-group__tab-name">${escapeHTML(group.projectName)}</span>`
            : `<span class="dashboard-project-group__tab-name">${escapeHTML(group.projectName)}</span>`;
        const tint = hexToRgba(group.dominantColor || "#888888", alpha);
        return `
    <div class="dashboard-project-group" style="background: ${tint};">
      <div class="dashboard-project-group__tab list__item--clickable" data-open-board="${escapeHTML(group.projectSlug)}" role="button" tabindex="0" title="${escapeHTML(t("dashboard.project.openTitle", { name: group.projectName }))}">${namePart}</div>
      ${sectionParts.join("")}
    </div>
  `;
    })
        .join("");
}
function renderDashboardTodo(todo) {
    const updatedAt = formatDate(todo.updatedAt, {
        year: "numeric",
        month: "numeric",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
        second: "2-digit",
    });
    const pillLabel = escapeHTML(todo.statusName);
    const pillColor = sanitizeHexColor(todo.statusColor, "#64748b");
    // Match board's tag styling: border + 12.5% alpha background + text all same color
    const pillStyle = `border-color: ${pillColor}; background: ${pillColor}20; color: ${pillColor};`;
    const showPoints = todo.estimationPoints != null;
    return `
    <div class="list__item list__item--clickable" data-open-board="${escapeHTML(todo.projectSlug)}" data-open-todo-local-id="${todo.localId}" role="button" tabindex="0">
      <div class="dashboard-todo__main">
        <div class="dashboard-todo__title-row">
          <span class="card__id-inline">#${todo.localId}</span>
          <span class="dashboard-todo__title">${escapeHTML(todo.title)}</span>
          ${showPoints ? `<span class="dashboard-todo__points" aria-label="${escapeHTML(t("dashboard.todo.estimationPointsAria"))}">${todo.estimationPoints}</span>` : ''}
        </div>
      </div>
      <div class="spacer"></div>
      <div class="muted" style="font-size: 12px; text-align: right;">
        <div class="dashboard-todo__status"><span class="status-pill" style="${pillStyle}">${pillLabel}</span></div>
        <div>${escapeHTML(updatedAt)}</div>
      </div>
    </div>
  `;
}
function bindTopNav() {
    const projectsBtn = document.getElementById('projectsTabBtn');
    if (projectsBtn && !projectsBtn[BOUND_FLAG]) {
        projectsBtn.addEventListener('click', () => {
            setProjectsTab("projects");
            localStorage.setItem("projectsTab", "projects");
            navigate('/');
        });
        projectsBtn[BOUND_FLAG] = true;
    }
    const temporaryBtn = document.getElementById('temporaryTabBtn');
    if (temporaryBtn && !temporaryBtn[BOUND_FLAG]) {
        temporaryBtn.addEventListener('click', () => {
            setProjectsTab("temporary");
            localStorage.setItem("projectsTab", "temporary");
            navigate('/');
        });
        temporaryBtn[BOUND_FLAG] = true;
    }
    const goToBoard = (el) => {
        const slug = el.getAttribute('data-open-board');
        const localId = el.getAttribute('data-open-todo-local-id');
        if (slug) {
            const path = localId ? `/${slug}/t/${localId}` : `/${slug}`;
            navigate(path);
        }
    };
    document.querySelectorAll('[data-open-board]').forEach((el) => {
        if (!el[BOUND_FLAG]) {
            el.addEventListener('click', () => goToBoard(el));
            el.addEventListener('keydown', (e) => {
                const ke = e;
                if (ke.key === 'Enter' || ke.key === ' ') {
                    ke.preventDefault();
                    goToBoard(el);
                }
            });
            el[BOUND_FLAG] = true;
        }
    });
}
function bindDashboardSort() {
    const sel = document.getElementById('dashboardTodoSort');
    if (!sel || sel[BOUND_FLAG]) {
        return;
    }
    sel.addEventListener('change', async () => {
        const next = sel.value === 'board' ? 'board' : 'activity';
        const prev = getDashboardTodoSort();
        if (next === prev) {
            return;
        }
        setDashboardTodoSort(next);
        setDashboardLoading(true);
        renderDashboardContent();
        try {
            const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
            const [summary, todosResp] = await Promise.all([
                apiFetch(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
                apiFetch(`/api/dashboard/todos?${dashboardTodosQueryString()}`),
            ]);
            setDashboardSummary(summary);
            setDashboardTodos(todosResp.items || []);
            setDashboardNextCursor(todosResp.nextCursor || null);
        }
        catch (err) {
            setDashboardTodoSort(prev);
            console.error('Dashboard refetch failed:', err);
        }
        finally {
            setDashboardLoading(false);
            renderDashboardContent();
        }
    });
    sel[BOUND_FLAG] = true;
}
function bindLoadMore() {
    const wrap = document.querySelector('[data-dashboard-load-more]');
    if (!wrap || wrap[BOUND_FLAG]) {
        return;
    }
    const run = async () => {
        if (getDashboardLoading() || !getDashboardNextCursor()) {
            return;
        }
        setDashboardLoading(true);
        renderDashboardContent();
        try {
            const cursor = getDashboardNextCursor();
            const resp = await apiFetch(`/api/dashboard/todos?${dashboardTodosQueryString()}&cursor=${encodeURIComponent(cursor || '')}`);
            appendDashboardTodos(resp.items || []);
            setDashboardNextCursor(resp.nextCursor || null);
        }
        finally {
            setDashboardLoading(false);
            renderDashboardContent();
        }
    };
    const loadMoreBtn = document.getElementById('dashboardLoadMoreBtn');
    const loadMoreMobile = document.getElementById('dashboardLoadMoreMobile');
    loadMoreBtn?.addEventListener('click', run);
    loadMoreMobile?.addEventListener('click', run);
    loadMoreMobile?.addEventListener('keydown', (e) => {
        const ke = e;
        if (ke.key === 'Enter' || ke.key === ' ') {
            ke.preventDefault();
            void run();
        }
    });
    wrap[BOUND_FLAG] = true;
}
export async function renderDashboard() {
    ensureDashboardI18nBinding();
    renderLoadingShell();
    setDashboardLoading(true);
    try {
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
        const [summary, todosResp, projects] = await Promise.all([
            apiFetch(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
            apiFetch(`/api/dashboard/todos?${dashboardTodosQueryString()}`),
            apiFetch("/api/projects").catch(() => null),
        ]);
        setDashboardSummary(summary);
        setDashboardTodos(todosResp.items || []);
        setDashboardNextCursor(todosResp.nextCursor || null);
        if (projects) {
            setProjects(projects);
            ingestProjectsFromApp(projects);
        }
    }
    catch (err) {
        const e = err;
        if (e?.data?.error?.details?.detail) {
            console.error('Dashboard summary error (from API):', e.data.error.details.detail);
        }
        throw err;
    }
    finally {
        setDashboardLoading(false);
        renderDashboardContent();
    }
}
