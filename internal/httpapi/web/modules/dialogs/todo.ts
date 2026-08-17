import {
  addTagBtn,
  closeTodoBtn,
  deleteTodoBtn,
  shareTodoBtn,
  todoBody,
  todoBodyPreview,
  todoBodyPreviewTab,
  todoBodyToggle,
  todoBodyWriteTab,
  todoDialog,
  todoDialogTitle,
  todoEstimationField,
  todoEstimationPoints,
  todoPriority,
  todoStatus,
  todoTags,
  todoTitle,
} from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { DIALOG_CLOSE_REQUEST_EVENT, type DialogCloseRequestDetail } from '../core/modal-outside-click.js';
import { renderMarkdownPreviewInto } from '../markdown-preview.js';
import { THEME_CHANGE_EVENT } from '../theme.js';
import { getBoard, getBoardMembers, getMarkdownNotesEnabled, getMermaidNotesEnabled, getSlug, getTagColors, getUser } from '../state/selectors.js';
import { setAvailableTags, setAvailableTagsMap, setEditingTodo, setTagColors } from '../state/mutations.js';
import { escapeHTML, isAnonymousBoard, showConfirmDialog, showToast } from '../utils.js';
import { applyFieldTooltips, TODO_DIALOG_TOOLTIPS } from '../field-tooltips.js';
import { apiErrorMessage, formatDate as formatLocalizedDate, hasI18nKey, I18N_LOCALE_CHANGED, t } from '../i18n/index.js';
import { boardSprintsEnabled, normalizeSprints } from '../sprints.js';
import {
  bindShareTodoButton,
  bindTodoDialogLinkLifecycle,
  initializeTodoDialogLinks,
  resetTodoDialogLinks,
} from './todo-links.js';
import {
  computeTodoDialogPermissions,
  setTodoFormPermissions,
} from './todo-permissions.js';
import {
  getTagsFromChips,
  renderTagsChips,
  resetTodoTagAutocompleteBindings,
  setupTagAutocomplete,
} from './todo-tags.js';

export {
  getTodoFormPermissions,
  type TodoDialogPermissions,
} from './todo-permissions.js';
export {
  getTagsFromChips,
  normalizeTagName,
  removeTag,
  renderTagAutocomplete,
  renderTagsChips,
  setupTagAutocomplete,
} from './todo-tags.js';

type TodoNotesMode = "markdown" | "preview";
type TodoDialogCloseReason = "button" | "cancel" | "outside" | "save" | "delete";
type TodoDialogSnapshot = {
  title: string;
  body: string;
  tags: string[];
  status: string;
  estimation: string;
  assignee: string;
  sprint: string;
  priority: string;
};

let todoNotesMode: TodoNotesMode = "markdown";
let todoNotesPreviewBound = false;
let todoDialogCloseGuardsBound = false;
let todoTooltipsApplied = false;
let todoDialogBaseline: TodoDialogSnapshot | null = null;
let todoDialogClosePromptOpen = false;
let todoCreatorLocaleAbort: AbortController | null = null;

function sprintStateLabel(state: string): string {
  const key = `todo.sprint.state.${state}`;
  return state && hasI18nKey(key) ? t(key) : state;
}

function setTodoDialogTitleKey(key: string): void {
  const titleEl = todoDialogTitle as HTMLElement | null;
  if (!titleEl) return;
  titleEl.setAttribute("data-i18n-text", key);
  titleEl.textContent = t(key);
}

export function resolveColumnKey(raw: string | undefined | null): string {
  const v = (raw || "").trim();
  if (!v) return "";
  const upper = v.toUpperCase();
  switch (upper) {
    case "BACKLOG":
      return "backlog";
    case "NOT_STARTED":
      return "not_started";
    case "IN_PROGRESS":
      return "doing";
    case "TESTING":
      return "testing";
    case "DONE":
      return "done";
    default:
      return v.toLowerCase();
  }
}

