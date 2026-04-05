import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { navigate } from '../router.js';
import { escapeHTML, renderUserAvatar, sanitizeHexColor } from '../utils.js';
import {
  getDashboardLoading,
  getDashboardNextCursor,
  getDashboardSummary,
  getDashboardTodos,
  getDashboardTodoSort,
  getProjects,
  getUser,
} from '../state/selectors.js';
import {
  appendDashboardTodos,
  setDashboardLoading,
  setDashboardNextCursor,
  setDashboardTodoSort,
  setProjects,
  setProjectsTab,
  setSettingsActiveTab,
  setDashboardSummary,
  setDashboardTodos,
} from '../state/mutations.js';
import { ingestProjectsFromApp } from '../core/notifications.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { DashboardProject, DashboardSummary, DashboardTodo, DashboardTodosResponse, Project, SprintSectionInfo } from '../types.js';
import { temporaryBoardsNavLabel } from '../nav-labels.js';

const BOUND_FLAG = Symbol('bound');

const DASHBOARD_SORT_HINT =
  "Order matches each project's board: column, then drag order. Projects appear in a fixed order (not alphabetical or by activity).";

function dashboardTodosQueryString(): string {
  let q = 'limit=20';
  if (getDashboardTodoSort() === 'board') {
    q += '&sort=board';
  }
  return q;
}

/** Narrow viewports use a shorter board option label so the select can stay compact. */
function boardOrderOptionText(): string {
  return typeof window !== 'undefined' && window.innerWidth <= 767 ? 'Board Order' : 'Board Order (per project)';
}

function renderDashboardPanelHeader(): string {
  const sort = getDashboardTodoSort();
  const hint = escapeHTML(DASHBOARD_SORT_HINT);
  const titleAttr = escapeHTML(DASHBOARD_SORT_HINT);
  const boardLabel = escapeHTML(boardOrderOptionText());
  return `
          <div class="panel__header panel__header--dashboard">
            <div class="panel__title">Dashboard</div>
            <div class="dashboard-sort">
              <label class="dashboard-sort__label" for="dashboardTodoSort">Sort</label>
              <select id="dashboardTodoSort" class="dashboard-sort__select" aria-describedby="dashboardSortHint" title="${titleAttr}">
                <option value="activity" ${sort === 'activity' ? 'selected' : ''}>Activity</option>
                <option value="board" ${sort === 'board' ? 'selected' : ''}>${boardLabel}</option>
              </select>
            </div>
          </div>
          <p id="dashboardSortHint" class="dashboard-sort__hint muted">${hint}</p>`;
}

function renderTopTabs(): string {
  const projects = getProjects() || [];
  const durableProjects = projects.filter((p: any) => !p.expiresAt);
  const temporaryBoards = projects.filter((p: any) => !!p.expiresAt);
  const temporaryLabel = temporaryBoardsNavLabel();
  return `
    <div class="chips" style="margin-top: 10px;">
      <button class="chip chip--active" id="dashboardTabBtn" type="button">Dashboard</button>
      <button class="chip" id="projectsTabBtn" type="button">
        Projects <span class="chip__count">${durableProjects.length}</span>
      </button>
      <button class="chip" id="temporaryTabBtn" type="button">
        ${temporaryLabel} <span class="chip__count">${temporaryBoards.length}</span>
      </button>
    </div>
  `;
}

function bindAvatarButton(): void {
  const userAvatarBtn = document.getElementById('userAvatarBtn');
  if (!userAvatarBtn || (userAvatarBtn as any)[BOUND_FLAG]) {
    return;
  }
  userAvatarBtn.addEventListener('click', async () => {
    setSettingsActiveTab('profile');
    await renderSettingsModal();
    (settingsDialog as HTMLDialogElement).showModal();
  });
  (userAvatarBtn as any)[BOUND_FLAG] = true;
}

