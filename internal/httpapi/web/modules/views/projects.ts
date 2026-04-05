import { app, settingsDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { ingestProjectsFromApp } from '../core/notifications.js';
import { navigate } from '../router.js';
import type { Board } from '../types.js';
import { escapeHTML, showToast, renderUserAvatar } from '../utils.js';
import {
  getProjectsTab,
  getProjectView,
  getProjects,
  getUser,
} from '../state/selectors.js';
import {
  setProjects,
  setProjectsTab,
  setProjectView,
  setSettingsActiveTab,
} from '../state/mutations.js';
import { renderSettingsModal } from '../dialogs/settings.js';
import { CreateProjectPayload, Project, WorkflowLaneDraft } from '../types.js';
import { temporaryBoardsNavLabel } from '../nav-labels.js';

// Symbol for idempotent listener attachment
const BOUND_FLAG = Symbol('bound');
declare const Sortable: any;

// Board prefetch cache for Projects → Board navigation (hover to prefetch, click to use)
const boardPrefetchPromises = new Map<string, Promise<Board>>();
const resolvedBoardBySlug = new Map<string, Board>();
const PREFETCH_DELAY_MS = 250;

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
  if (lanes.length < 2) return "Workflow must have at least 2 lanes.";
  const keyRe = /^[a-z0-9](?:[a-z0-9_]*[a-z0-9])?$/;
  const colorRe = /^#[0-9a-fA-F]{6}$/;
  let doneCount = 0;
  const seen = new Set<string>();
  for (const lane of lanes) {
    if (!lane.name || lane.name.trim() === "") return "Lane names cannot be empty.";
    if (!keyRe.test(lane.key)) return "Lane keys must be snake_case (letters, numbers, underscore).";
    if (!colorRe.test(lane.color || "")) return "Lane colors must be valid hex colors.";
    if (seen.has(lane.key)) return "Duplicate lane keys. Rename lanes to fix.";
    seen.add(lane.key);
    if (lane.isDone) doneCount++;
  }
  if (doneCount !== 1) return "Exactly one lane must be marked as Done.";
  return null;
}