function populateTodoStatusOptions(preferredKey: string): string {
  const select = todoStatus as HTMLSelectElement;
  const board = getBoard();
  const order = (board as any)?.columnOrder as
    | Array<{ key: string; name: string; color?: string; isDone?: boolean }>
    | undefined;
  if (!order || order.length === 0) {
    return preferredKey || "backlog";
  }
  select.innerHTML = order
    .map((c) => `<option value="${escapeHTML(c.key)}">${escapeHTML(c.name)}</option>`)
    .join("");
  const hasPreferred = order.some((c) => c.key === preferredKey);
  const selected = hasPreferred ? preferredKey : order[0].key;
  select.value = selected;
  return selected;
}

function populateTodoPriorityOptions(preferredKey: string | null | undefined): string {
  const select = todoPriority as HTMLSelectElement | null;
  if (!select) return preferredKey ?? "";
  const board = getBoard();
  const tiers = board?.priorityOrder ?? [];
  const noneOption = `<option value="" data-i18n-text="todo.priority.none">${escapeHTML(t("todo.priority.none"))}</option>`;
  select.innerHTML =
    noneOption +
    tiers.map((tier) => `<option value="${escapeHTML(tier.key)}">${escapeHTML(tier.name)}</option>`).join("");
  const hasPreferred = !!preferredKey && tiers.some((tier) => tier.key === preferredKey);
  const selected = hasPreferred ? (preferredKey as string) : "";
  select.value = selected;
  return selected;
}

function isModifiedFibonacciMode(): boolean {
  const mode = getBoard()?.project?.estimationMode;
  return mode == null || mode === "MODIFIED_FIBONACCI";
}

// Collapse arbitrary text (e.g. a multi-line sticky-note body) into a value
// suitable for the single-line Title input: trim ends, collapse runs of
// whitespace (including newlines) to a single space, and honor the input's
// maxLength if one is set.
function normalizeSeedTitle(raw: string | undefined): string {
  if (!raw) return "";
  const collapsed = raw.replace(/\s+/g, " ").trim();
  const input = todoTitle as HTMLInputElement | null;
  const max = input?.maxLength;
  if (typeof max === "number" && max > 0 && collapsed.length > max) {
    return collapsed.slice(0, max);
  }
  return collapsed;
}

export { normalizeSeedTitle as __normalizeSeedTitleForTest };

function markdownNotesPreviewEnabled(): boolean {
  return !!(
    getMarkdownNotesEnabled() &&
    todoBody &&
    todoBodyPreview &&
    todoBodyToggle &&
    todoBodyWriteTab &&
    todoBodyPreviewTab
  );
}

async function renderTodoNotesPreview(): Promise<void> {
  if (!todoBodyPreview || !todoBody) {
    return;
  }
  try {
    await renderMarkdownPreviewInto(
      todoBodyPreview as HTMLElement,
      (todoBody as HTMLTextAreaElement).value || "",
      { mermaidEnabled: getMermaidNotesEnabled() },
    );
  } catch (err: any) {
    showToast(apiErrorMessage(err, { fallbackKey: "todo.notes.previewUnavailable" }));
    todoNotesMode = "markdown";
    syncTodoNotesModeUI();
  }
}

function syncTodoNotesModeUI(): void {
  const previewEnabled = markdownNotesPreviewEnabled();
  if (todoBodyToggle) {
    (todoBodyToggle as HTMLElement).hidden = !previewEnabled;
  }
  if (!previewEnabled) {
    todoNotesMode = "markdown";
  }

  const isPreview = previewEnabled && todoNotesMode === "preview";
  if (todoBody) {
    (todoBody as HTMLTextAreaElement).hidden = isPreview;
  }
  if (todoBodyPreview) {
    (todoBodyPreview as HTMLElement).hidden = !isPreview;
  }
  if (todoBodyWriteTab) {
    const button = todoBodyWriteTab as HTMLButtonElement;
    button.classList.toggle("is-active", !isPreview);
    button.setAttribute("aria-pressed", (!isPreview).toString());
  }
  if (todoBodyPreviewTab) {
    const button = todoBodyPreviewTab as HTMLButtonElement;
    button.classList.toggle("is-active", isPreview);
    button.setAttribute("aria-pressed", isPreview.toString());
  }
}