function renderLoadingShell(): void {
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
            <div class="list__item"><div class="muted">Loading assigned todos...</div></div>
            <div class="list__item"><div class="muted">Loading assigned todos...</div></div>
            <div class="list__item"><div class="muted">Loading assigned todos...</div></div>
          </div>
        </div>
      </div>
    </div>
  `;
  bindTopNav();
  bindDashboardSort();
  bindAvatarButton();
}

function renderDashboardContent(): void {
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
  const projectsByProjectId = new Map<number, DashboardProject>();
  for (const p of summary?.projects ?? []) {
    projectsByProjectId.set(p.projectId, p);
  }
  const leftColumnMarkup = hasTodos
    ? `<div class="dashboard-todo-groups">${renderDashboardTodoGroups(todos, projectsByProjectId)}</div>
       ${
         nextCursor
           ? `<div style="margin-top: 12px;">
                <button class="btn" id="dashboardLoadMoreBtn" type="button" ${loading ? 'disabled' : ''}>
                  ${loading ? 'Loading...' : 'Load more'}
                </button>
              </div>`
           : ''
       }`
    : `<div class="muted" style="margin-top: 48px;">No todos assigned to you.</div>`;

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
      return `<div class="dashboard-throughput__bar" style="--bar-height: ${h}%" title="${escapeHTML(p.weekStart)}: ${p.stories} stories, ${p.points} pts"></div>`;
    })
    .join('');
  const sprintAssignedRow = assignedSplit != null
    ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">ASSIGNED</span>
        <span class="dashboard-stats__value">${assignedSplit.sprintPoints} pts · ${assignedSplit.sprintStories} todos</span>
      </div>`
    : '';

  const completionRateRow = sprintCompletion && (sprintCompletion.totalStories > 0 || sprintCompletion.totalPoints > 0)
    ? `<div class="dashboard-stats__row dashboard-stats__row--progress">
        <span class="dashboard-stats__label">YOUR COMPLETION</span>
        <span class="dashboard-stats__value">Stories: ${storiesPct}% · Points: ${pointsPct}%</span>
        <div class="dashboard-stats__progress-wrap">
          <div class="dashboard-stats__progress-bar" role="progressbar" aria-valuenow="${sprintCompletion.doneStories}" aria-valuemin="0" aria-valuemax="${sprintCompletion.totalStories}" style="--progress: ${sprintCompletion.totalStories > 0 ? (sprintCompletion.doneStories / sprintCompletion.totalStories) * 100 : 0}%"></div>
        </div>
      </div>`
    : '';

  const completionRateAllUsersRow = sprintCompletionAllUsers && (sprintCompletionAllUsers.totalStories > 0 || sprintCompletionAllUsers.totalPoints > 0)
    ? `<div class="dashboard-stats__row dashboard-stats__row--progress dashboard-stats__row--progress-team">
        <span class="dashboard-stats__label">TEAM COMPLETION</span>
        <span class="dashboard-stats__value">Stories: ${storiesPctAll}% · Points: ${pointsPctAll}%</span>
        <div class="dashboard-stats__progress-wrap">
          <div class="dashboard-stats__progress-bar" role="progressbar" aria-valuenow="${sprintCompletionAllUsers.doneStories}" aria-valuemin="0" aria-valuemax="${sprintCompletionAllUsers.totalStories}" style="--progress: ${sprintCompletionAllUsers.totalStories > 0 ? (sprintCompletionAllUsers.doneStories / sprintCompletionAllUsers.totalStories) * 100 : 0}%"></div>
        </div>
      </div>`
    : '';

  const workloadAssignedRow = assignedSplit != null
    ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">Total assigned</span>
        <span class="dashboard-stats__value dashboard-stats__value--assigned"><span class="dashboard-stats__value--total">Total:</span> ${assignedSplit.backlogPoints + assignedSplit.sprintPoints} pts · ${assignedSplit.backlogStories + assignedSplit.sprintStories} todos</span>
      </div>`
    : `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">Total assigned</span>
        <span class="dashboard-stats__value">${totalStoryPoints} pts</span>
      </div>`;

  const showLegacyWipSplit = wipInProgressCount > 0 || wipTestingCount > 0;
  const wipRow = `<div class="dashboard-stats__row">
    <span class="dashboard-stats__label">WIP</span>
    <span class="dashboard-stats__value">${showLegacyWipSplit ? `<span class="dashboard-stats__wip-in-progress">In progress</span>: ${wipInProgressCount} · <span class="dashboard-stats__wip-testing">Testing</span>: ${wipTestingCount}` : wipCount}</span>
  </div>`;

  const oldestWipRow = oldestWip
    ? `<div class="dashboard-stats__row dashboard-stats__row--wip ${oldestWip.ageDays > 7 ? 'dashboard-stats__row--wip-warning' : ''}">
        <span class="dashboard-stats__label">Oldest in progress</span>
        <div class="dashboard-stats__wip-link list__item list__item--clickable" data-open-board="${escapeHTML(oldestWip.projectSlug)}" data-open-todo-local-id="${oldestWip.localId}" role="button" tabindex="0">
          #${oldestWip.localId} ${escapeHTML(oldestWip.title)} — ${oldestWip.ageDays}d (${escapeHTML(oldestWip.projectName)})
        </div>
      </div>`
    : '';

  const leadTimeRow = `<div class="dashboard-stats__row">
    <span class="dashboard-stats__label">Avg. lead time</span>
    <span class="dashboard-stats__value dashboard-stats__value--num">${avgLeadTimeDays != null ? avgLeadTimeDays.toFixed(1) + 'd' : '—'}</span>
  </div>`;

  const throughputRow = weeklyThroughput.length > 0
    ? `<div class="dashboard-stats__row">
        <span class="dashboard-stats__label">Throughput (last 4 weeks)</span>
        <div class="dashboard-throughput">${throughputBars}</div>
      </div>`
    : '';

  const currentSprintCardRows = [sprintAssignedRow, completionRateRow, completionRateAllUsersRow].filter(Boolean).join('');
  const workloadCardRows = [workloadAssignedRow, wipRow, oldestWipRow].filter(Boolean).join('');
  const flowCardRows = [leadTimeRow, throughputRow].filter(Boolean).join('');

  const contentMarkup = `
    <div class="dashboard-content">
      <div class="dashboard-project-groups-wrap">
        ${leftColumnMarkup}
      </div>
      <div class="dashboard-stats">
        <div class="dashboard-stats__section">
          <span class="dashboard-stats__label">CURRENT SPRINT</span>
          <div class="dashboard-stats__section-card">${currentSprintCardRows}</div>
        </div>
        <div class="dashboard-stats__section dashboard-stats__section--spaced">
          <span class="dashboard-stats__label">YOUR WORKLOAD</span>
          <div class="dashboard-stats__section-card">${workloadCardRows}</div>
        </div>
        <div class="dashboard-stats__section dashboard-stats__section--spaced">
          <span class="dashboard-stats__label">YOUR FLOW</span>
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

function hexToRgba(hex: string, alpha: number): string {
  const h = hex && hex.length === 7 && hex.startsWith("#") ? hex : "#888888";
  const r = parseInt(h.slice(1, 3), 16);
  const g = parseInt(h.slice(3, 5), 16);
  const b = parseInt(h.slice(5, 7), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/** Group todos by project, preserving order of first occurrence of each project. */
function groupTodosByProject(todos: DashboardTodo[]): { projectId: number; projectName: string; projectSlug: string; projectImage?: string; dominantColor: string; todos: DashboardTodo[] }[] {
  const byId = new Map<number, { projectName: string; projectSlug: string; projectImage?: string; dominantColor: string; todos: DashboardTodo[] }>();
  const order: number[] = [];
  for (const t of todos) {
    if (!byId.has(t.projectId)) {
      byId.set(t.projectId, { projectName: t.projectName, projectSlug: t.projectSlug, projectImage: t.projectImage, dominantColor: t.projectDominantColor || "#888888", todos: [] });
      order.push(t.projectId);
    }
    byId.get(t.projectId)!.todos.push(t);
  }
  return order.map((pid) => {
    const g = byId.get(pid)!;
    return { projectId: pid, projectName: g.projectName, projectSlug: g.projectSlug, projectImage: g.projectImage, dominantColor: g.dominantColor, todos: g.todos };
  });
}

function formatSprintDateRange(startAt: number, endAt: number): string {
  const start = new Date(startAt);
  const end = new Date(endAt);
  return `${start.toLocaleDateString(undefined, { month: "short", day: "numeric" })} – ${end.toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;
}

function ordinal(n: number): string {
  const s = n % 100;
  if (s >= 11 && s <= 13) return "th";
  switch (n % 10) {
    case 1: return "st";
    case 2: return "nd";
    case 3: return "rd";
    default: return "th";
  }
}

function formatSprintTooltipDateRange(startAt: number, endAt: number): string {
  const start = new Date(startAt);
  const end = new Date(endAt);
  const fmt = (d: Date) =>
    `${d.toLocaleDateString(undefined, { weekday: "long" })}, ${d.toLocaleDateString(undefined, { month: "long" })} ${d.getDate()}${ordinal(d.getDate())} ${d.getFullYear()}`;
  return `${fmt(start)} - ${fmt(end)}`;
}

function renderDashboardTodoGroups(todos: DashboardTodo[], projectsByProjectId?: Map<number, DashboardProject>): string {
  const groups = groupTodosByProject(todos);
  const isLight = document.documentElement.getAttribute("data-theme") === "light";
  const alpha = isLight ? 0.08 : 0.2;
  return groups
    .map(
      (group) => {
        const project = projectsByProjectId?.get(group.projectId);
        const sprintSections: SprintSectionInfo[] = project?.sprintSections ?? [{ name: "Unscheduled" }];
        const sectionIdSet = new Set<number>(sprintSections.filter((s) => s.id != null).map((s) => s.id!));
        const todosBySection = new Map<number | null, DashboardTodo[]>();
        for (const todo of group.todos) {
          const key: number | null =
            todo.sprintId != null && sectionIdSet.has(todo.sprintId) ? todo.sprintId : null;
          const arr = todosBySection.get(key) ?? [];
          arr.push(todo);
          todosBySection.set(key, arr);
        }
        const sectionParts: string[] = [];
        for (const section of sprintSections) {
          const sectionKey = section.id ?? null;
          const sectionTodos = todosBySection.get(sectionKey) ?? [];
          if (sectionTodos.length === 0) continue;
          const tabLabel =
            section.startAt != null && section.startAt > 0 && section.endAt != null && section.endAt > 0
              ? `${escapeHTML(section.name)} · ${escapeHTML(formatSprintDateRange(section.startAt, section.endAt))}`
              : escapeHTML(section.name);
          const isDesktop = typeof window !== "undefined" && window.matchMedia("(min-width: 768px)").matches;
          const hasDates = section.startAt != null && section.startAt > 0 && section.endAt != null && section.endAt > 0;
          const tabTitle =
            isDesktop && hasDates
              ? `${escapeHTML(section.name)}\n${escapeHTML(formatSprintTooltipDateRange(section.startAt, section.endAt))}`
              : escapeHTML(section.name);
          const sectionKind =
            section.state === "ACTIVE" ? "active" : section.id != null ? "sprint" : "unscheduled";
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
      <div class="dashboard-project-group__tab list__item--clickable" data-open-board="${escapeHTML(group.projectSlug)}" role="button" tabindex="0" title="Open ${escapeHTML(group.projectName)}">${namePart}</div>
      ${sectionParts.join("")}
    </div>
  `;
      }
    )
    .join("");
}

function renderDashboardTodo(todo: DashboardTodo): string {
  const updatedAt = new Date(todo.updatedAt).toLocaleString();
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
          ${showPoints ? `<span class="dashboard-todo__points" aria-label="Estimation points">${todo.estimationPoints}</span>` : ''}
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

function bindTopNav(): void {
  const projectsBtn = document.getElementById('projectsTabBtn');
  if (projectsBtn && !(projectsBtn as any)[BOUND_FLAG]) {
    projectsBtn.addEventListener('click', () => {
      setProjectsTab("projects");
      localStorage.setItem("projectsTab", "projects");
      navigate('/');
    });
    (projectsBtn as any)[BOUND_FLAG] = true;
  }
  const temporaryBtn = document.getElementById('temporaryTabBtn');
  if (temporaryBtn && !(temporaryBtn as any)[BOUND_FLAG]) {
    temporaryBtn.addEventListener('click', () => {
      setProjectsTab("temporary");
      localStorage.setItem("projectsTab", "temporary");
      navigate('/');
    });
    (temporaryBtn as any)[BOUND_FLAG] = true;
  }

  const goToBoard = (el: Element) => {
    const slug = el.getAttribute('data-open-board');
    const localId = el.getAttribute('data-open-todo-local-id');
    if (slug) {
      const path = localId ? `/${slug}/t/${localId}` : `/${slug}`;
      navigate(path);
    }
  };
  document.querySelectorAll('[data-open-board]').forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener('click', () => goToBoard(el));
      el.addEventListener('keydown', (e: Event) => {
        const ke = e as KeyboardEvent;
        if (ke.key === 'Enter' || ke.key === ' ') {
          ke.preventDefault();
          goToBoard(el);
        }
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });
}

function bindDashboardSort(): void {
  const sel = document.getElementById('dashboardTodoSort') as HTMLSelectElement | null;
  if (!sel || (sel as any)[BOUND_FLAG]) {
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
        apiFetch<DashboardSummary>(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
        apiFetch<DashboardTodosResponse>(`/api/dashboard/todos?${dashboardTodosQueryString()}`),
      ]);
      setDashboardSummary(summary);
      setDashboardTodos(todosResp.items || []);
      setDashboardNextCursor(todosResp.nextCursor || null);
    } catch (err: unknown) {
      setDashboardTodoSort(prev);
      console.error('Dashboard refetch failed:', err);
    } finally {
      setDashboardLoading(false);
      renderDashboardContent();
    }
  });
  (sel as any)[BOUND_FLAG] = true;
}

