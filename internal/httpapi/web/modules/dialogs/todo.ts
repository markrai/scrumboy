import { todoDialog, todoDialogTitle, todoTitle, todoBody, todoTags, addTagBtn, todoStatus, todoEstimationField, todoEstimationPoints, deleteTodoBtn, shareTodoBtn, closeTodoBtn } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { getSlug, getTag, getTagColors, getAvailableTags, getAvailableTagsMap, getAutocompleteSuggestion, getUser, getBoard, getBoardMembers, getEditingTodo } from '../state/selectors.js';
import { setEditingTodo, setAvailableTags, setAvailableTagsMap, setTagColors, setAutocompleteSuggestion } from '../state/mutations.js';
import { escapeHTML, isAnonymousBoard, showToast, sanitizeHexColor } from '../utils.js';
import { normalizeSprints } from '../sprints.js';
import { recordLocalMutation } from '../realtime/guard.js';

// Symbol for idempotent listener attachment
const BOUND_FLAG = Symbol('bound');

// Module-level state for tag autocomplete
let tagInputHandlersSetup = false;

// Permission map computed on each dialog open (used by this module and app.js submit guard)
export type TodoDialogPermissions = {
  canChangeSprint: boolean;
  canChangeEstimation: boolean;
  canEditTags: boolean;
  canEditNotes: boolean;
  canEditAssignment: boolean;
  canDeleteTodo: boolean;
  /** Title and workflow column (status); maintainers (and anonymous boards) only — not contributors/viewers. */
  canEditTitle: boolean;
  canEditStatus: boolean;
  /** POST/PATCH/move from the dialog; contributors only when assigned (body edit); viewers never. */
  canSubmitTodo: boolean;
  /** Add/remove linked stories; contributor+ on authenticated boards, or anonymous-board maintainer-equivalent. */
  canEditLinks: boolean;
};
let permissions: TodoDialogPermissions = {
  canChangeSprint: false,
  canChangeEstimation: false,
  canEditTags: false,
  canEditNotes: false,
  canEditAssignment: false,
  canDeleteTodo: false,
  canEditTitle: false,
  canEditStatus: false,
  canSubmitTodo: false,
  canEditLinks: false,
};

export function getTodoFormPermissions(): Readonly<TodoDialogPermissions> {
  return { ...permissions };
}
let linksSearchDebounce: ReturnType<typeof setTimeout> | null = null;
let linksSearchController: AbortController | null = null;
let lastLoadedLinksForTodo: { slug: string; localId: number } | null = null;
let dialogLinkLifecycleBound = false;

type LinkedStoryItem = {
  localId: number;
  title: string;
  linkType?: string;
};

type LinkedStoriesResponse = {
  outbound: LinkedStoryItem[];
  inbound: LinkedStoryItem[];
};

let currentLinks: LinkedStoriesResponse = { outbound: [], inbound: [] };
let linkAutocompleteSuggestion: LinkedStoryItem | null = null;

export function resolveColumnKey(raw: string | undefined | null): string {
  const v = (raw || "").trim();
  if (!v) return "";
  const upper = v.toUpperCase();
  switch (upper) {
    case "BACKLOG": return "backlog";
    case "NOT_STARTED": return "not_started";
    case "IN_PROGRESS": return "doing";
    case "TESTING": return "testing";
    case "DONE": return "done";
    default: return v.toLowerCase();
  }
}

function populateTodoStatusOptions(preferredKey: string): string {
  const select = todoStatus as HTMLSelectElement;
  const board = getBoard();
  const order = (board as any)?.columnOrder as Array<{ key: string; name: string; color?: string; isDone?: boolean }> | undefined;
  if (!order || order.length === 0) {
    return preferredKey || "backlog";
  }
  select.innerHTML = order.map((c) => `<option value="${escapeHTML(c.key)}">${escapeHTML(c.name)}</option>`).join("");
  const hasPreferred = order.some((c) => c.key === preferredKey);
  const selected = hasPreferred ? preferredKey : order[0].key;
  select.value = selected;
  return selected;
}

// Helper functions
function getTagColor(tagName: string): string | null {
  return getTagColors()[tagName] || null;
}

function isModifiedFibonacciMode(): boolean {
  const mode = getBoard()?.project?.estimationMode;
  return mode == null || mode === "MODIFIED_FIBONACCI";
}

function getTagInput(): HTMLInputElement | null {
  return document.getElementById("todoTags") as HTMLInputElement | null;
}

export function getTagsFromChips(): string[] {
  const chipsContainer = document.getElementById("tagsChips");
  if (!chipsContainer) return [];
  return Array.from(chipsContainer.querySelectorAll(".tag-chip")).map(chip => chip.getAttribute("data-tag") || "");
}

export function normalizeTagName(tagName: string): string {
  // Check if there's an existing tag with the same name (case-insensitive)
  const lowerTag = tagName.toLowerCase();
  if (getAvailableTagsMap()[lowerTag]) {
    return getAvailableTagsMap()[lowerTag];
  }
  // Also check currently added tags in the chips (case-insensitive)
  const currentTags = getTagsFromChips();
  const existingTag = currentTags.find(t => t.toLowerCase() === lowerTag);
  if (existingTag) {
    return existingTag;
  }
  // No existing tag found, return the input as-is
  return tagName;
}