function setTodoNotesMode(mode: TodoNotesMode): void {
  todoNotesMode = mode;
  syncTodoNotesModeUI();
  if (todoNotesMode === "preview") {
    void renderTodoNotesPreview();
  }
}

function bindTodoNotesPreviewControls(): void {
  if (todoNotesPreviewBound) {
    return;
  }
  todoNotesPreviewBound = true;

  if (todoBodyWriteTab) {
    (todoBodyWriteTab as HTMLButtonElement).addEventListener("click", () => {
      setTodoNotesMode("markdown");
    });
  }
  if (todoBodyPreviewTab) {
    (todoBodyPreviewTab as HTMLButtonElement).addEventListener("click", () => {
      if (!markdownNotesPreviewEnabled()) {
        return;
      }
      setTodoNotesMode("preview");
    });
  }
  if (todoBody) {
    (todoBody as HTMLTextAreaElement).addEventListener("input", () => {
      if (todoNotesMode === "preview") {
        void renderTodoNotesPreview();
      }
    });
  }

  document.addEventListener(THEME_CHANGE_EVENT, () => {
    if (todoNotesMode === "preview" && markdownNotesPreviewEnabled()) {
      void renderTodoNotesPreview();
    }
  });
}

function readTodoDialogSnapshot(): TodoDialogSnapshot {
  const assignee = document.getElementById("todoAssignee") as HTMLSelectElement | null;
  const sprint = document.getElementById("todoSprint") as HTMLSelectElement | null;
  return {
    title: (todoTitle as HTMLInputElement | null)?.value ?? "",
    body: (todoBody as HTMLTextAreaElement | null)?.value ?? "",
    tags: getTagsFromChips(),
    status: (todoStatus as HTMLSelectElement | null)?.value ?? "",
    estimation: (todoEstimationPoints as HTMLSelectElement | null)?.value ?? "",
    assignee: assignee?.value ?? "",
    sprint: sprint?.value ?? "",
    priority: (todoPriority as HTMLSelectElement | null)?.value ?? "",
  };
}

function captureTodoDialogBaseline(): void {
  todoDialogBaseline = readTodoDialogSnapshot();
}

function isTodoDialogDirty(): boolean {
  if (!todoDialogBaseline) {
    return false;
  }
  const current = readTodoDialogSnapshot();
  return (
    current.title !== todoDialogBaseline.title ||
    current.body !== todoDialogBaseline.body ||
    current.status !== todoDialogBaseline.status ||
    current.estimation !== todoDialogBaseline.estimation ||
    current.assignee !== todoDialogBaseline.assignee ||
    current.sprint !== todoDialogBaseline.sprint ||
    current.priority !== todoDialogBaseline.priority ||
    current.tags.length !== todoDialogBaseline.tags.length ||
    current.tags.some((tag, idx) => tag !== todoDialogBaseline?.tags[idx])
  );
}

function resetTodoDialogCloseState(): void {
  todoDialogBaseline = null;
  todoDialogClosePromptOpen = false;
  todoCreatorLocaleAbort?.abort();
  todoCreatorLocaleAbort = null;
}

async function closeTodoDialogInternal(
  options: { force?: boolean; reason?: TodoDialogCloseReason } = {},
): Promise<boolean> {
  const dialog = todoDialog as HTMLDialogElement | null;
  if (!dialog || !dialog.open) {
    return true;
  }
  if (options.force || !isTodoDialogDirty()) {
    dialog.close();
    return true;
  }
  if (todoDialogClosePromptOpen) {
    return false;
  }

  todoDialogClosePromptOpen = true;
  try {
    const discard = await showConfirmDialog(
      t("todo.confirm.discardMessage"),
      t("todo.confirm.discardTitle"),
      t("todo.confirm.discardAction"),
    );
    if (!discard) {
      return false;
    }
    dialog.close();
    return true;
  } finally {
    todoDialogClosePromptOpen = false;
  }
}

