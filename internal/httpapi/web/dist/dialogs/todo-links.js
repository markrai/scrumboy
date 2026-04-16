import { shareTodoBtn, todoDialog } from '../dom/elements.js';
import { apiFetch } from '../api.js';
import { getEditingTodo, getSlug } from '../state/selectors.js';
import { escapeHTML, showToast } from '../utils.js';
import { recordLocalMutation } from '../realtime/guard.js';
import { getTodoFormPermissions } from './todo-permissions.js';
const BOUND_FLAG = Symbol('bound');
let linksSearchDebounce = null;
let linksSearchController = null;
let lastLoadedLinksForTodo = null;
let dialogLinkLifecycleBound = false;
let currentLinks = { outbound: [], inbound: [] };
let linkAutocompleteSuggestion = null;
function clearLinkSearchInFlight() {
    if (linksSearchDebounce) {
        clearTimeout(linksSearchDebounce);
        linksSearchDebounce = null;
    }
    if (linksSearchController) {
        linksSearchController.abort();
        linksSearchController = null;
    }
}
function removeLinksAutocompleteOverlay() {
    const existing = document.getElementById("linksAutocompleteSuggestion");
    if (existing)
        existing.remove();
}
function formatLinkedStoryLabel(item) {
    return `#${item.localId} ${item.title || ""}`.trim();
}
function getLinkedStorySuggestionText(item, q) {
    const label = formatLinkedStoryLabel(item);
    const normalizedQ = q.toLowerCase();
    if (label.toLowerCase().startsWith(normalizedQ))
        return label;
    const title = (item.title || "").trim();
    if (title.toLowerCase().startsWith(normalizedQ))
        return title;
    const hashID = `#${item.localId}`;
    if (hashID.toLowerCase().startsWith(normalizedQ))
        return hashID;
    return null;
}
function renderLinksAutocomplete(input) {
    removeLinksAutocompleteOverlay();
    if (!linkAutocompleteSuggestion)
        return;
    const q = input.value.trim();
    if (!q)
        return;
    const suggestionText = getLinkedStorySuggestionText(linkAutocompleteSuggestion, q);
    if (!suggestionText)
        return;
    let remaining = suggestionText.substring(q.length);
    if (!remaining)
        return;
    remaining = remaining.replace(/^\s+/, "");
    if (!remaining)
        return;
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
    if (!inputContainer)
        return;
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
        inputContainer.style.position = "relative";
    }
    inputContainer.appendChild(overlay);
}
function getAllLinkedLocalIDs() {
    const ids = new Set();
    currentLinks.outbound.forEach((l) => ids.add(l.localId));
    currentLinks.inbound.forEach((l) => ids.add(l.localId));
    return Array.from(ids.values());
}
async function addLinkedStoryByLocalID(slug, currentLocalId, targetLocalId, onNavigateToLinkedTodo) {
    if (!targetLocalId || targetLocalId === currentLocalId)
        return;
    recordLocalMutation();
    await apiFetch(`/api/board/${slug}/todos/${currentLocalId}/links`, {
        method: "POST",
        body: JSON.stringify({ targetLocalId }),
    });
    lastLoadedLinksForTodo = null;
    await loadLinksForTodo(slug, currentLocalId);
    renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo);
}
function parseLocalIDFromLinkInput(raw) {
    const trimmed = raw.trim();
    if (!trimmed)
        return null;
    const match = trimmed.match(/^#?(\d+)$/);
    if (!match)
        return null;
    const parsed = parseInt(match[1], 10);
    return parsed > 0 ? parsed : null;
}
function renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo) {
    const container = document.getElementById("linksChips");
    if (!container)
        return;
    const outbound = currentLinks.outbound
        .map((item) => {
        const removeBtn = getTodoFormPermissions().canEditLinks
            ? `<button type="button" class="tag-chip-remove" data-link-remove="${item.localId}" aria-label="Remove link">×</button>`
            : "";
        return `
    <span class="tag-chip" data-link-local-id="${item.localId}" data-link-direction="outbound">
      <button type="button" class="tag-chip-link" data-link-open="${item.localId}">#${item.localId} ${escapeHTML(item.title)}</button>
      ${removeBtn}
    </span>
  `;
    })
        .join("");
    const inbound = currentLinks.inbound
        .map((item) => `
    <span class="tag-chip" data-link-local-id="${item.localId}" data-link-direction="inbound">
      <button type="button" class="tag-chip-link" data-link-open="${item.localId}">#${item.localId} ${escapeHTML(item.title)}</button>
    </span>
  `)
        .join("");
    container.innerHTML = `${outbound}${inbound}`;
    container.querySelectorAll("[data-link-open]").forEach((btn) => {
        if (!btn[BOUND_FLAG]) {
            btn[BOUND_FLAG] = true;
            btn.addEventListener("click", () => {
                const id = parseInt(btn.getAttribute("data-link-open") || "0", 10);
                if (!id)
                    return;
                const nextPath = `/${slug}/t/${id}`;
                if (onNavigateToLinkedTodo) {
                    onNavigateToLinkedTodo(nextPath);
                }
            });
        }
    });
    container.querySelectorAll("[data-link-remove]").forEach((btn) => {
        if (!btn[BOUND_FLAG]) {
            btn[BOUND_FLAG] = true;
            btn.addEventListener("click", async (e) => {
                e.stopPropagation();
                const id = parseInt(btn.getAttribute("data-link-remove") || "0", 10);
                if (!id)
                    return;
                try {
                    recordLocalMutation();
                    await apiFetch(`/api/board/${slug}/todos/${currentLocalId}/links/${id}`, {
                        method: "DELETE",
                    });
                    lastLoadedLinksForTodo = null;
                    await loadLinksForTodo(slug, currentLocalId);
                    renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo);
                }
                catch (err) {
                    showToast(err.message || "Failed to remove link");
                }
            });
        }
    });
}
async function loadLinksForTodo(slug, localId) {
    const alreadyLoaded = !!lastLoadedLinksForTodo &&
        lastLoadedLinksForTodo.slug === slug &&
        lastLoadedLinksForTodo.localId === localId;
    if (alreadyLoaded)
        return;
    const res = await apiFetch(`/api/board/${slug}/todos/${localId}/links`);
    currentLinks = {
        outbound: Array.isArray(res?.outbound) ? res.outbound : [],
        inbound: Array.isArray(res?.inbound) ? res.inbound : [],
    };
    lastLoadedLinksForTodo = { slug, localId };
}
function setupLinkedStoriesSearch(slug, currentLocalId, onNavigateToLinkedTodo) {
    const existing = document.getElementById("linksSearchInput");
    if (!existing || !existing.parentNode)
        return;
    const existingAddBtn = document.getElementById("addLinkBtn");
    const input = existing.cloneNode(true);
    existing.parentNode.replaceChild(input, existing);
    let addBtn = null;
    if (existingAddBtn && existingAddBtn.parentNode) {
        addBtn = existingAddBtn.cloneNode(true);
        existingAddBtn.parentNode.replaceChild(addBtn, existingAddBtn);
    }
    input.value = "";
    linkAutocompleteSuggestion = null;
    removeLinksAutocompleteOverlay();
    clearLinkSearchInFlight();
    if (!getTodoFormPermissions().canEditLinks) {
        input.disabled = true;
        input.placeholder = "";
        if (addBtn)
            addBtn.disabled = true;
        return;
    }
    const submitLinkFromInput = async () => {
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
        }
        catch (err) {
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
            const exclude = Array.from(new Set([currentLocalId, ...getAllLinkedLocalIDs()])).join(",");
            const searchQ = q.match(/^#(\d+)$/)?.[1] ?? q;
            linksSearchController = new AbortController();
            try {
                const params = new URLSearchParams();
                params.set("q", searchQ);
                params.set("limit", "20");
                if (exclude)
                    params.set("exclude", exclude);
                const list = await apiFetch(`/api/board/${slug}/todos/search?${params.toString()}`, { signal: linksSearchController.signal });
                const items = Array.isArray(list) ? list : [];
                linkAutocompleteSuggestion = items.length > 0 ? items[0] : null;
                renderLinksAutocomplete(input);
            }
            catch (err) {
                if (err?.name === "AbortError")
                    return;
                showToast(err.message || "Failed to search stories");
            }
            finally {
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
export async function initializeTodoDialogLinks(slug, currentLocalId, onNavigateToLinkedTodo) {
    await loadLinksForTodo(slug, currentLocalId);
    renderLinksChips(slug, currentLocalId, onNavigateToLinkedTodo);
    setupLinkedStoriesSearch(slug, currentLocalId, onNavigateToLinkedTodo);
}
export function resetTodoDialogLinks() {
    const linksChips = document.getElementById("linksChips");
    if (linksChips)
        linksChips.innerHTML = "";
    clearLinkSearchInFlight();
    linkAutocompleteSuggestion = null;
    removeLinksAutocompleteOverlay();
    currentLinks = { outbound: [], inbound: [] };
}
export function bindTodoDialogLinkLifecycle() {
    if (dialogLinkLifecycleBound)
        return;
    dialogLinkLifecycleBound = true;
    todoDialog.addEventListener("close", () => {
        clearLinkSearchInFlight();
        linkAutocompleteSuggestion = null;
        removeLinksAutocompleteOverlay();
        lastLoadedLinksForTodo = null;
        currentLinks = { outbound: [], inbound: [] };
    });
}
export function bindShareTodoButton() {
    if (!shareTodoBtn || shareTodoBtn[BOUND_FLAG])
        return;
    shareTodoBtn[BOUND_FLAG] = true;
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
            }
            catch (err) {
                if (err?.name !== "AbortError") {
                    showToast(err?.message || "Share failed");
                }
            }
        }
        else {
            try {
                await navigator.clipboard.writeText(url);
                showToast("Link copied");
            }
            catch {
                showToast("Share not supported");
            }
        }
    });
}