export function renderTagsChips(tags: string[], opts?: { canRemove?: boolean }): void {
  const chipsContainer = document.getElementById("tagsChips");
  if (!chipsContainer) return;

  const canRemove = opts?.canRemove ?? true;

  chipsContainer.innerHTML = tags.map(tagName => {
    const tagColor = getTagColor(tagName);
    const safe = sanitizeHexColor(tagColor);
    const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
    const removeBtn = canRemove ? `<button type="button" class="tag-chip-remove" aria-label="Remove tag">×</button>` : "";
    return `
      <span class="tag-chip" data-tag="${escapeHTML(tagName)}" ${colorStyle}>
        ${escapeHTML(tagName)}
        ${removeBtn}
      </span>
    `;
  }).join("");

  // Add remove handlers only when canRemove (with guard for delegated clicks)
  chipsContainer.querySelectorAll(".tag-chip-remove").forEach(btn => {
    if (!(btn as any)[BOUND_FLAG]) {
      btn.addEventListener("click", (e) => {
        if (!permissions.canEditTags) return;
        e.stopPropagation();
        const chip = btn.closest(".tag-chip");
        const tagName = chip?.getAttribute("data-tag");
        if (tagName) {
          removeTag(tagName);
        }
      });
      (btn as any)[BOUND_FLAG] = true;
    }
  });
}

function updateTagAutocomplete(): void {
  const input = getTagInput();
  if (!input) return;
  const value = input.value;
  const cursorPos = input.selectionStart || 0;
  
  // Find the current tag being typed (last segment after comma)
  const beforeCursor = value.substring(0, cursorPos);
  const lastCommaIndex = beforeCursor.lastIndexOf(",");
  const currentTagRaw = beforeCursor.substring(lastCommaIndex + 1);
  const currentTag = currentTagRaw.trim();
  
  if (currentTag.length === 0 || getAvailableTags().length === 0) {
    setAutocompleteSuggestion(null);
    renderTagAutocomplete();
    return;
  }
  
  // Get all tags that have already been entered (excluding the current one being typed)
  const fullValue = input.value;
  const existingTags = fullValue
    .split(",")
    .map(t => t.trim().toLowerCase())
    .filter(t => t.length > 0 && t !== currentTag.toLowerCase());
  
  // Find matching tag (case-insensitive prefix match) that hasn't been used yet
  const matchingTag = getAvailableTags().find(tag => {
    const tagLower = tag.toLowerCase();
    const currentTagLower = currentTag.toLowerCase();
    return tagLower.startsWith(currentTagLower) && 
           tagLower !== currentTagLower &&
           !existingTags.includes(tagLower);
  });
  
  // Use normalized version if found (to get proper capitalization)
  if (matchingTag) {
    setAutocompleteSuggestion(normalizeTagName(matchingTag));
  } else {
    setAutocompleteSuggestion(null);
  }
  
  // Update the visual suggestion
  renderTagAutocomplete();
}

export function renderTagAutocomplete(): void {
  // Remove existing suggestion overlay
  const existing = document.getElementById("tagAutocompleteSuggestion");
  if (existing) {
    existing.remove();
  }
  
  if (!getAutocompleteSuggestion()) {
    return;
  }
  
  const input = getTagInput();
  if (!input) return;
  const value = input.value;
  const cursorPos = input.selectionStart || 0;
  const beforeCursor = value.substring(0, cursorPos);
  const lastCommaIndex = beforeCursor.lastIndexOf(",");
  const currentTagRaw = beforeCursor.substring(lastCommaIndex + 1);
  const currentTag = currentTagRaw.trim();
  
  if (currentTag.length === 0) {
    return;
  }
  
  const suggestion = getAutocompleteSuggestion();
  if (!suggestion) return;
  const remaining = suggestion.substring(currentTag.length);
  if (remaining.length === 0) return;
  
  // Create overlay element for suggestion
  const overlay = document.createElement("div");
  overlay.id = "tagAutocompleteSuggestion";
  overlay.className = "tag-autocomplete-suggestion";
  overlay.textContent = remaining;
  
  // Position overlay to match input text position
  const inputRect = input.getBoundingClientRect();
  const style = window.getComputedStyle(input);
  const paddingLeft = parseFloat(style.paddingLeft) || 0;
  const paddingTop = parseFloat(style.paddingTop) || 0;
  const borderLeft = parseFloat(style.borderLeftWidth) || 0;
  const borderTop = parseFloat(style.borderTopWidth) || 0;
  
  // Create temporary span to measure text width (up to cursor)
  const measureSpan = document.createElement("span");
  measureSpan.style.position = "absolute";
  measureSpan.style.visibility = "hidden";
  measureSpan.style.whiteSpace = "pre";
  measureSpan.style.fontSize = style.fontSize;
  measureSpan.style.fontFamily = style.fontFamily;
  measureSpan.style.fontWeight = style.fontWeight;
  measureSpan.style.fontStyle = style.fontStyle;
  measureSpan.style.letterSpacing = style.letterSpacing;
  measureSpan.style.padding = "0";
  measureSpan.style.margin = "0";
  measureSpan.style.border = "none";
  measureSpan.style.lineHeight = style.lineHeight;
  measureSpan.textContent = beforeCursor;
  document.body.appendChild(measureSpan);
  const textWidth = measureSpan.getBoundingClientRect().width;
  
  // Create another span to measure vertical text position within input
  const measureVerticalSpan = document.createElement("span");
  measureVerticalSpan.style.position = "absolute";
  measureVerticalSpan.style.visibility = "hidden";
  measureVerticalSpan.style.whiteSpace = "pre";
  measureVerticalSpan.style.fontSize = style.fontSize;
  measureVerticalSpan.style.fontFamily = style.fontFamily;
  measureVerticalSpan.style.fontWeight = style.fontWeight;
  measureVerticalSpan.style.fontStyle = style.fontStyle;
  measureVerticalSpan.style.letterSpacing = style.letterSpacing;
  measureVerticalSpan.style.textTransform = style.textTransform;
  measureVerticalSpan.style.padding = "0";
  measureVerticalSpan.style.margin = "0";
  measureVerticalSpan.style.border = "none";
  measureVerticalSpan.style.lineHeight = style.lineHeight;
  measureVerticalSpan.textContent = "X"; // Single character to measure baseline
  // Position it exactly where input text would be
  measureVerticalSpan.style.top = `${inputRect.top + borderTop + paddingTop}px`;
  measureVerticalSpan.style.left = `${inputRect.left + borderLeft + paddingLeft}px`;
  document.body.appendChild(measureVerticalSpan);
  const textTop = measureVerticalSpan.getBoundingClientRect().top;
  measureVerticalSpan.remove();
  measureSpan.remove();
  
  // Find the input's container (tags-input-container) to position relative to it
  const inputContainer = input.closest(".tags-input-container") || input.parentElement;
  if (!inputContainer) return;
  const containerRect = inputContainer.getBoundingClientRect();
  
  // Position absolutely relative to the input container
  // Use measured text position for accurate vertical alignment
  overlay.style.position = "absolute";
  overlay.style.left = `${inputRect.left - containerRect.left + borderLeft + paddingLeft + textWidth - input.scrollLeft}px`;
  overlay.style.top = `${textTop - containerRect.top}px`;
  overlay.style.fontSize = style.fontSize;
  overlay.style.fontFamily = style.fontFamily;
  overlay.style.fontWeight = style.fontWeight;
  overlay.style.fontStyle = style.fontStyle;
  overlay.style.letterSpacing = style.letterSpacing;
  overlay.style.textTransform = style.textTransform;
  // pointer-events: none on desktop (CSS); auto on mobile so tap accepts suggestion
  overlay.style.zIndex = "10000";
  overlay.style.lineHeight = style.lineHeight;
  overlay.style.color = "var(--muted)";

  // On mobile there is no Tab key; single tap on the suggestion accepts it
  overlay.addEventListener("click", (e) => {
    e.preventDefault();
    acceptAutocompleteSuggestion();
  });

  // Ensure container has relative positioning for absolute children
  const containerStyle = window.getComputedStyle(inputContainer);
  if (containerStyle.position === "static") {
    (inputContainer as HTMLElement).style.position = "relative";
  }

  // Append to the input container so it's positioned relative to it
  inputContainer.appendChild(overlay);
}