function bindLoadMore(): void {
  const loadMoreBtn = document.getElementById('dashboardLoadMoreBtn');
  if (!loadMoreBtn || (loadMoreBtn as any)[BOUND_FLAG]) {
    return;
  }
  loadMoreBtn.addEventListener('click', async () => {
    if (getDashboardLoading() || !getDashboardNextCursor()) {
      return;
    }
    setDashboardLoading(true);
    renderDashboardContent();
    try {
      const cursor = getDashboardNextCursor();
      const resp = await apiFetch<DashboardTodosResponse>(
        `/api/dashboard/todos?${dashboardTodosQueryString()}&cursor=${encodeURIComponent(cursor || '')}`,
      );
      appendDashboardTodos(resp.items || []);
      setDashboardNextCursor(resp.nextCursor || null);
    } finally {
      setDashboardLoading(false);
      renderDashboardContent();
    }
  });
  (loadMoreBtn as any)[BOUND_FLAG] = true;
}

export async function renderDashboard(): Promise<void> {
  renderLoadingShell();
  setDashboardLoading(true);
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const [summary, todosResp, projects] = await Promise.all([
      apiFetch<DashboardSummary>(`/api/dashboard/summary?tz=${encodeURIComponent(tz)}`),
      apiFetch<DashboardTodosResponse>(`/api/dashboard/todos?${dashboardTodosQueryString()}`),
      apiFetch<Project[]>("/api/projects").catch(() => null),
    ]);
    setDashboardSummary(summary);
    setDashboardTodos(todosResp.items || []);
    setDashboardNextCursor(todosResp.nextCursor || null);
    if (projects) {
      setProjects(projects);
      ingestProjectsFromApp(projects);
    }
  } catch (err: unknown) {
    const e = err as Error & { data?: { error?: { details?: { detail?: string } } } };
    if (e?.data?.error?.details?.detail) {
      console.error('Dashboard summary error (from API):', e.data.error.details.detail);
    }
    throw err;
  } finally {
    setDashboardLoading(false);
    renderDashboardContent();
  }
}
