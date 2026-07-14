import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { ingestProjectsFromApp } from '../core/notifications.js';
import { apiErrorMessage, I18N_LOCALE_CHANGED, t } from '../i18n/index.js';
import { temporaryBoardsNavLabelKey } from '../nav-labels.js';
import { navigate } from '../router.js';
import type { Board } from '../types.js';
import { escapeHTML, showToast, renderUserAvatar, confirmDelete, showPromptDialog } from '../utils.js';
import { FIELD_TOOLTIPS, titleAttr } from '../field-tooltips.js';
import {
  getProjectsTab,
  getProjectView,
  getProjects,
  getUser,
  getOidcEnabled,
  getLocalAuthEnabled,
  getSelfServicePasswordResetEnabled,
} from '../state/selectors.js';
import {
  setProjects,
  setProjectsTab,
  setProjectView,
  setSettingsActiveTab,
} from '../state/mutations.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { CreateProjectPayload, Project, WorkflowLaneDraft } from '../types.js';

// Symbol for idempotent listener attachment
const BOUND_FLAG = Symbol('bound');
declare const Sortable: any;

// Board prefetch cache for Projects → Board navigation (hover to prefetch, click to use)
const boardPrefetchPromises = new Map<string, Promise<Board>>();
const resolvedBoardBySlug = new Map<string, Board>();
const PREFETCH_DELAY_MS = 250;
let projectsI18nBound = false;

const DEFAULT_WORKFLOW_LANES: WorkflowLaneDraft[] = [
  { key: "backlog", name: "Backlog", color: "#9CA3AF", position: 0, isDone: false },
  { key: "not_started", name: "Not Started", color: "#F59E0B", position: 1, isDone: false },
  { key: "doing", name: "In Progress", color: "#10B981", position: 2, isDone: false },
  { key: "testing", name: "Testing", color: "#3B82F6", position: 3, isDone: false },
  { key: "done", name: "Done", color: "#EF4444", position: 4, isDone: true },
];

let workflowLanes: WorkflowLaneDraft[] = DEFAULT_WORKFLOW_LANES.map((l) => ({ ...l }));

function getInitialWorkflowTemplate(): WorkflowLaneDraft[] {
  return DEFAULT_WORKFLOW_LANES;
}

function cloneWorkflow(template: WorkflowLaneDraft[]): WorkflowLaneDraft[] {
  return template.map((l) => ({ ...l }));
}

function resetWorkflowEditorState(): void {
  workflowLanes = DEFAULT_WORKFLOW_LANES.map((l) => ({ ...l }));
}

function resequenceWorkflowPositions(lanes: WorkflowLaneDraft[]): void {
  lanes.forEach((lane, idx) => { lane.position = idx; });
}

function keyFromLaneName(name: string): string {
  const normalized = name
    .toLowerCase()
    .trim()
    .replace(/\s+/g, "_")
    .replace(/[^a-z0-9_]+/g, "_")
    .replace(/_+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 32)
    .replace(/^_+|_+$/g, "");
  return normalized || "lane";
}

function makeUniqueLaneKey(baseKey: string, lanes: WorkflowLaneDraft[]): string {
  const used = new Set(lanes.map((l) => l.key));
  if (!used.has(baseKey)) return baseKey;
  let n = 2;
  while (n < 1000) {
    const suffix = `_${n}`;
    const candidate = baseKey.length + suffix.length > 32
      ? `${baseKey.slice(0, 32 - suffix.length)}${suffix}`
      : `${baseKey}${suffix}`;
    if (!used.has(candidate)) return candidate;
    n++;
  }
  return `${Date.now()}`.slice(-8);
}

function insertLaneBeforeDone(lanes: WorkflowLaneDraft[], name: string): string {
  const laneName = name.trim();
  const key = makeUniqueLaneKey(keyFromLaneName(laneName), lanes);
  const newLane: WorkflowLaneDraft = { key, name: laneName, color: "#64748b", position: 0, isDone: false };
  const doneIdx = lanes.findIndex((l) => l.isDone);
  const insertIdx = doneIdx >= 0 ? doneIdx : lanes.length;
  lanes.splice(insertIdx, 0, newLane);
  resequenceWorkflowPositions(lanes);
  return key;
}