function handleTagInput(e: Event): void {
  updateTagAutocomplete();
}

function handleTagKeydown(e: KeyboardEvent): void {
  if (!permissions.canEditTags) return;
  if (getAutocompleteSuggestion() && (e.key === "Tab" || e.key === "Enter")) {
    e.preventDefault();
    acceptAutocompleteSuggestion();
  } else if (e.key === "Escape") {
    setAutocompleteSuggestion(null);
    renderTagAutocomplete();
  } else if (e.key === "Enter" && !getAutocompleteSuggestion()) {
    e.preventDefault();
    addTagFromInput();
  } else if (e.key === "Tab" && !getAutocompleteSuggestion() && getTagInput()?.value.trim()) {
    e.preventDefault();
    addTagFromInput();
  } else if (e.key === "," && !getAutocompleteSuggestion()) {
    e.preventDefault();
    addTagFromInput();
  }
}

function acceptAutocompleteSuggestion(): void {
  if (!permissions.canEditTags) return;
  if (!getAutocompleteSuggestion()) return;
  
  // Normalize the suggestion to ensure proper capitalization
  const normalized = normalizeTagName(getAutocompleteSuggestion()!);
  addTag(normalized);
  setAutocompleteSuggestion(null);
  renderTagAutocomplete();
}

function addTag(tagName: string): void {
  if (!permissions.canEditTags) return;
  const trimmed = tagName.trim();
  if (!trimmed) return;
  
  // Normalize to existing tag capitalization if it exists
  const normalized = normalizeTagName(trimmed);
  
  const currentTags = getTagsFromChips();
  // Check for duplicates (case-insensitive)
  if (currentTags.some(t => t.toLowerCase() === normalized.toLowerCase())) {
    return; // Don't add duplicates
  }
  
  currentTags.push(normalized);
  renderTagsChips(currentTags, { canRemove: permissions.canEditTags });
  const input = getTagInput();
  if (input) input.value = "";
  updateTagAutocomplete();
}

export function removeTag(tagName: string): void {
  if (!permissions.canEditTags) return;
  const currentTags = getTagsFromChips();
  const filtered = currentTags.filter(t => t !== tagName);
  renderTagsChips(filtered, { canRemove: permissions.canEditTags });
  updateTagAutocomplete();
}

function addTagFromInput(): void {
  if (!permissions.canEditTags) return;
  const input = getTagInput();
  if (!input) return;
  const value = input.value.trim();
  if (!value) return;
  
  // If there's an autocomplete suggestion, use that
  if (getAutocompleteSuggestion()) {
    acceptAutocompleteSuggestion();
    return;
  }
  
  // Otherwise, add the current input value
  const tags = value.split(",").map(t => t.trim()).filter(Boolean);
  tags.forEach(tag => addTag(tag));
  input.value = "";
}