function ensureTodoDialogTooltips(): void {
  if (todoTooltipsApplied || !todoDialog) return;
  todoTooltipsApplied = true;
  applyFieldTooltips(TODO_DIALOG_TOOLTIPS, todoDialog);
}

function bindTodoDialogCloseGuards(): void {
  ensureTodoDialogTooltips();
  if (todoDialogCloseGuardsBound || !todoDialog) {
    return;
  }
  todoDialogCloseGuardsBound = true;

  (todoDialog as HTMLDialogElement).addEventListener("cancel", (event) => {
    if (todoDialogClosePromptOpen) {
      event.preventDefault();
      return;
    }
    if (!isTodoDialogDirty()) {
      return;
    }
    event.preventDefault();
    void closeTodoDialogInternal({ reason: "cancel" });
  });

  (todoDialog as HTMLDialogElement).addEventListener(DIALOG_CLOSE_REQUEST_EVENT, (event) => {
    if (todoDialogClosePromptOpen) {
      event.preventDefault();
      return;
    }
    if (!isTodoDialogDirty()) {
      return;
    }
    event.preventDefault();
    const detail = (event as CustomEvent<DialogCloseRequestDetail>).detail;
    void closeTodoDialogInternal({ reason: detail?.reason === "outside" ? "outside" : "button" });
  });

  (todoDialog as HTMLDialogElement).addEventListener("close", () => {
    resetTodoDialogCloseState();
  });
}

export function requestTodoDialogClose(
  options: { force?: boolean; reason?: TodoDialogCloseReason } = {},
): Promise<boolean> {
  bindTodoDialogCloseGuards();
  return closeTodoDialogInternal(options);
}

export function __isTodoDialogDirtyForTest(): boolean {
  return isTodoDialogDirty();
}