function validateWorkflowLanes(lanes: WorkflowLaneDraft[]): string | null {
  if (lanes.length < 2) return t("projects.workflow.validation.minLanes");
  const keyRe = /^[a-z0-9](?:[a-z0-9_]*[a-z0-9])?$/;
  const colorRe = /^#[0-9a-fA-F]{6}$/;
  let doneCount = 0;
  const seen = new Set<string>();
  for (const lane of lanes) {
    if (!lane.name || lane.name.trim() === "") return t("projects.workflow.validation.emptyName");
    if (!keyRe.test(lane.key)) return t("projects.workflow.validation.invalidKey");
    if (!colorRe.test(lane.color || "")) return t("projects.workflow.validation.invalidColor");
    if (seen.has(lane.key)) return t("projects.workflow.validation.duplicateKey");
    seen.add(lane.key);
    if (lane.isDone) doneCount++;
  }
  if (doneCount !== 1) return t("projects.workflow.validation.exactlyOneDone");
  return null;
}

function renderWorkflowEditorBody(
  lanes: WorkflowLaneDraft[],
  listId = "workflowModalLaneList",
  ghostId = "workflowModalGhostInput"
): string {
  const addLaneAriaLabel = escapeHTML(t("projects.workflow.addLaneAriaLabel"));
  return `
    <div class="muted" style="margin-bottom: 8px;" data-i18n-text="projects.workflow.helper">${escapeHTML(t("projects.workflow.helper"))}</div>
    <div id="${escapeHTML(listId)}">
      ${lanes.map((lane) => `
        <div class="row" style="margin-bottom:6px;" data-lane-key="${escapeHTML(lane.key)}">
          <button type="button" class="btn btn--ghost btn--small workflow-lane__handle" aria-label="${escapeHTML(t("projects.workflow.reorderLane"))}">↕</button>
          <input class="input" data-lane-name="${escapeHTML(lane.key)}" value="${escapeHTML(lane.name)}" maxlength="50" />
          <input type="color" class="settings-color-picker" data-lane-color="${escapeHTML(lane.key)}" value="${escapeHTML(lane.color || "#64748b")}" aria-label="${escapeHTML(t("projects.workflow.laneColor", { name: lane.name }))}" />
          <label class="muted workflow-done-label-wrap" style="display:flex; align-items:center; gap:4px;"${titleAttr(FIELD_TOOLTIPS.doneLane)}>
            <input type="radio" name="workflowModalDoneLane" data-lane-done="${escapeHTML(lane.key)}" ${lane.isDone ? "checked" : ""} aria-label="${escapeHTML(t("projects.workflow.setDoneLane", { name: lane.name }))}" />
            <span class="workflow-done-label" data-i18n-text="projects.workflow.doneLabel">${escapeHTML(t("projects.workflow.doneLabel"))}</span>
          </label>
          <button class="btn btn--ghost btn--small" type="button" data-lane-remove="${escapeHTML(lane.key)}" ${lanes.length <= 2 || lane.isDone ? "disabled" : ""}>×</button>
        </div>
      `).join("")}
    </div>
    <div class="row" style="gap:8px;align-items:center;">
      <input class="input" id="${escapeHTML(ghostId)}" placeholder="${escapeHTML(t("projects.workflow.addLanePlaceholder"))}" data-i18n-placeholder="projects.workflow.addLanePlaceholder" style="flex:1;min-width:0;"${titleAttr(FIELD_TOOLTIPS.workflowAddLane)} />
      <button type="button" class="btn btn--small" id="workflowModalGhostAdd" aria-label="${addLaneAriaLabel}" data-i18n-aria-label="projects.workflow.addLaneAriaLabel" data-i18n-text="projects.workflow.addLaneAction">${escapeHTML(t("projects.workflow.addLaneAction"))}</button>
    </div>
  `;
}