function renderWorkflowEditorBody(
  lanes: WorkflowLaneDraft[],
  listId = "workflowModalLaneList",
  ghostId = "workflowModalGhostInput"
): string {
  return `
    <div class="muted" style="margin-bottom: 8px;">Configure lanes before creating the project.</div>
    <div id="${escapeHTML(listId)}">
      ${lanes.map((lane) => `
        <div class="row" style="margin-bottom:6px;" data-lane-key="${escapeHTML(lane.key)}">
          <button type="button" class="btn btn--ghost btn--small workflow-lane__handle" aria-label="Reorder lane">↕</button>
          <input class="input" data-lane-name="${escapeHTML(lane.key)}" value="${escapeHTML(lane.name)}" maxlength="50" />
          <input type="color" class="settings-color-picker" data-lane-color="${escapeHTML(lane.key)}" value="${escapeHTML(lane.color || "#64748b")}" aria-label="Lane color for ${escapeHTML(lane.name)}" />
          <label class="muted workflow-done-label-wrap" style="display:flex; align-items:center; gap:4px;" title="Done lane">
            <input type="radio" name="workflowModalDoneLane" data-lane-done="${escapeHTML(lane.key)}" ${lane.isDone ? "checked" : ""} aria-label="Set ${escapeHTML(lane.name)} as done lane" />
            <span class="workflow-done-label">Done</span>
          </label>
          <button class="btn btn--ghost btn--small" type="button" data-lane-remove="${escapeHTML(lane.key)}" ${lanes.length <= 2 || lane.isDone ? "disabled" : ""}>×</button>
        </div>
      `).join("")}
    </div>
    <div class="row" style="gap:8px;align-items:center;">
      <input class="input" id="${escapeHTML(ghostId)}" placeholder="Add lane..." style="flex:1;min-width:0;" />
      <button type="button" class="btn btn--small" id="workflowModalGhostAdd" aria-label="Add lane">Add</button>
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
        <div class="dialog__title">Customize Workflow</div>
      </div>
      <div class="dialog__content" id="workflowModalContent"></div>
      <div class="dialog__footer">
        <button type="button" class="btn btn--ghost" id="workflowModalCancel">Cancel</button>
        <button type="button" class="btn" id="workflowModalConfirm">Confirm</button>
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

    confirmBtn.textContent = "Creating...";
    confirmBtn.disabled = true;

    try {
      const p = await apiFetch<Project>("/api/projects", { method: "POST", body: JSON.stringify(payload) });
      resetWorkflowEditorState();
      cleanup();
      navigate(`/${p.slug}`);
    } catch (err: any) {
      showToast(err.message);
      confirmBtn.textContent = "Confirm";
      confirmBtn.disabled = false;
    }
  });

  (dialog as HTMLDialogElement).showModal();

  const firstLaneInput = contentDiv.querySelector("[data-lane-name]") as HTMLInputElement | null;
  const ghostInput = contentDiv.querySelector("#workflowModalGhostInput") as HTMLInputElement | null;
  (firstLaneInput || ghostInput)?.focus();
}

// Declare renderAuth function (will be available after Step 3)
declare function renderAuth(opts: { next: string; bootstrap?: boolean }): void;

// Runtime access to renderAuth from auth view (after Step 3)
async function getRenderAuth(): Promise<(opts: { next: string; bootstrap?: boolean }) => void> {
  try {
    // @ts-ignore - auth.js will exist after Step 3
    const authModule = await import('./auth.js');
    return authModule.renderAuth;
  } catch {
    return (window as any).renderAuth || renderAuth;
  }
}

export async function renderProjects(): Promise<void> {
  let projects: Project[];
  try {
    projects = await apiFetch<Project[]>("/api/projects");
  } catch (err: any) {
    if (err && err.status === 401) {
      const renderAuth = await getRenderAuth();
      renderAuth({ next: "/" });
      return;
    }
    throw err;
  }
  // Cache for settings modal: in full mode tags are global but the legacy tag APIs require a project id.
  setProjects(projects);
  ingestProjectsFromApp(projects);
  if (!getProjectsTab()) {
    setProjectsTab(localStorage.getItem("projectsTab") || "projects");
  }

  const durableProjects = projects.filter((p) => !p.expiresAt);
  const temporaryBoards = projects.filter((p) => !!p.expiresAt);
  const activeList = getProjectsTab() === "temporary" ? temporaryBoards : durableProjects;
  const emptyMsg = getProjectsTab() === "temporary" ? "No temporary boards yet." : "No projects yet.";
  const temporaryLabel = temporaryBoardsNavLabel();
  const tabsHTML = `
    <div class="chips" style="margin-top: 10px; margin-bottom: 12px;">
      <button class="chip" id="dashboardTabBtn" type="button">
        Dashboard
      </button>
      <button class="chip ${getProjectsTab() === "projects" ? "chip--active" : ""}" data-projects-tab="projects">
        Projects <span class="chip__count">${durableProjects.length}</span>
      </button>
      <button class="chip ${getProjectsTab() === "temporary" ? "chip--active" : ""}" data-projects-tab="temporary">
        ${temporaryLabel} <span class="chip__count">${temporaryBoards.length}</span>
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
        ${!getUser() ? `<button class="btn btn--ghost" id="settingsBtn" aria-label="Settings">
          <span class="hamburger">☰</span>
        </button>` : ''}
        ${renderUserAvatar(getUser())}
      </div>

      <div class="container">
        <div class="panel">
          <div class="panel__header">
          <div class="panel__title">Projects</div>
            <div class="view-toggle">
              <button class="btn btn--ghost btn--small view-toggle-btn ${getProjectView() === 'list' ? 'view-toggle-btn--active' : ''}" data-view="list" title="List view">
                <span>☰</span>
              </button>
              <button class="btn btn--ghost btn--small view-toggle-btn ${getProjectView() === 'grid' ? 'view-toggle-btn--active' : ''}" data-view="grid" title="Grid view">
                <span>⊞</span>
              </button>
            </div>
          </div>
          ${tabsHTML}
          <form id="createProjectForm" class="row">
            ${getProjectsTab() === "temporary" ? `<div class="spacer"></div>` : `<input class="input" id="projectName" placeholder="New project name" maxlength="200" required />`}
            <button class="btn" type="submit">${getProjectsTab() === "temporary" ? "Create Temporary Board" : "Create"}</button>
          </form>
          <div class="${getProjectView() === 'grid' ? 'project-grid' : 'list'}" id="projectList">
            ${activeList.length === 0 ? `<div class="muted">${escapeHTML(emptyMsg)}</div>` : ""}
            ${activeList
              .map(
                (p) => {
                  const displayName = p.expiresAt ? `${p.name} (${p.slug})` : p.name;
                  const isMaintainer = (p.role || "").toLowerCase() === "maintainer";
                  const maintainerActions = isMaintainer ? `
                    <button class="btn btn--ghost btn--small" data-rename="${p.id}" title="Rename project">Rename</button>
                    <button class="btn btn--danger btn--small" data-del="${p.id}">Delete</button>` : "";
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
        showToast("Project name is required.");
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
        
        const newName = prompt("Enter new project name:", project.name);
        if (!newName || newName.trim() === "" || newName === project.name) return;
        
        try {
          await apiFetch(`/api/projects/${id}`, {
            method: "PATCH",
            body: JSON.stringify({ name: newName.trim() }),
          });
          await renderProjects();
          showToast("Project renamed");
        } catch (err: any) {
          showToast(err.message);
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
        if (!confirm("Delete this project and all its todos?")) return;
        try {
          await apiFetch(`/api/projects/${id}`, { method: "DELETE" });
          await renderProjects();
        } catch (err: any) {
          showToast(err.message);
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