export function setupTagAutocomplete(): void {
  setAutocompleteSuggestion(null);
  const input = getTagInput();
  if (!input) return;

  // Only setup once per element (tagInputHandlersSetup is reset when we clone)
  if (tagInputHandlersSetup) {
    updateTagAutocomplete();
    return;
  }

  input.addEventListener("input", handleTagInput);
  input.addEventListener("keydown", handleTagKeydown);
  input.addEventListener("blur", () => {
    setTimeout(() => {
      setAutocompleteSuggestion(null);
      renderTagAutocomplete();
    }, 200);
  });

  if (addTagBtn && !(addTagBtn as any)[BOUND_FLAG]) {
    (addTagBtn as any)[BOUND_FLAG] = true;
    addTagBtn.addEventListener("click", () => {
      addTagFromInput();
      getTagInput()?.focus();
    });
  }

  tagInputHandlersSetup = true;
  updateTagAutocomplete();
}


function clearLinkSearchInFlight(): void {
  if (linksSearchDebounce) {
    clearTimeout(linksSearchDebounce);
    linksSearchDebounce = null;
  }
  if (linksSearchController) {
    linksSearchController.abort();
    linksSearchController = null;
  }
}

function removeLinksAutocompleteOverlay(): void {
  const existing = document.getElementById("linksAutocompleteSuggestion");
  if (existing) existing.remove();
}

function formatLinkedStoryLabel(item: LinkedStoryItem): string {
  return `#${item.localId} ${item.title || ""}`.trim();
}

function getLinkedStorySuggestionText(item: LinkedStoryItem, q: string): string | null {
  const label = formatLinkedStoryLabel(item);
  const normalizedQ = q.toLowerCase();
  if (label.toLowerCase().startsWith(normalizedQ)) return label;
  const title = (item.title || "").trim();
  if (title.toLowerCase().startsWith(normalizedQ)) return title;
  const hashID = `#${item.localId}`;
  if (hashID.toLowerCase().startsWith(normalizedQ)) return hashID;
  return null;
}

function renderLinksAutocomplete(input: HTMLInputElement): void {
  removeLinksAutocompleteOverlay();
  if (!linkAutocompleteSuggestion) return;

  const q = input.value.trim();
  if (!q) return;

  const suggestionText = getLinkedStorySuggestionText(linkAutocompleteSuggestion, q);
  if (!suggestionText) return;
  let remaining = suggestionText.substring(q.length);
  if (!remaining) return;
  remaining = remaining.replace(/^\s+/, ""); // avoid double space if suggestion had leading space
  if (!remaining) return;

  const overlay = document.createElement("div");
  overlay.id = "linksAutocompleteSuggestion";
  overlay.className = "tag-autocomplete-suggestion";
  overlay.textContent = " " + remaining;

  const inputRect = input.getBoundingClientRect();
  const style = window.getComputedStyle(input);
  const paddingLeft = parseFloat(style.paddingLeft) || 0;
  const paddingTop = parseFloat(style.paddingTop) || 0;
  const borderLeft = parseFloat(style.borderLeftWidth) || 0;
  const borderTop = parseFloat(style.borderTopWidth) || 0;

  const measureSpan = document.createElement("span");
  measureSpan.style.position = "absolute";
  measureSpan.style.visibility = "hidden";
  measureSpan.style.whiteSpace = "pre";
  measureSpan.style.fontSize = style.fontSize;
  measureSpan.style.fontFamily = style.fontFamily;
  measureSpan.style.fontWeight = style.fontWeight;
  measureSpan.style.fontStyle = style.fontStyle;
  measureSpan.style.letterSpacing = style.letterSpacing;
  measureSpan.style.padding = "0";
  measureSpan.style.margin = "0";
  measureSpan.style.border = "none";
  measureSpan.style.lineHeight = style.lineHeight;
  measureSpan.textContent = q;
  document.body.appendChild(measureSpan);
  const textWidth = measureSpan.getBoundingClientRect().width;
  measureSpan.remove();

  const inputContainer = input.closest(".tags-input-container") || input.parentElement;
  if (!inputContainer) return;
  const containerRect = inputContainer.getBoundingClientRect();

  overlay.style.position = "absolute";
  overlay.style.left = `${inputRect.left - containerRect.left + borderLeft + paddingLeft + textWidth - input.scrollLeft}px`;
  overlay.style.top = `${inputRect.top - containerRect.top + borderTop + paddingTop}px`;
  overlay.style.fontSize = style.fontSize;
  overlay.style.fontFamily = style.fontFamily;
  overlay.style.fontWeight = style.fontWeight;
  overlay.style.fontStyle = style.fontStyle;
  overlay.style.letterSpacing = style.letterSpacing;
  overlay.style.textTransform = style.textTransform;
  overlay.style.zIndex = "10000";
  overlay.style.lineHeight = style.lineHeight;
  overlay.style.color = "var(--muted)";
  overlay.style.whiteSpace = "pre";
  overlay.addEventListener("click", (e) => {
    e.preventDefault();
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  });

  const containerStyle = window.getComputedStyle(inputContainer);
  if (containerStyle.position === "static") {
    (inputContainer as HTMLElement).style.position = "relative";
  }
  inputContainer.appendChild(overlay);
}

function getAllLinkedLocalIDs(): number[] {
  const ids = new Set<number>();
  currentLinks.outbound.forEach((l) => ids.add(l.localId));
  currentLinks.inbound.forEach((l) => ids.add(l.localId));
  return Array.from(ids.values());
}