function createWorkflowEditorRenderer() {
  let sortableInstance: any = null;
  return {
    render(container: HTMLElement, lanes: WorkflowLaneDraft[], onLanesChange: () => void): void {
      if (sortableInstance) {
        try { sortableInstance.destroy(); } catch (_) { /* noop */ }
        sortableInstance = null;
      }
      const listId = "workflowModalLaneList";
      const ghostId = "workflowModalGhostInput";
      container.innerHTML = renderWorkflowEditorBody(lanes, listId, ghostId);

      const list = container.querySelector(`#${listId}`);
      const ghostInput = container.querySelector(`#${ghostId}`) as HTMLInputElement | null;

      container.querySelectorAll("[data-lane-name]").forEach((el) => {
        const input = el as HTMLInputElement;
        input.addEventListener("input", () => {
          const key = input.getAttribute("data-lane-name");
          const lane = lanes.find((l) => l.key === key);
          if (lane) lane.name = input.value;
          /* Do not call onLanesChange() here: it re-renders the whole editor and steals focus. */
        });
        input.addEventListener("blur", () => {
          const key = input.getAttribute("data-lane-name");
          const lane = lanes.find((l) => l.key === key);
          if (!lane) return;
          lane.name = lane.name.trim();
          if (!lane.name) lane.name = "Untitled";
          if (input.value !== lane.name) input.value = lane.name;
          /* No re-render needed; keep focus behavior intact. */
        });
      });

      container.querySelectorAll("[data-lane-done]").forEach((el) => {
        (el as HTMLInputElement).addEventListener("change", () => {
          const key = (el as HTMLElement).getAttribute("data-lane-done");
          lanes.forEach((lane) => { lane.isDone = lane.key === key; });
          resequenceWorkflowPositions(lanes);
          onLanesChange();
        });
      });

      container.querySelectorAll("[data-lane-color]").forEach((el) => {
        (el as HTMLInputElement).addEventListener("input", () => {
          const key = (el as HTMLElement).getAttribute("data-lane-color");
          const lane = lanes.find((l) => l.key === key);
          if (lane) lane.color = (el as HTMLInputElement).value || "#64748b";
          /* Do not call onLanesChange() here: it re-renders and steals focus. */
        });
      });

      container.querySelectorAll("[data-lane-remove]").forEach((el) => {
        (el as HTMLElement).addEventListener("click", () => {
          const key = (el as HTMLElement).getAttribute("data-lane-remove");
          if (!key) return;
          const target = lanes.find((lane) => lane.key === key);
          if (!target || target.isDone || lanes.length <= 2) return;
          lanes.splice(lanes.indexOf(target), 1);
          resequenceWorkflowPositions(lanes);
          onLanesChange();
        });
      });

      const ghostAddBtn = container.querySelector("#workflowModalGhostAdd") as HTMLButtonElement | null;
      if (ghostInput) {
        const commitGhostLane = () => {
          const text = (ghostInput.value || "").trim();
          if (!text) return;
          insertLaneBeforeDone(lanes, text);
          ghostInput.value = "";
          onLanesChange();
          requestAnimationFrame(() => {
            const next = container.querySelector(`#${ghostId}`) as HTMLInputElement | null;
            next?.focus();
          });
        };
        ghostInput.addEventListener("keydown", (e) => {
          if (e.key === "Enter") { e.preventDefault(); commitGhostLane(); }
        });
        ghostAddBtn?.addEventListener("click", () => { commitGhostLane(); });
      }

      if (list && typeof Sortable !== "undefined") {
        sortableInstance = Sortable.create(list, {
          handle: ".workflow-lane__handle",
          animation: 120,
          onEnd: () => {
            const keyOrder = Array.from(list.querySelectorAll("[data-lane-key]"))
              .map((el) => el.getAttribute("data-lane-key"))
              .filter((v): v is string => !!v);
            const byKey = new Map(lanes.map((l) => [l.key, l]));
            lanes.length = 0;
            lanes.push(...keyOrder.map((key) => byKey.get(key)).filter((v): v is WorkflowLaneDraft => !!v));
            resequenceWorkflowPositions(lanes);
            onLanesChange();
          },
        });
      }
    },
    destroy(): void {
      if (sortableInstance) {
        try { sortableInstance.destroy(); } catch (_) { /* noop */ }
        sortableInstance = null;
      }
    },
  };
}