export async function openTodoDialog(opts: {
  mode: string;
  todo?: any;
  status?: string;
  initialTitle?: string;
  onNavigateToLinkedTodo?: (path: string) => void;
  role?: string | null;
}): Promise<void> {
  const { mode, todo, status, onNavigateToLinkedTodo } = opts;
  setEditingTodo(mode === "edit" ? todo : null);
  bindTodoDialogCloseGuards();
  bindTodoDialogLinkLifecycle();
  bindTodoNotesPreviewControls();

  const board = getBoard();
  const permissions = computeTodoDialogPermissions({
    board,
    mode,
    todo,
    role: opts.role,
  });
  setTodoFormPermissions(permissions);

  if (getSlug()) {
    try {
      let tagsResponse: any[];
      if (getUser()) {
        tagsResponse = (await apiFetch(`/api/tags/mine`)) as any[];
      } else {
        tagsResponse = (await apiFetch(`/api/board/${getSlug()}/tags`)) as any[];
      }

      setAvailableTags(tagsResponse.map((tag: any) => (typeof tag === "string" ? tag : tag.name)));
      const tagsMap: Record<string, string> = {};
      tagsResponse.forEach((tag: any) => {
        const tagName = typeof tag === "string" ? tag : tag.name;
        tagsMap[tagName.toLowerCase()] = tagName;
        if (tag.color) {
          const tagColors = { ...getTagColors() };
          tagColors[tagName] = tag.color;
          setTagColors(tagColors);
        }
      });
      setAvailableTagsMap(tagsMap);
    } catch (err: any) {
      console.error("Failed to fetch tags:", err);
      setAvailableTags([]);
      setAvailableTagsMap({});
    }
  } else {
    setAvailableTags([]);
    setAvailableTagsMap({});
  }

  const assigneeField = document.getElementById("todoAssigneeField");
  const assigneeSelect = document.getElementById("todoAssignee") as HTMLSelectElement | null;
  const showAssignee = assigneeField && assigneeSelect && !isAnonymousBoard(getBoard());
  if (assigneeField) {
    assigneeField.style.display = showAssignee ? "" : "none";
  }

  const sprintField = document.getElementById("todoSprintField");
  const sprintSelect = document.getElementById("todoSprint") as HTMLSelectElement | null;
  const showSprint =
    sprintField &&
    sprintSelect &&
    !isAnonymousBoard(getBoard()) &&
    !!getSlug() &&
    opts.role === "maintainer" &&
    boardSprintsEnabled(getBoard());
  if (sprintField) {
    sprintField.style.display = showSprint ? "" : "none";
  }
  if (sprintSelect) {
    if (!showSprint) {
      sprintSelect.value = "";
    } else {
      try {
        const res = await apiFetch<{ sprints?: { id: number; name: string; state: string }[] } | null>(
          `/api/board/${getSlug()}/sprints`,
        );
        const sprints = normalizeSprints(res);
        const defaultOpt = document.createElement("option");
        defaultOpt.value = "";
        defaultOpt.textContent = "—";
        const options: HTMLOptionElement[] = [defaultOpt];
        for (const sp of sprints) {
          const opt = document.createElement("option");
          opt.value = String(sp.id);
          opt.textContent = `${sp.name} (${sprintStateLabel(sp.state)})`;
          options.push(opt);
        }
        sprintSelect.replaceChildren(...options);
        const fromTodo = todo?.sprintId != null ? String(todo.sprintId) : "";
        sprintSelect.value = fromTodo;
      } catch (err: any) {
        console.error("Failed to fetch sprints:", err);
      }
    }
  }

  if (assigneeSelect) {
    if (showAssignee) {
      const user = getUser();
      const members = getBoardMembers();
      const myMember = user ? members.find((m) => m.userId === user.id) : null;
      const canAssignOthers = myMember?.role === "maintainer";

      assigneeSelect.innerHTML = "";
      const unassigned = document.createElement("option");
      unassigned.value = "";
      unassigned.textContent = t("todo.assignee.unassigned");
      assigneeSelect.appendChild(unassigned);

      if (canAssignOthers) {
        for (const m of members) {
          const opt = document.createElement("option");
          opt.value = String(m.userId);
          opt.textContent = m.name || m.email || String(m.userId);
          assigneeSelect.appendChild(opt);
        }
      } else {
        if (mode === "edit") {
          const currentAssigneeId = todo?.assigneeUserId;
          if (currentAssigneeId != null && user && Number(currentAssigneeId) !== Number(user.id)) {
            const assigneeMember = members.find((m) => Number(m.userId) === Number(currentAssigneeId));
            if (assigneeMember) {
              const opt = document.createElement("option");
              opt.value = String(assigneeMember.userId);
              opt.textContent = t("todo.assignee.current", {
                name: assigneeMember.name || assigneeMember.email || String(assigneeMember.userId),
              });
              opt.disabled = true;
              assigneeSelect.appendChild(opt);
            }
          }
        }
        if (user) {
          const opt = document.createElement("option");
          opt.value = String(user.id);
          opt.textContent = user.name || user.email || t("todo.assignee.me");
          assigneeSelect.appendChild(opt);
        }
      }
      assigneeSelect.value = todo?.assigneeUserId != null ? String(todo.assigneeUserId) : "";
    } else {
      assigneeSelect.innerHTML = `<option value="">${escapeHTML(t("todo.assignee.unassigned"))}</option>`;
    }
  }

  const linksField = document.getElementById("todoLinksField");
  const slug = getSlug();
  const editableWithLinks = mode === "edit" && !!todo?.localId && !!slug;
  if (linksField) {
    linksField.style.display = editableWithLinks ? "" : "none";
  }
  if (editableWithLinks) {
    try {
      await initializeTodoDialogLinks(slug!, todo.localId, onNavigateToLinkedTodo);
    } catch (err: any) {
      showToast(apiErrorMessage(err, { fallbackKey: "todo.loadLinkedFailed" }));
    }
  } else {
    resetTodoDialogLinks();
  }

  const estimationField = todoEstimationField as HTMLElement | null;
  const estimationSelect = todoEstimationPoints as HTMLSelectElement | null;
  const showEstimation = isModifiedFibonacciMode();
  if (estimationField) {
    estimationField.style.display = showEstimation ? "" : "none";
  }
  if (estimationSelect) {
    if (!showEstimation) {
      estimationSelect.value = "";
    } else if (mode === "create") {
      estimationSelect.value = "";
    } else {
      estimationSelect.value = todo?.estimationPoints != null ? String(todo.estimationPoints) : "";
    }
  }

  const createdEl = document.getElementById("todoDialogCreated") as HTMLElement | null;
  const createdByEl = document.getElementById("todoDialogCreatedBy") as HTMLElement | null;
  const updatedEl = document.getElementById("todoDialogUpdated") as HTMLElement | null;
  const formatDialogDate = (d: string) =>
    formatLocalizedDate(d, {
      year: "2-digit",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    });
  const setDates = (createdAt: string | undefined, updatedAt: string | undefined) => {
    if (createdEl) {
      const valueEl = createdEl.querySelector(".todo-dialog-datetime-value");
      if (createdAt == null) {
        if (valueEl) (valueEl as HTMLElement).textContent = "";
        createdEl.setAttribute("aria-hidden", "true");
      } else {
        if (valueEl) (valueEl as HTMLElement).textContent = formatDialogDate(createdAt);
        createdEl.setAttribute("aria-hidden", "false");
      }
    }
    if (updatedEl) {
      const valueEl = updatedEl.querySelector(".todo-dialog-datetime-value");
      if (updatedAt == null) {
        if (valueEl) (valueEl as HTMLElement).textContent = "";
        updatedEl.setAttribute("aria-hidden", "true");
      } else {
        if (valueEl) (valueEl as HTMLElement).textContent = formatDialogDate(updatedAt);
        updatedEl.setAttribute("aria-hidden", "false");
      }
    }
  };
  const setCreatedBy = (createdByUserId: number | null | undefined) => {
    if (!createdByEl) return;
    const member = createdByUserId != null ? getBoardMembers().find((m) => m.userId === createdByUserId) : null;
    const name = member?.name || member?.email || "";
    createdByEl.textContent = name ? t("todo.dialog.openedBy", { name }) : "";
  };

  todoCreatorLocaleAbort?.abort();
  todoCreatorLocaleAbort = new AbortController();
  document.addEventListener(
    I18N_LOCALE_CHANGED,
    () => setCreatedBy(mode === "edit" ? todo?.createdByUserId : undefined),
    { signal: todoCreatorLocaleAbort.signal },
  );

  if (mode === "create") {
    setTodoDialogTitleKey("todo.dialog.title.new");
    (todoTitle as HTMLInputElement).value = normalizeSeedTitle(opts.initialTitle);
    (todoBody as HTMLTextAreaElement).value = "";
    (todoTags as HTMLInputElement).value = "";
    const initialKey = resolveColumnKey(status);
    const selected = populateTodoStatusOptions(initialKey);
    (todoStatus as HTMLSelectElement).value = selected;
    populateTodoPriorityOptions(null);
    (deleteTodoBtn as HTMLElement).style.display = "none";
    if (shareTodoBtn) (shareTodoBtn as HTMLElement).style.display = "none";
    setDates(undefined, undefined);
    setCreatedBy(undefined);
  } else {
    setTodoDialogTitleKey(permissions.canSubmitTodo ? "todo.dialog.title.edit" : "todo.dialog.title.view");
    (todoTitle as HTMLInputElement).value = todo.title || "";
    (todoBody as HTMLTextAreaElement).value = todo.body || "";
    (todoTags as HTMLInputElement).value = "";
    const initialKey = resolveColumnKey(todo.columnKey || todo.status);
    const selected = populateTodoStatusOptions(initialKey);
    (todoStatus as HTMLSelectElement).value = selected;
    populateTodoPriorityOptions(todo.priorityKey);
    (deleteTodoBtn as HTMLElement).style.display = permissions.canDeleteTodo ? "" : "none";
    if (shareTodoBtn) (shareTodoBtn as HTMLElement).style.display = "";
    setDates(todo.createdAt, todo.updatedAt);
    setCreatedBy(todo.createdByUserId);
  }
  setTodoNotesMode("markdown");

  const tagInputEl = document.getElementById("todoTags") as HTMLInputElement | null;
  if (tagInputEl) {
    tagInputEl.replaceWith(tagInputEl.cloneNode(true));
    resetTodoTagAutocompleteBindings();
  }
  const tagInputRefetched = document.getElementById("todoTags") as HTMLInputElement | null;
  if (tagInputRefetched) {
    tagInputRefetched.value = "";
  }

  if (assigneeSelect) assigneeSelect.disabled = !permissions.canEditAssignment;
  if (estimationSelect) estimationSelect.disabled = !permissions.canChangeEstimation;
  if (todoPriority) (todoPriority as HTMLSelectElement).disabled = !permissions.canChangeEstimation;
  const tagInput = document.getElementById("todoTags") as HTMLInputElement | null;
  if (tagInput) tagInput.disabled = !permissions.canEditTags;
  if (addTagBtn) (addTagBtn as HTMLButtonElement).disabled = !permissions.canEditTags;
  (todoBody as HTMLTextAreaElement).readOnly = !permissions.canEditNotes;
  (todoTitle as HTMLInputElement).readOnly = !permissions.canEditTitle;
  (todoStatus as HTMLSelectElement).disabled = !permissions.canEditStatus;
  const saveTodoBtn = document.getElementById("saveTodoBtn") as HTMLButtonElement | null;
  if (saveTodoBtn) saveTodoBtn.disabled = !permissions.canSubmitTodo;

  const tagsChips = document.getElementById("tagsChips");
  if (tagsChips) tagsChips.innerHTML = "";
  const tagsToShow = mode === "create" ? [] : (todo?.tags || []);
  renderTagsChips(tagsToShow, { canRemove: permissions.canEditTags });

  if (permissions.canEditTags) {
    setupTagAutocomplete();
  }

  bindShareTodoButton();
  captureTodoDialogBaseline();

  (todoDialog as HTMLDialogElement).showModal();
  let userChoseFocus = false;
  const ac = new AbortController();
  todoDialog.addEventListener(
    "pointerdown",
    () => {
      userChoseFocus = true;
    },
    { capture: true, signal: ac.signal },
  );
  requestAnimationFrame(() => {
    ac.abort();
    if (userChoseFocus) {
      return;
    }
    if (window.matchMedia("(pointer: coarse)").matches) {
      (closeTodoBtn as HTMLButtonElement)?.focus();
      return;
    }
    if (mode === "edit") {
      if (!permissions.canSubmitTodo) {
        (closeTodoBtn as HTMLButtonElement)?.focus();
      } else {
        (todoStatus as HTMLSelectElement)?.focus();
      }
    } else {
      (todoTitle as HTMLInputElement).focus();
    }
  });
}

export function resetAssigneeSelect(): void {
  const assigneeSelect = document.getElementById("todoAssignee") as HTMLSelectElement | null;
  if (assigneeSelect) {
    assigneeSelect.innerHTML = `<option value="">${escapeHTML(t("todo.assignee.unassigned"))}</option>`;
  }
  const estimationSelect = todoEstimationPoints as HTMLSelectElement | null;
  if (estimationSelect) {
    estimationSelect.value = "";
  }
}