async function addLinkedStoryByLocalID(
  slug: string,
  currentLocalId: number,
  targetLocalId: number,
  onNavigateToLinkedTodo?: (path: string) => void
): Promise<void> {
  if (!targetLocalId || targetLocalId === currentLocalId) return;
  recordLocalMutation();
  await apiFetch(`/api/board/${slug}/todos/${currentLocalId}/links`, {
    method: "POST",
    body: JSON.stringify({ targetLocalId }),
  });
  lastLoadedLinksForTodo = null;
  await loadLinksForTodo(slug, currentLocalId);
  renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo);
}

function parseLocalIDFromLinkInput(raw: string): number | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const match = trimmed.match(/^#?(\d+)$/);
  if (!match) return null;
  const parsed = parseInt(match[1], 10);
  return parsed > 0 ? parsed : null;
}

function renderLinksChips(
  slug: string,
  currentLocalId: number,
  onNavigateToLinkedTodo?: (path: string) => void
): void {
  const container = document.getElementById("linksChips");
  if (!container) return;

  const outbound = currentLinks.outbound.map((item) => {
    const removeBtn = permissions.canEditLinks
      ? `<button type="button" class="tag-chip-remove" data-link-remove="${item.localId}" aria-label="Remove link">×</button>`
      : "";
    return `
    <span class="tag-chip" data-link-local-id="${item.localId}" data-link-direction="outbound">
      <button type="button" class="tag-chip-link" data-link-open="${item.localId}">#${item.localId} ${escapeHTML(item.title)}</button>
      ${removeBtn}
    </span>
  `;
  }).join("");

  const inbound = currentLinks.inbound.map((item) => `
    <span class="tag-chip" data-link-local-id="${item.localId}" data-link-direction="inbound">
      <button type="button" class="tag-chip-link" data-link-open="${item.localId}">#${item.localId} ${escapeHTML(item.title)}</button>
    </span>
  `).join("");

  container.innerHTML = `${outbound}${inbound}`;

  container.querySelectorAll("[data-link-open]").forEach((btn) => {
    if (!(btn as any)[BOUND_FLAG]) {
      (btn as any)[BOUND_FLAG] = true;
      btn.addEventListener("click", () => {
        const id = parseInt((btn as HTMLElement).getAttribute("data-link-open") || "0", 10);
        if (!id) return;
        const nextPath = `/${slug}/t/${id}`;
        if (onNavigateToLinkedTodo) {
          onNavigateToLinkedTodo(nextPath);
        }
      });
    }
  });

  container.querySelectorAll("[data-link-remove]").forEach((btn) => {
    if (!(btn as any)[BOUND_FLAG]) {
      (btn as any)[BOUND_FLAG] = true;
      btn.addEventListener("click", async (e) => {
        e.stopPropagation();
        const id = parseInt((btn as HTMLElement).getAttribute("data-link-remove") || "0", 10);
        if (!id) return;
        try {
          recordLocalMutation();
          await apiFetch(`/api/board/${slug}/todos/${currentLocalId}/links/${id}`, { method: "DELETE" });
          lastLoadedLinksForTodo = null;
          await loadLinksForTodo(slug, currentLocalId);
          renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo);
        } catch (err: any) {
          showToast(err.message || "Failed to remove link");
        }
      });
    }
  });
}

async function loadLinksForTodo(slug: string, localId: number): Promise<void> {
  const alreadyLoaded = !!lastLoadedLinksForTodo &&
    lastLoadedLinksForTodo.slug === slug &&
    lastLoadedLinksForTodo.localId === localId;
  if (alreadyLoaded) return;

  const res = await apiFetch<LinkedStoriesResponse>(`/api/board/${slug}/todos/${localId}/links`);
  currentLinks = {
    outbound: Array.isArray(res?.outbound) ? res.outbound : [],
    inbound: Array.isArray(res?.inbound) ? res.inbound : [],
  };
  lastLoadedLinksForTodo = { slug, localId };
}