function openWorkflowSetupModal(projectName: string): void {
  const modalLanes = cloneWorkflow(getInitialWorkflowTemplate());
  const editor = createWorkflowEditorRenderer();

  const dialog = document.createElement("dialog");
  dialog.className = "dialog";
  dialog.innerHTML = `
    <div class="dialog__form">
      <div class="dialog__header">
        <div class="dialog__title" data-i18n-text="projects.workflow.title">${escapeHTML(t("projects.workflow.title"))}</div>
      </div>
      <div class="dialog__content" id="workflowModalContent"></div>
      <div class="dialog__footer">
        <button type="button" class="btn btn--ghost" id="workflowModalCancel" data-i18n-text="projects.workflow.cancelAction">${escapeHTML(t("projects.workflow.cancelAction"))}</button>
        <button type="button" class="btn" id="workflowModalConfirm" data-i18n-text="projects.workflow.confirmAction">${escapeHTML(t("projects.workflow.confirmAction"))}</button>
      </div>
    </div>
  `;
  document.body.appendChild(dialog);

  const contentDiv = dialog.querySelector("#workflowModalContent") as HTMLElement;
  const cancelBtn = dialog.querySelector("#workflowModalCancel") as HTMLButtonElement;
  const confirmBtn = dialog.querySelector("#workflowModalConfirm") as HTMLButtonElement;

  const refresh = () => editor.render(contentDiv, modalLanes, refresh);
  refresh();

  const cleanup = () => {
    editor.destroy();
    dialog.remove();
  };

  dialog.addEventListener("close", cleanup);
  dialog.addEventListener("cancel", cleanup);

  cancelBtn.addEventListener("click", () => {
    cleanup();
    document.getElementById("projectName")?.focus();
  });

  confirmBtn.addEventListener("click", async () => {
    if (confirmBtn.disabled) return;

    const ghostInput = contentDiv.querySelector("#workflowModalGhostInput") as HTMLInputElement | null;
    const commitGhostLane = () => {
      if (!ghostInput) return;
      const text = (ghostInput.value || "").trim();
      if (!text) return;
      insertLaneBeforeDone(modalLanes, text);
      ghostInput.value = "";
    };
    commitGhostLane();

    const err = validateWorkflowLanes(modalLanes);
    if (err) {
      showToast(err);
      return;
    }

    modalLanes.forEach((l) => { l.name = l.name.trim(); });
    const payload: CreateProjectPayload = {
      name: projectName.trim(),
      workflow: modalLanes.map((l, i) => ({
        key: l.key,
        name: l.name.trim(),
        color: l.color,
        position: i,
        isDone: l.isDone,
      })),
    };

    confirmBtn.textContent = t("projects.workflow.creating");
    confirmBtn.setAttribute("data-i18n-text", "projects.workflow.creating");
    confirmBtn.disabled = true;

    try {
      const p = await apiFetch<Project>("/api/projects", { method: "POST", body: JSON.stringify(payload) });
      resetWorkflowEditorState();
      cleanup();
      navigate(`/${p.slug}`);
    } catch (err: any) {
      showToast(apiErrorMessage(err, { fallbackKey: "projects.create.failed" }));
      confirmBtn.textContent = t("projects.workflow.confirmAction");
      confirmBtn.setAttribute("data-i18n-text", "projects.workflow.confirmAction");
      confirmBtn.disabled = false;
    }
  });

  (dialog as HTMLDialogElement).showModal();

  const firstLaneInput = contentDiv.querySelector("[data-lane-name]") as HTMLInputElement | null;
  const ghostInput = contentDiv.querySelector("#workflowModalGhostInput") as HTMLInputElement | null;
  (firstLaneInput || ghostInput)?.focus();
}

// Declare renderAuth function (will be available after Step 3)
type RenderAuthOptions = {
  next: string;
  bootstrap?: boolean;
  oidcEnabled?: boolean;
  localAuthEnabled?: boolean;
  selfServicePasswordResetEnabled?: boolean;
};

declare function renderAuth(opts: RenderAuthOptions): void;

// Runtime access to renderAuth from auth view (after Step 3)
async function getRenderAuth(): Promise<(opts: RenderAuthOptions) => void> {
  try {
    // @ts-ignore - auth.js will exist after Step 3
    const authModule = await import('./auth.js');
    return authModule.renderAuth;
  } catch {
    return (window as any).renderAuth || renderAuth;
  }
}

function ensureProjectsI18nBinding(): void {
  if (projectsI18nBound) return;
  projectsI18nBound = true;
  document.addEventListener(I18N_LOCALE_CHANGED, () => {
    if (!document.querySelector(".page--projects")) return;
    const projects = getProjects();
    if (!Array.isArray(projects)) return;
    renderProjectsContent(projects);
  });
}

