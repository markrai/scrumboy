import { addTagBtn } from '../dom/elements.js';
import { getAutocompleteSuggestion, getAvailableTags, getAvailableTagsMap, getTagColors, } from '../state/selectors.js';
import { setAutocompleteSuggestion } from '../state/mutations.js';
import { escapeHTML, sanitizeHexColor } from '../utils.js';
import { getTodoFormPermissions } from './todo-permissions.js';
const BOUND_FLAG = Symbol('bound');
let tagInputHandlersSetup = false;
function getTagInput() {
    return document.getElementById("todoTags");
}
function getTagColor(tagName) {
    return getTagColors()[tagName] || null;
}
export function resetTodoTagAutocompleteBindings() {
    tagInputHandlersSetup = false;
}
export function getTagsFromChips() {
    const chipsContainer = document.getElementById("tagsChips");
    if (!chipsContainer)
        return [];
    return Array.from(chipsContainer.querySelectorAll(".tag-chip")).map((chip) => chip.getAttribute("data-tag") || "");
}
export function normalizeTagName(tagName) {
    const lowerTag = tagName.toLowerCase();
    if (getAvailableTagsMap()[lowerTag]) {
        return getAvailableTagsMap()[lowerTag];
    }
    const currentTags = getTagsFromChips();
    const existingTag = currentTags.find((t) => t.toLowerCase() === lowerTag);
    if (existingTag) {
        return existingTag;
    }
    return tagName;
}
export function renderTagsChips(tags, opts) {
    const chipsContainer = document.getElementById("tagsChips");
    if (!chipsContainer)
        return;
    const canRemove = opts?.canRemove ?? true;
    chipsContainer.innerHTML = tags
        .map((tagName) => {
        const tagColor = getTagColor(tagName);
        const safe = sanitizeHexColor(tagColor);
        const colorStyle = safe
            ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"`
            : "";
        const removeBtn = canRemove
            ? '<button type="button" class="tag-chip-remove" aria-label="Remove tag">×</button>'
            : "";
        return `
      <span class="tag-chip" data-tag="${escapeHTML(tagName)}" ${colorStyle}>
        ${escapeHTML(tagName)}
        ${removeBtn}
      </span>
    `;
    })
        .join("");
    chipsContainer.querySelectorAll(".tag-chip-remove").forEach((btn) => {
        if (!btn[BOUND_FLAG]) {
            btn.addEventListener("click", (e) => {
                if (!getTodoFormPermissions().canEditTags)
                    return;
                e.stopPropagation();
                const chip = btn.closest(".tag-chip");
                const tagName = chip?.getAttribute("data-tag");
                if (tagName) {
                    removeTag(tagName);
                }
            });
            btn[BOUND_FLAG] = true;
        }
    });
}
function updateTagAutocomplete() {
    const input = getTagInput();
    if (!input)
        return;
    const value = input.value;
    const cursorPos = input.selectionStart || 0;
    const beforeCursor = value.substring(0, cursorPos);
    const lastCommaIndex = beforeCursor.lastIndexOf(",");
    const currentTagRaw = beforeCursor.substring(lastCommaIndex + 1);
    const currentTag = currentTagRaw.trim();
    if (currentTag.length === 0 || getAvailableTags().length === 0) {
        setAutocompleteSuggestion(null);
        renderTagAutocomplete();
        return;
    }
    const fullValue = input.value;
    const existingTags = fullValue
        .split(",")
        .map((t) => t.trim().toLowerCase())
        .filter((t) => t.length > 0 && t !== currentTag.toLowerCase());
    const matchingTag = getAvailableTags().find((tag) => {
        const tagLower = tag.toLowerCase();
        const currentTagLower = currentTag.toLowerCase();
        return (tagLower.startsWith(currentTagLower) &&
            tagLower !== currentTagLower &&
            !existingTags.includes(tagLower));
    });
    if (matchingTag) {
        setAutocompleteSuggestion(normalizeTagName(matchingTag));
    }
    else {
        setAutocompleteSuggestion(null);
    }
    renderTagAutocomplete();
}
export function renderTagAutocomplete() {
    const existing = document.getElementById("tagAutocompleteSuggestion");
    if (existing) {
        existing.remove();
    }
    if (!getAutocompleteSuggestion()) {
        return;
    }
    const input = getTagInput();
    if (!input)
        return;
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
    if (!suggestion)
        return;
    const remaining = suggestion.substring(currentTag.length);
    if (remaining.length === 0)
        return;
    const overlay = document.createElement("div");
    overlay.id = "tagAutocompleteSuggestion";
    overlay.className = "tag-autocomplete-suggestion";
    overlay.textContent = remaining;
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
    measureSpan.textContent = beforeCursor;
    document.body.appendChild(measureSpan);
    const textWidth = measureSpan.getBoundingClientRect().width;
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
    measureVerticalSpan.textContent = "X";
    measureVerticalSpan.style.top = `${inputRect.top + borderTop + paddingTop}px`;
    measureVerticalSpan.style.left = `${inputRect.left + borderLeft + paddingLeft}px`;
    document.body.appendChild(measureVerticalSpan);
    const textTop = measureVerticalSpan.getBoundingClientRect().top;
    measureVerticalSpan.remove();
    measureSpan.remove();
    const inputContainer = input.closest(".tags-input-container") || input.parentElement;
    if (!inputContainer)
        return;
    const containerRect = inputContainer.getBoundingClientRect();
    overlay.style.position = "absolute";
    overlay.style.left = `${inputRect.left - containerRect.left + borderLeft + paddingLeft + textWidth - input.scrollLeft}px`;
    overlay.style.top = `${textTop - containerRect.top}px`;
    overlay.style.fontSize = style.fontSize;
    overlay.style.fontFamily = style.fontFamily;
    overlay.style.fontWeight = style.fontWeight;
    overlay.style.fontStyle = style.fontStyle;
    overlay.style.letterSpacing = style.letterSpacing;
    overlay.style.textTransform = style.textTransform;
    overlay.style.zIndex = "10000";
    overlay.style.lineHeight = style.lineHeight;
    overlay.style.color = "var(--muted)";
    overlay.addEventListener("click", (e) => {
        e.preventDefault();
        acceptAutocompleteSuggestion();
    });
    const containerStyle = window.getComputedStyle(inputContainer);
    if (containerStyle.position === "static") {
        inputContainer.style.position = "relative";
    }
    inputContainer.appendChild(overlay);
}
function handleTagInput() {
    updateTagAutocomplete();
}
function handleTagKeydown(e) {
    if (!getTodoFormPermissions().canEditTags)
        return;
    if (getAutocompleteSuggestion() && (e.key === "Tab" || e.key === "Enter")) {
        e.preventDefault();
        acceptAutocompleteSuggestion();
    }
    else if (e.key === "Escape") {
        setAutocompleteSuggestion(null);
        renderTagAutocomplete();
    }
    else if (e.key === "Enter" && !getAutocompleteSuggestion()) {
        e.preventDefault();
        addTagFromInput();
    }
    else if (e.key === "Tab" && !getAutocompleteSuggestion() && getTagInput()?.value.trim()) {
        e.preventDefault();
        addTagFromInput();
    }
    else if (e.key === "," && !getAutocompleteSuggestion()) {
        e.preventDefault();
        addTagFromInput();
    }
}
function acceptAutocompleteSuggestion() {
    if (!getTodoFormPermissions().canEditTags)
        return;
    if (!getAutocompleteSuggestion())
        return;
    const normalized = normalizeTagName(getAutocompleteSuggestion());
    addTag(normalized);
    setAutocompleteSuggestion(null);
    renderTagAutocomplete();
}
function addTag(tagName) {
    if (!getTodoFormPermissions().canEditTags)
        return;
    const trimmed = tagName.trim();
    if (!trimmed)
        return;
    const normalized = normalizeTagName(trimmed);
    const currentTags = getTagsFromChips();
    if (currentTags.some((t) => t.toLowerCase() === normalized.toLowerCase())) {
        return;
    }
    currentTags.push(normalized);
    renderTagsChips(currentTags, { canRemove: getTodoFormPermissions().canEditTags });
    const input = getTagInput();
    if (input)
        input.value = "";
    updateTagAutocomplete();
}
export function removeTag(tagName) {
    if (!getTodoFormPermissions().canEditTags)
        return;
    const currentTags = getTagsFromChips();
    const filtered = currentTags.filter((t) => t !== tagName);
    renderTagsChips(filtered, { canRemove: getTodoFormPermissions().canEditTags });
    updateTagAutocomplete();
}
function addTagFromInput() {
    if (!getTodoFormPermissions().canEditTags)
        return;
    const input = getTagInput();
    if (!input)
        return;
    const value = input.value.trim();
    if (!value)
        return;
    if (getAutocompleteSuggestion()) {
        acceptAutocompleteSuggestion();
        return;
    }
    const tags = value.split(",").map((t) => t.trim()).filter(Boolean);
    tags.forEach((tag) => addTag(tag));
    input.value = "";
}
export function setupTagAutocomplete() {
    setAutocompleteSuggestion(null);
    const input = getTagInput();
    if (!input)
        return;
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
    if (addTagBtn && !addTagBtn[BOUND_FLAG]) {
        addTagBtn[BOUND_FLAG] = true;
        addTagBtn.addEventListener("click", () => {
            addTagFromInput();
            getTagInput()?.focus();
        });
    }
    tagInputHandlersSetup = true;
    updateTagAutocomplete();
}