function setupLinkedStoriesSearch(
  slug: string,
  currentLocalId: number,
  onNavigateToLinkedTodo?: (path: string) => void
): void {
  const existing = document.getElementById("linksSearchInput") as HTMLInputElement | null;
  if (!existing || !existing.parentNode) return;
  const existingAddBtn = document.getElementById("addLinkBtn") as HTMLButtonElement | null;
  const input = existing.cloneNode(true) as HTMLInputElement;
  existing.parentNode.replaceChild(input, existing);
  let addBtn: HTMLButtonElement | null = null;
  if (existingAddBtn && existingAddBtn.parentNode) {
    addBtn = existingAddBtn.cloneNode(true) as HTMLButtonElement;
    existingAddBtn.parentNode.replaceChild(addBtn, existingAddBtn);
  }
  input.value = "";
  linkAutocompleteSuggestion = null;
  removeLinksAutocompleteOverlay();
  clearLinkSearchInFlight();

  if (!permissions.canEditLinks) {
    input.disabled = true;
    input.placeholder = "";
    if (addBtn) addBtn.disabled = true;
    return;
  }

  const submitLinkFromInput = async (): Promise<void> => {
    const directLocalID = parseLocalIDFromLinkInput(input.value);
    const target = linkAutocompleteSuggestion?.localId ?? directLocalID;
    if (!target) {
      showToast("Type #id or title, then tap Add");
      return;
    }
    try {
      await addLinkedStoryByLocalID(slug, currentLocalId, target, onNavigateToLinkedTodo);
      input.value = "";
      linkAutocompleteSuggestion = null;
      removeLinksAutocompleteOverlay();
      clearLinkSearchInFlight();
    } catch (err: any) {
      showToast(err.message || "Failed to link story");
    }
  };

  const updateAutocomplete = () => {
    const q = input.value.trim();
    clearLinkSearchInFlight();
    if (!q) {
      linkAutocompleteSuggestion = null;
      removeLinksAutocompleteOverlay();
      return;
    }
    linksSearchDebounce = setTimeout(async () => {
      linksSearchDebounce = null;
      const exclude = Array.from(new Set<number>([currentLocalId, ...getAllLinkedLocalIDs()])).join(",");
      const searchQ = q.match(/^#(\d+)$/)?.[1] ?? q;
      linksSearchController = new AbortController();
      try {
        const params = new URLSearchParams();
        params.set("q", searchQ);
        params.set("limit", "20");
        if (exclude) params.set("exclude", exclude);
        const list = await apiFetch<LinkedStoryItem[]>(
          `/api/board/${slug}/todos/search?${params.toString()}`,
          { signal: linksSearchController.signal } as RequestInit
        );
        const items = Array.isArray(list) ? list : [];
        linkAutocompleteSuggestion = items.length > 0 ? items[0] : null;
        renderLinksAutocomplete(input);
      } catch (err: any) {
        if (err?.name === "AbortError") return;
        showToast(err.message || "Failed to search stories");
      } finally {
        linksSearchController = null;
      }
    }, 300);
  };

  input.addEventListener("input", updateAutocomplete);
  input.addEventListener("keydown", async (e) => {
    if (e.key === "Tab" || e.key === "Enter") {
      e.preventDefault();
      await submitLinkFromInput();
      return;
    }
    if (e.key === "Escape") {
      linkAutocompleteSuggestion = null;
      removeLinksAutocompleteOverlay();
    }
  });
  if (addBtn) {
    addBtn.addEventListener("click", async () => {
      await submitLinkFromInput();
      input.focus();
    });
  }
  input.addEventListener("blur", () => {
    setTimeout(() => {
      removeLinksAutocompleteOverlay();
    }, 150);
  });
}

function bindDialogLinkLifecycle(): void {
  if (dialogLinkLifecycleBound) return;
  dialogLinkLifecycleBound = true;
  (todoDialog as HTMLDialogElement).addEventListener("close", () => {
    clearLinkSearchInFlight();
    linkAutocompleteSuggestion = null;
    removeLinksAutocompleteOverlay();
    lastLoadedLinksForTodo = null;
    currentLinks = { outbound: [], inbound: [] };
  });
}

function bindShareTodoButton(): void {
  if (!shareTodoBtn || (shareTodoBtn as any)[BOUND_FLAG]) return;
  (shareTodoBtn as any)[BOUND_FLAG] = true;
  shareTodoBtn.addEventListener("click", async () => {
    const slug = getSlug();
    const editing = getEditingTodo();
    if (!slug || !editing?.localId) {
      showToast("Cannot share: no story in context");
      return;
    }
    const url = `${window.location.origin}/${slug}/t/${editing.localId}`;
    const title = editing.title ? `${editing.title} (#${editing.localId})` : `Story #${editing.localId}`;
    if (typeof navigator.share === "function") {
      try {
        await navigator.share({
          url,
          title: title,
          text: editing.title || undefined,
        });
        showToast("Link shared");
      } catch (err: any) {
        if (err?.name !== "AbortError") {
          showToast(err?.message || "Share failed");
        }
      }
    } else {
      try {
        await navigator.clipboard.writeText(url);
        showToast("Link copied");
      } catch {
        showToast("Share not supported");
      }
    }
  });
}

export async function openTodoDialog(opts: { mode: string; todo?: any; status?: string; onNavigateToLinkedTodo?: (path: string) => void; role?: string | null }): Promise<void> {
  const { mode, todo, status, onNavigateToLinkedTodo } = opts;
  setEditingTodo(mode === "edit" ? todo : null);
  bindDialogLinkLifecycle();

  // Compute permissions once (mode-aware so create never inherits stale assignment state)
  const board = getBoard();
  const anonymousBoard = isAnonymousBoard(board);
  const isMaintainer = (opts.role ?? "") === "maintainer" || anonymousBoard;
  const roleNorm = (opts.role ?? "").toLowerCase();
  const isContributor = roleNorm === "contributor" || roleNorm === "editor";
  const currentUser = getUser();
  const isAssignedToMe =
    currentUser &&
    mode === "edit" &&
    Number(todo?.assigneeUserId) === Number(currentUser.id);
  const canEditTitle = isMaintainer;
  const canEditStatus = isMaintainer;
  const canSubmitTodo =
    mode === "create"
      ? isMaintainer || anonymousBoard
      : isMaintainer || (!anonymousBoard && isContributor && !!isAssignedToMe);
  const canEditLinks = isMaintainer || (!anonymousBoard && isContributor);
  permissions = {
    canChangeSprint: isMaintainer && !anonymousBoard,
    canChangeEstimation: isMaintainer,
    canEditTags: isMaintainer,
    canEditNotes: isMaintainer || (!anonymousBoard && isContributor && !!isAssignedToMe),
    canEditAssignment: isMaintainer && !anonymousBoard,
    canDeleteTodo: isMaintainer,
    canEditTitle,
    canEditStatus,
    canSubmitTodo,
    canEditLinks,
  };

  // Fetch available tags for autocomplete
  // Authenticated boards: fetch ALL user-owned tags from full library (/api/tags/mine)
  //   This allows autocomplete to suggest tags not yet used on this board
  // Anonymous boards: fetch board-scoped tags (/api/board/{slug}/tags)
  if (getSlug()) {
    try {
      let tagsResponse: any[];
      if (getUser()) {
        // Authenticated: fetch ALL user-owned tags from full library (cross-project)
        // This allows autocomplete to suggest tags from other projects
        tagsResponse = await apiFetch(`/api/tags/mine`) as any[];
      } else {
        // Anonymous: fetch board-scoped tags (only tags used on this board)
        tagsResponse = await apiFetch(`/api/board/${getSlug()}/tags`) as any[];
      }
      
      // Extract tag names from the response (tags are objects with name and color)
      setAvailableTags(tagsResponse.map((tag: any) => typeof tag === 'string' ? tag : tag.name));
      // Build map for case-insensitive lookup (lowercase -> proper capitalization)
      const tagsMap: Record<string, string> = {};
      tagsResponse.forEach((tag: any) => {
        const tagName = typeof tag === 'string' ? tag : tag.name;
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
    // No slug - no autocomplete
    setAvailableTags([]);
    setAvailableTagsMap({});
  }

  // Assignee field: visible when board supports assignments (not anonymous).
  // Contributors see it but dropdown is disabled; maintainers can change assignment.
  const assigneeField = document.getElementById("todoAssigneeField");
  const assigneeSelect = document.getElementById("todoAssignee") as HTMLSelectElement | null;
  const showAssignee =
    assigneeField && assigneeSelect && !isAnonymousBoard(getBoard());
  if (assigneeField) {
    assigneeField.style.display = showAssignee ? "" : "none";
  }

  // Sprint field: visible when board is not anonymous, has slug, and user is Maintainer
  const sprintField = document.getElementById("todoSprintField");
  const sprintSelect = document.getElementById("todoSprint") as HTMLSelectElement | null;
  const showSprint = sprintField && sprintSelect && !isAnonymousBoard(getBoard()) && !!getSlug() && opts.role === "maintainer";
  if (sprintField) {
    sprintField.style.display = showSprint ? "" : "none";
  }
  if (sprintSelect) {
    if (!showSprint) {
      sprintSelect.value = "";
    } else {
      try {
        const res = await apiFetch<{ sprints?: { id: number; name: string; state: string }[] } | null>(`/api/board/${getSlug()}/sprints`);
        const sprints = normalizeSprints(res);
        const defaultOpt = document.createElement("option");
        defaultOpt.value = "";
        defaultOpt.textContent = "—";
        const options: HTMLOptionElement[] = [defaultOpt];
        for (const sp of sprints) {
          const opt = document.createElement("option");
          opt.value = String(sp.id);
          opt.textContent = `${sp.name} (${sp.state})`;
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
      // Only maintainers can assign to others; contributors see only Unassigned + self
      const user = getUser();
      const members = getBoardMembers();
      const myMember = user ? members.find((m) => m.userId === user.id) : null;
      const canAssignOthers = myMember?.role === "maintainer";

      assigneeSelect.innerHTML = "";
      const unassigned = document.createElement("option");
      unassigned.value = "";
      unassigned.textContent = "Unassigned";
      assigneeSelect.appendChild(unassigned);

      if (canAssignOthers) {
        for (const m of members) {
          const opt = document.createElement("option");
          opt.value = String(m.userId);
          opt.textContent = m.name || m.email || String(m.userId);
          assigneeSelect.appendChild(opt);
        }
      } else {
        // Contributor (or non-maintainer): in edit mode show current assignee as disabled if different from self; then self only
        if (mode === "edit") {
          const currentAssigneeId = todo?.assigneeUserId;
          if (
            currentAssigneeId != null &&
            user &&
            Number(currentAssigneeId) !== Number(user.id)
          ) {
            const assigneeMember = members.find(
              (m) => Number(m.userId) === Number(currentAssigneeId)
            );
            if (assigneeMember) {
              const opt = document.createElement("option");
              opt.value = String(assigneeMember.userId);
              opt.textContent = `Current: ${assigneeMember.name || assigneeMember.email || String(assigneeMember.userId)}`;
              opt.disabled = true;
              assigneeSelect.appendChild(opt);
            }
          }
        }
        if (user) {
          const opt = document.createElement("option");
          opt.value = String(user.id);
          opt.textContent = user.name || user.email || "Me";
          assigneeSelect.appendChild(opt);
        }
      }
      assigneeSelect.value = todo?.assigneeUserId != null ? String(todo.assigneeUserId) : "";
    } else {
      assigneeSelect.innerHTML = '<option value="">Unassigned</option>';
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
      await loadLinksForTodo(slug!, todo.localId);
      renderLinksChips(slug!, todo.localId, onNavigateToLinkedTodo);
      setupLinkedStoriesSearch(slug!, todo.localId, onNavigateToLinkedTodo);
    } catch (err: any) {
      showToast(err.message || "Failed to load linked stories");
    }
  } else {
    const linksChips = document.getElementById("linksChips");
    if (linksChips) linksChips.innerHTML = "";
    clearLinkSearchInFlight();
    linkAutocompleteSuggestion = null;
    removeLinksAutocompleteOverlay();
    currentLinks = { outbound: [], inbound: [] };
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
  const updatedEl = document.getElementById("todoDialogUpdated") as HTMLElement | null;
  const formatDate = (d: string) =>
    new Date(d).toLocaleString(undefined, { year: "2-digit", month: "numeric", day: "numeric", hour: "numeric", minute: "2-digit" });
  const setDates = (createdAt: string | undefined, updatedAt: string | undefined) => {
    if (createdEl) {
      const valueEl = createdEl.querySelector(".todo-dialog-datetime-value");
      if (createdAt == null) {
        if (valueEl) (valueEl as HTMLElement).textContent = "";
        createdEl.setAttribute("aria-hidden", "true");
      } else {
        if (valueEl) (valueEl as HTMLElement).textContent = formatDate(createdAt);
        createdEl.setAttribute("aria-hidden", "false");
      }
    }
    if (updatedEl) {
      const valueEl = updatedEl.querySelector(".todo-dialog-datetime-value");
      if (updatedAt == null) {
        if (valueEl) (valueEl as HTMLElement).textContent = "";
        updatedEl.setAttribute("aria-hidden", "true");
      } else {
        if (valueEl) (valueEl as HTMLElement).textContent = formatDate(updatedAt);
        updatedEl.setAttribute("aria-hidden", "false");
      }
    }
  };

  if (mode === "create") {
    (todoDialogTitle as HTMLElement).textContent = "New Todo";
    (todoTitle as HTMLInputElement).value = "";
    (todoBody as HTMLTextAreaElement).value = "";
    (todoTags as HTMLInputElement).value = "";
    const initialKey = resolveColumnKey(status);
    const selected = populateTodoStatusOptions(initialKey);
    (todoStatus as HTMLSelectElement).value = selected;
    (deleteTodoBtn as HTMLElement).style.display = "none";
    if (shareTodoBtn) (shareTodoBtn as HTMLElement).style.display = "none";
    setDates(undefined, undefined);
  } else {
    (todoDialogTitle as HTMLElement).textContent = permissions.canSubmitTodo ? "Edit Todo" : "View Todo";
    (todoTitle as HTMLInputElement).value = todo.title || "";
    (todoBody as HTMLTextAreaElement).value = todo.body || "";
    (todoTags as HTMLInputElement).value = "";
    const initialKey = resolveColumnKey(todo.columnKey || todo.status);
    const selected = populateTodoStatusOptions(initialKey);
    (todoStatus as HTMLSelectElement).value = selected;
    (deleteTodoBtn as HTMLElement).style.display = permissions.canDeleteTodo ? "" : "none";
    if (shareTodoBtn) (shareTodoBtn as HTMLElement).style.display = "";
    setDates(todo.createdAt, todo.updatedAt);
  }

  // 1. Clone tag input to clear previous autocomplete listeners (maintainer→contributor would otherwise keep handlers)
  const tagInputEl = document.getElementById("todoTags") as HTMLInputElement | null;
  if (tagInputEl) {
    tagInputEl.replaceWith(tagInputEl.cloneNode(true));
    tagInputHandlersSetup = false;
  }
  // 2. Refetch (clone clears input; tags live in chips)
  const tagInputRefetched = document.getElementById("todoTags") as HTMLInputElement | null;
  if (tagInputRefetched) {
    tagInputRefetched.value = "";
  }

  // 3. Reset block (every open) - set disabled/readOnly from current permissions (use getTagInput for tag input after clone)
  if (assigneeSelect) assigneeSelect.disabled = !permissions.canEditAssignment;
  if (estimationSelect) estimationSelect.disabled = !permissions.canChangeEstimation;
  const tagInput = getTagInput();
  if (tagInput) tagInput.disabled = !permissions.canEditTags;
  if (addTagBtn) (addTagBtn as HTMLButtonElement).disabled = !permissions.canEditTags;
  (todoBody as HTMLTextAreaElement).readOnly = !permissions.canEditNotes;
  (todoTitle as HTMLInputElement).readOnly = !permissions.canEditTitle;
  (todoStatus as HTMLSelectElement).disabled = !permissions.canEditStatus;
  const saveTodoBtn = document.getElementById("saveTodoBtn") as HTMLButtonElement | null;
  if (saveTodoBtn) saveTodoBtn.disabled = !permissions.canSubmitTodo;

  // 4. Tag chips: clear and re-render so old chips (with "×" from previous maintainer open) are not reused
  const tagsChips = document.getElementById("tagsChips");
  if (tagsChips) tagsChips.innerHTML = "";
  const tagsToShow = mode === "create" ? [] : (todo?.tags || []);
  renderTagsChips(tagsToShow, { canRemove: permissions.canEditTags });

  // 5. Setup autocomplete only when editable
  if (permissions.canEditTags) {
    setupTagAutocomplete();
  }

  bindShareTodoButton();

  (todoDialog as HTMLDialogElement).showModal();
  // On touch devices, avoid focusing the title input (causes keyboard to pop up).
  // Run after the next frame so default dialog focus/layout settle without arbitrary timer delay.
  let userChoseFocus = false;
  const ac = new AbortController();
  todoDialog.addEventListener(
    "pointerdown",
    () => {
      userChoseFocus = true;
    },
    { capture: true, signal: ac.signal }
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

/**
 * Reset assignee select on dialog close: clear all options, re-add only "Unassigned".
 * Prevents stale values across different edit sessions. Do not mutate boardMembers.
 */
export function resetAssigneeSelect(): void {
  const assigneeSelect = document.getElementById("todoAssignee") as HTMLSelectElement | null;
  if (assigneeSelect) {
    assigneeSelect.innerHTML = '<option value="">Unassigned</option>';
  }
  const estimationSelect = todoEstimationPoints as HTMLSelectElement | null;
  if (estimationSelect) {
    estimationSelect.value = "";
  }
}