function renderProjectsContent(projects: Project[]): void {
  if (!getProjectsTab()) {
    setProjectsTab(localStorage.getItem("projectsTab") || "projects");
  }

  const durableProjects = projects.filter((p) => !p.expiresAt);
  const temporaryBoards = projects.filter((p) => !!p.expiresAt);
  const activeList = getProjectsTab() === "temporary" ? temporaryBoards : durableProjects;
  const emptyKey = getProjectsTab() === "temporary" ? "projects.empty.temporary" : "projects.empty.projects";
  const createButtonKey =
    getProjectsTab() === "temporary"
      ? "projects.actions.createTemporaryBoard"
      : "projects.actions.create";
  const temporaryLabelKey = temporaryBoardsNavLabelKey();
  const tabsHTML = `
    <div class="chips" style="margin-top: 10px; margin-bottom: 12px;">
      <button class="chip" id="dashboardTabBtn" type="button" data-i18n-text="projects.tabs.dashboard">
        ${escapeHTML(t("projects.tabs.dashboard"))}
      </button>
      <button class="chip ${getProjectsTab() === "projects" ? "chip--active" : ""}" data-projects-tab="projects">
        <span class="projects-tab__label" data-i18n-text="projects.tabs.projects">${escapeHTML(t("projects.tabs.projects"))}</span>
        <span class="chip__count">${durableProjects.length}</span>
      </button>
      <button class="chip ${getProjectsTab() === "temporary" ? "chip--active" : ""}" data-projects-tab="temporary">
        <span class="projects-tab__label" data-i18n-text="${temporaryLabelKey}">${escapeHTML(t(temporaryLabelKey))}</span>
        <span class="chip__count">${temporaryBoards.length}</span>
      </button>
    </div>
  `;

  app.innerHTML = `
    <div class="page page--projects">
      <div class="topbar">
        <div class="brand">
          <img src="/scrumboytext.png" alt="Scrumboy" class="brand-text" />
        </div>
        <div class="spacer"></div>
        ${!getUser() ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="${escapeHTML(t("projects.actions.settings"))}" data-i18n-aria-label="projects.actions.settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${renderUserAvatar(getUser())}
      </div>

      <div class="container">
        <div class="panel">
          <div class="panel__header">
          <div class="panel__title" data-i18n-text="projects.title">${escapeHTML(t("projects.title"))}</div>
            <div class="view-toggle">
              <button class="btn btn--ghost btn--small view-toggle-btn ${getProjectView() === 'list' ? 'view-toggle-btn--active' : ''}" data-view="list" title="${escapeHTML(t("projects.view.list"))}" data-i18n-title="projects.view.list">
                <span>☰</span>
              </button>
              <button class="btn btn--ghost btn--small view-toggle-btn ${getProjectView() === 'grid' ? 'view-toggle-btn--active' : ''}" data-view="grid" title="${escapeHTML(t("projects.view.grid"))}" data-i18n-title="projects.view.grid">
                <span>⊞</span>
              </button>
            </div>
          </div>
          ${tabsHTML}
          <form id="createProjectForm" class="row">
            ${getProjectsTab() === "temporary" ? `<div class="spacer"></div>` : `<input class="input" id="projectName" placeholder="${escapeHTML(t("projects.fields.namePlaceholder"))}" data-i18n-placeholder="projects.fields.namePlaceholder" maxlength="200" required />`}
            <button class="btn" type="submit" data-i18n-text="${createButtonKey}">${escapeHTML(t(createButtonKey))}</button>
          </form>
          <div class="${getProjectView() === 'grid' ? 'project-grid' : 'list'}" id="projectList">
            ${activeList.length === 0 ? `<div class="muted" data-i18n-text="${emptyKey}">${escapeHTML(t(emptyKey))}</div>` : ""}
            ${activeList
              .map(
                (p) => {
                  const displayName = p.expiresAt ? `${p.name} (${p.slug})` : p.name;
                  const isMaintainer = (p.role || "").toLowerCase() === "maintainer";
                  const maintainerActions = isMaintainer ? `
                    <button class="btn btn--ghost btn--small" data-rename="${p.id}" title="${escapeHTML(t("projects.actions.renameProject"))}" data-i18n-title="projects.actions.renameProject" data-i18n-text="projects.actions.rename">${escapeHTML(t("projects.actions.rename"))}</button>
                    <button class="btn btn--danger btn--small" data-del="${p.id}" data-i18n-text="projects.actions.delete">${escapeHTML(t("projects.actions.delete"))}</button>` : "";
                  return getProjectView() === 'grid' ? `
              <div class="project-grid__item">
                <button class="project-grid__image-btn" data-open="${p.slug}" title="${escapeHTML(displayName)}">
                  ${p.image ? `<img src="${escapeHTML(p.image)}" alt="" class="project-grid__image" />` : `<span class="project-grid__placeholder">📷</span>`}
                </button>
                <div class="project-grid__content">
                  <button class="project-grid__name" data-open="${p.slug}">${escapeHTML(displayName)}</button>
                  <div class="project-grid__actions">
                    ${maintainerActions}
                  </div>
                </div>
              </div>
            ` : `
              <div class="list__item" data-open="${p.slug}">
                <button class="btn btn--ghost project-image-btn" title="${escapeHTML(displayName)}">
                  ${p.image ? `<img src="${escapeHTML(p.image)}" alt="" class="project-image" />` : `<span class="project-image-placeholder">📷</span>`}
                </button>
                <div class="list__item-name">${escapeHTML(displayName)}</div>
                <div class="spacer"></div>
                ${maintainerActions}
              </div>
            `;
                }
              )
              .join("")}
          </div>
        </div>
      </div>
    </div>
  `;

  document.querySelectorAll("[data-projects-tab]").forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener("click", async () => {
        const tab = el.getAttribute("data-projects-tab");
        if (tab !== "projects" && tab !== "temporary") return;
        setProjectsTab(tab);
        localStorage.setItem("projectsTab", tab);
        
        // Save to backend if user is logged in
        if (getUser()) {
          try {
            await apiFetch("/api/user/preferences", {
              method: "PUT",
              body: JSON.stringify({ key: "projectsTab", value: tab }),
            });
          } catch (err) {
            // Ignore errors saving preferences
          }
        }
        
        renderProjects();
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });

  const dashboardTabBtn = document.getElementById("dashboardTabBtn");
  if (dashboardTabBtn && !(dashboardTabBtn as any)[BOUND_FLAG]) {
    dashboardTabBtn.addEventListener("click", () => {
      navigate("/dashboard");
    });
    (dashboardTabBtn as any)[BOUND_FLAG] = true;
  }

  const createProjectForm = document.getElementById("createProjectForm");
  if (createProjectForm && !(createProjectForm as any)[BOUND_FLAG]) {
    createProjectForm.addEventListener("submit", (e) => {
      e.preventDefault();
      if (getProjectsTab() === "temporary") {
        window.location.href = "/anon";
        return;
      }
      const projectNameEl = document.getElementById("projectName") as HTMLInputElement;
      const name = (projectNameEl?.value || "").trim();
      if (!name) {
        showToast(t("projects.validation.nameRequired"));
        return;
      }
      openWorkflowSetupModal(name);
    });
    (createProjectForm as any)[BOUND_FLAG] = true;
  }

  let hoverTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let hoverSlug: string | null = null;
  document.querySelectorAll("[data-open]").forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener("mouseenter", () => {
        const slug = el.getAttribute("data-open");
        if (!slug) return;
        hoverSlug = slug;
        hoverTimeoutId = setTimeout(() => {
          hoverTimeoutId = null;
          if (!boardPrefetchPromises.has(slug)) {
            const p = apiFetch<Board>(`/api/board/${slug}?limitPerLane=20`);
            boardPrefetchPromises.set(slug, p);
            p.then((board) => resolvedBoardBySlug.set(slug, board)).catch(() => {});
          }
        }, PREFETCH_DELAY_MS);
      });
      el.addEventListener("mouseleave", () => {
        if (el.getAttribute("data-open") === hoverSlug) {
          if (hoverTimeoutId) {
            clearTimeout(hoverTimeoutId);
            hoverTimeoutId = null;
          }
          hoverSlug = null;
        }
      });
      el.addEventListener("click", async (e) => {
        const slug = el.getAttribute("data-open");
        console.log("Project clicked, slug:", slug);
        if (slug) {
          const board = resolvedBoardBySlug.get(slug);
          if (board) {
            resolvedBoardBySlug.delete(slug);
            navigate(`/${slug}`, { state: { boardData: board } });
          } else {
            navigate(`/${slug}`);
          }
        } else {
          console.error("No slug found on clicked element");
        }
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });

  document.querySelectorAll("[data-rename]").forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener("click", async (e) => {
        e.stopPropagation(); // Prevent navigation when clicking rename
        const id = parseInt(el.getAttribute("data-rename") || "");
        const project = projects.find((p) => p.id === id);
        if (!project) return;

        const nextName = await showPromptDialog({
          title: t("projects.rename.title"),
          label: t("projects.rename.label"),
          initialValue: project.name,
          confirmLabel: t("projects.rename.confirmAction"),
          placeholder: t("projects.rename.placeholder"),
          maxLength: 200,
        });
        const newName = nextName?.trim() ?? "";
        if (!newName || newName === project.name) return;

        try {
          await apiFetch(`/api/projects/${id}`, {
            method: "PATCH",
            body: JSON.stringify({ name: newName }),
          });
          await renderProjects();
          showToast(t("projects.rename.success"));
        } catch (err: any) {
          showToast(apiErrorMessage(err, { fallbackKey: "projects.rename.failed" }));
        }
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });

  document.querySelectorAll("[data-del]").forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener("click", async (e) => {
        e.stopPropagation(); // Prevent navigation when clicking delete
        const id = el.getAttribute("data-del");
        if (!await confirmDelete(t("projects.delete.confirmMessage"))) return;
        try {
          await apiFetch(`/api/projects/${id}`, { method: "DELETE" });
          await renderProjects();
        } catch (err: any) {
          showToast(apiErrorMessage(err, { fallbackKey: "projects.delete.failed" }));
        }
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });


  document.querySelectorAll(".view-toggle-btn").forEach((el) => {
    if (!(el as any)[BOUND_FLAG]) {
      el.addEventListener("click", async () => {
        const view = el.getAttribute("data-view");
        setProjectView(view as any);
        localStorage.setItem("projectView", view || "");
        
        // Save to backend if user is logged in
        if (getUser()) {
          try {
            await apiFetch("/api/user/preferences", {
              method: "PUT",
              body: JSON.stringify({ key: "projectView", value: view || "" }),
            });
          } catch (err) {
            // Ignore errors saving preferences
          }
        }
        
        renderProjects();
      });
      (el as any)[BOUND_FLAG] = true;
    }
  });

  const settingsBtn = document.getElementById("settingsBtn");
  if (settingsBtn && !(settingsBtn as any)[BOUND_FLAG]) {
    settingsBtn.addEventListener("click", async () => {
      await renderSettingsModal();
      (settingsDialog as HTMLDialogElement).showModal();
    });
    (settingsBtn as any)[BOUND_FLAG] = true;
  }

  const userAvatarBtn = document.getElementById("userAvatarBtn");
  if (userAvatarBtn && !(userAvatarBtn as any)[BOUND_FLAG]) {
    userAvatarBtn.addEventListener("click", async () => {
      setSettingsActiveTab("profile");
      await renderSettingsModal();
      (settingsDialog as HTMLDialogElement).showModal();
    });
    (userAvatarBtn as any)[BOUND_FLAG] = true;
  }
}

export async function renderProjects(): Promise<void> {
  ensureProjectsI18nBinding();
  let projects: Project[];
  try {
    projects = await apiFetch<Project[]>("/api/projects");
  } catch (err: any) {
    if (err && err.status === 401) {
      const renderAuth = await getRenderAuth();
      renderAuth({
        next: "/",
        oidcEnabled: getOidcEnabled(),
        localAuthEnabled: getLocalAuthEnabled(),
        selfServicePasswordResetEnabled: getSelfServicePasswordResetEnabled(),
      });
      return;
    }
    throw err;
  }
  // Cache for settings modal: in full mode tags are global but the legacy tag APIs require a project id.
  setProjects(projects);
  ingestProjectsFromApp(projects);
  renderProjectsContent(projects);
}
