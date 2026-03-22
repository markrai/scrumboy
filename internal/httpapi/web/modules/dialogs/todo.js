"use strict";
var __assign = (this && this.__assign) || function () {
    __assign = Object.assign || function(t) {
        for (var s, i = 1, n = arguments.length; i < n; i++) {
            s = arguments[i];
            for (var p in s) if (Object.prototype.hasOwnProperty.call(s, p))
                t[p] = s[p];
        }
        return t;
    };
    return __assign.apply(this, arguments);
};
var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
var __generator = (this && this.__generator) || function (thisArg, body) {
    var _ = { label: 0, sent: function() { if (t[0] & 1) throw t[1]; return t[1]; }, trys: [], ops: [] }, f, y, t, g = Object.create((typeof Iterator === "function" ? Iterator : Object).prototype);
    return g.next = verb(0), g["throw"] = verb(1), g["return"] = verb(2), typeof Symbol === "function" && (g[Symbol.iterator] = function() { return this; }), g;
    function verb(n) { return function (v) { return step([n, v]); }; }
    function step(op) {
        if (f) throw new TypeError("Generator is already executing.");
        while (g && (g = 0, op[0] && (_ = 0)), _) try {
            if (f = 1, y && (t = op[0] & 2 ? y["return"] : op[0] ? y["throw"] || ((t = y["return"]) && t.call(y), 0) : y.next) && !(t = t.call(y, op[1])).done) return t;
            if (y = 0, t) op = [op[0] & 2, t.value];
            switch (op[0]) {
                case 0: case 1: t = op; break;
                case 4: _.label++; return { value: op[1], done: false };
                case 5: _.label++; y = op[1]; op = [0]; continue;
                case 7: op = _.ops.pop(); _.trys.pop(); continue;
                default:
                    if (!(t = _.trys, t = t.length > 0 && t[t.length - 1]) && (op[0] === 6 || op[0] === 2)) { _ = 0; continue; }
                    if (op[0] === 3 && (!t || (op[1] > t[0] && op[1] < t[3]))) { _.label = op[1]; break; }
                    if (op[0] === 6 && _.label < t[1]) { _.label = t[1]; t = op; break; }
                    if (t && _.label < t[2]) { _.label = t[2]; _.ops.push(op); break; }
                    if (t[2]) _.ops.pop();
                    _.trys.pop(); continue;
            }
            op = body.call(thisArg, _);
        } catch (e) { op = [6, e]; y = 0; } finally { f = t = 0; }
        if (op[0] & 5) throw op[1]; return { value: op[0] ? op[1] : void 0, done: true };
    }
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.getTagsFromChips = getTagsFromChips;
exports.renderTagsChips = renderTagsChips;
exports.renderTagAutocomplete = renderTagAutocomplete;
exports.removeTag = removeTag;
exports.setupTagAutocomplete = setupTagAutocomplete;
exports.setMoveButtonsEnabled = setMoveButtonsEnabled;
exports.openTodoDialog = openTodoDialog;
var elements_js_1 = require("../dom/elements.js");
var api_js_1 = require("../api.js");
var selectors_js_1 = require("../state/selectors.js");
var mutations_js_1 = require("../state/mutations.js");
var utils_js_1 = require("../utils.js");
// Module-level state for tag autocomplete
var tagInputHandlersSetup = false;
// Helper functions
function getTagColor(tagName) {
    return (0, selectors_js_1.getTagColors)()[tagName] || null;
}
function getTagsFromChips() {
    var chipsContainer = document.getElementById("tagsChips");
    if (!chipsContainer)
        return [];
    return Array.from(chipsContainer.querySelectorAll(".tag-chip")).map(function (chip) { return chip.getAttribute("data-tag") || ""; });
}
function normalizeTagName(tagName) {
    // Check if there's an existing tag with the same name (case-insensitive)
    var lowerTag = tagName.toLowerCase();
    if ((0, selectors_js_1.getAvailableTagsMap)()[lowerTag]) {
        return (0, selectors_js_1.getAvailableTagsMap)()[lowerTag];
    }
    // Also check currently added tags in the chips (case-insensitive)
    var currentTags = getTagsFromChips();
    var existingTag = currentTags.find(function (t) { return t.toLowerCase() === lowerTag; });
    if (existingTag) {
        return existingTag;
    }
    // No existing tag found, return the input as-is
    return tagName;
}
function renderTagsChips(tags) {
    var chipsContainer = document.getElementById("tagsChips");
    if (!chipsContainer)
        return;
    chipsContainer.innerHTML = tags.map(function (tagName) {
        var tagColor = getTagColor(tagName);
        var colorStyle = tagColor ? "style=\"border-color: ".concat(tagColor, "; background: ").concat(tagColor, "20; color: ").concat(tagColor, ";\"") : "";
        return "\n      <span class=\"tag-chip\" data-tag=\"".concat((0, utils_js_1.escapeHTML)(tagName), "\" ").concat(colorStyle, ">\n        ").concat((0, utils_js_1.escapeHTML)(tagName), "\n        <button type=\"button\" class=\"tag-chip-remove\" aria-label=\"Remove tag\">\u00D7</button>\n      </span>\n    ");
    }).join("");
    // Add remove handlers
    chipsContainer.querySelectorAll(".tag-chip-remove").forEach(function (btn) {
        btn.addEventListener("click", function (e) {
            e.stopPropagation();
            var chip = btn.closest(".tag-chip");
            var tagName = chip === null || chip === void 0 ? void 0 : chip.getAttribute("data-tag");
            if (tagName) {
                removeTag(tagName);
            }
        });
    });
}
function updateTagAutocomplete() {
    var input = elements_js_1.todoTags;
    var value = input.value;
    var cursorPos = input.selectionStart || 0;
    // Find the current tag being typed (last segment after comma)
    var beforeCursor = value.substring(0, cursorPos);
    var lastCommaIndex = beforeCursor.lastIndexOf(",");
    var currentTagRaw = beforeCursor.substring(lastCommaIndex + 1);
    var currentTag = currentTagRaw.trim();
    if (currentTag.length === 0 || (0, selectors_js_1.getAvailableTags)().length === 0) {
        (0, mutations_js_1.setAutocompleteSuggestion)(null);
        renderTagAutocomplete();
        return;
    }
    // Get all tags that have already been entered (excluding the current one being typed)
    var fullValue = input.value;
    var existingTags = fullValue
        .split(",")
        .map(function (t) { return t.trim().toLowerCase(); })
        .filter(function (t) { return t.length > 0 && t !== currentTag.toLowerCase(); });
    // Find matching tag (case-insensitive prefix match) that hasn't been used yet
    var matchingTag = (0, selectors_js_1.getAvailableTags)().find(function (tag) {
        var tagLower = tag.toLowerCase();
        var currentTagLower = currentTag.toLowerCase();
        return tagLower.startsWith(currentTagLower) &&
            tagLower !== currentTagLower &&
            !existingTags.includes(tagLower);
    });
    // Use normalized version if found (to get proper capitalization)
    if (matchingTag) {
        (0, mutations_js_1.setAutocompleteSuggestion)(normalizeTagName(matchingTag));
    }
    else {
        (0, mutations_js_1.setAutocompleteSuggestion)(null);
    }
    // Update the visual suggestion
    renderTagAutocomplete();
}
function renderTagAutocomplete() {
    // Remove existing suggestion overlay
    var existing = document.getElementById("tagAutocompleteSuggestion");
    if (existing) {
        existing.remove();
    }
    if (!(0, selectors_js_1.getAutocompleteSuggestion)()) {
        return;
    }
    var input = elements_js_1.todoTags;
    var value = input.value;
    var cursorPos = input.selectionStart || 0;
    var beforeCursor = value.substring(0, cursorPos);
    var lastCommaIndex = beforeCursor.lastIndexOf(",");
    var currentTagRaw = beforeCursor.substring(lastCommaIndex + 1);
    var currentTag = currentTagRaw.trim();
    if (currentTag.length === 0) {
        return;
    }
    var suggestion = (0, selectors_js_1.getAutocompleteSuggestion)();
    if (!suggestion)
        return;
    var remaining = suggestion.substring(currentTag.length);
    if (remaining.length === 0)
        return;
    // Create overlay element for suggestion
    var overlay = document.createElement("div");
    overlay.id = "tagAutocompleteSuggestion";
    overlay.className = "tag-autocomplete-suggestion";
    overlay.textContent = remaining;
    // Position overlay to match input text position
    var inputRect = input.getBoundingClientRect();
    var style = window.getComputedStyle(input);
    var paddingLeft = parseFloat(style.paddingLeft) || 0;
    var paddingTop = parseFloat(style.paddingTop) || 0;
    var borderLeft = parseFloat(style.borderLeftWidth) || 0;
    var borderTop = parseFloat(style.borderTopWidth) || 0;
    // Create temporary span to measure text width (up to cursor)
    var measureSpan = document.createElement("span");
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
    var textWidth = measureSpan.getBoundingClientRect().width;
    // Create another span to measure vertical text position within input
    var measureVerticalSpan = document.createElement("span");
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
    measureVerticalSpan.style.top = "".concat(inputRect.top + borderTop + paddingTop, "px");
    measureVerticalSpan.style.left = "".concat(inputRect.left + borderLeft + paddingLeft, "px");
    document.body.appendChild(measureVerticalSpan);
    var textTop = measureVerticalSpan.getBoundingClientRect().top;
    measureVerticalSpan.remove();
    measureSpan.remove();
    // Find the input's container (tags-input-container) to position relative to it
    var inputContainer = input.closest(".tags-input-container") || input.parentElement;
    if (!inputContainer)
        return;
    var containerRect = inputContainer.getBoundingClientRect();
    // Position absolutely relative to the input container
    // Use measured text position for accurate vertical alignment
    overlay.style.position = "absolute";
    overlay.style.left = "".concat(inputRect.left - containerRect.left + borderLeft + paddingLeft + textWidth - input.scrollLeft, "px");
    overlay.style.top = "".concat(textTop - containerRect.top, "px");
    overlay.style.fontSize = style.fontSize;
    overlay.style.fontFamily = style.fontFamily;
    overlay.style.fontWeight = style.fontWeight;
    overlay.style.fontStyle = style.fontStyle;
    overlay.style.letterSpacing = style.letterSpacing;
    overlay.style.textTransform = style.textTransform;
    overlay.style.pointerEvents = "none";
    overlay.style.zIndex = "10000";
    overlay.style.lineHeight = style.lineHeight;
    overlay.style.color = "var(--muted)";
    // Ensure container has relative positioning for absolute children
    var containerStyle = window.getComputedStyle(inputContainer);
    if (containerStyle.position === "static") {
        inputContainer.style.position = "relative";
    }
    // Append to the input container so it's positioned relative to it
    inputContainer.appendChild(overlay);
}
function handleTagInput(e) {
    updateTagAutocomplete();
}
function handleTagKeydown(e) {
    if ((0, selectors_js_1.getAutocompleteSuggestion)() && (e.key === "Tab" || e.key === "Enter")) {
        e.preventDefault();
        acceptAutocompleteSuggestion();
    }
    else if (e.key === "Escape") {
        (0, mutations_js_1.setAutocompleteSuggestion)(null);
        renderTagAutocomplete();
    }
    else if (e.key === "Enter" && !(0, selectors_js_1.getAutocompleteSuggestion)()) {
        e.preventDefault();
        addTagFromInput();
    }
    else if (e.key === "Tab" && !(0, selectors_js_1.getAutocompleteSuggestion)() && elements_js_1.todoTags.value.trim()) {
        e.preventDefault();
        addTagFromInput();
    }
    else if (e.key === "," && !(0, selectors_js_1.getAutocompleteSuggestion)()) {
        e.preventDefault();
        addTagFromInput();
    }
}
function acceptAutocompleteSuggestion() {
    if (!(0, selectors_js_1.getAutocompleteSuggestion)())
        return;
    // Normalize the suggestion to ensure proper capitalization
    var normalized = normalizeTagName((0, selectors_js_1.getAutocompleteSuggestion)());
    addTag(normalized);
    (0, mutations_js_1.setAutocompleteSuggestion)(null);
    renderTagAutocomplete();
}
function addTag(tagName) {
    var trimmed = tagName.trim();
    if (!trimmed)
        return;
    // Normalize to existing tag capitalization if it exists
    var normalized = normalizeTagName(trimmed);
    var currentTags = getTagsFromChips();
    // Check for duplicates (case-insensitive)
    if (currentTags.some(function (t) { return t.toLowerCase() === normalized.toLowerCase(); })) {
        return; // Don't add duplicates
    }
    currentTags.push(normalized);
    renderTagsChips(currentTags);
    elements_js_1.todoTags.value = "";
    updateTagAutocomplete();
}
function removeTag(tagName) {
    var currentTags = getTagsFromChips();
    var filtered = currentTags.filter(function (t) { return t !== tagName; });
    renderTagsChips(filtered);
    updateTagAutocomplete();
}
function addTagFromInput() {
    var value = elements_js_1.todoTags.value.trim();
    if (!value)
        return;
    // If there's an autocomplete suggestion, use that
    if ((0, selectors_js_1.getAutocompleteSuggestion)()) {
        acceptAutocompleteSuggestion();
        return;
    }
    // Otherwise, add the current input value
    var tags = value.split(",").map(function (t) { return t.trim(); }).filter(Boolean);
    tags.forEach(function (tag) { return addTag(tag); });
    elements_js_1.todoTags.value = "";
}
function setupTagAutocomplete() {
    (0, mutations_js_1.setAutocompleteSuggestion)(null);
    // Only setup once, reuse the same input element
    if (tagInputHandlersSetup) {
        updateTagAutocomplete();
        return;
    }
    elements_js_1.todoTags.addEventListener("input", handleTagInput);
    elements_js_1.todoTags.addEventListener("keydown", handleTagKeydown);
    elements_js_1.todoTags.addEventListener("blur", function () {
        // Clear suggestion on blur (with delay to allow click events)
        setTimeout(function () {
            (0, mutations_js_1.setAutocompleteSuggestion)(null);
            renderTagAutocomplete();
        }, 200);
    });
    tagInputHandlersSetup = true;
    updateTagAutocomplete();
}
function setMoveButtonsEnabled(enabled) {
    [elements_js_1.moveTopBtn, elements_js_1.moveUpBtn, elements_js_1.moveDownBtn, elements_js_1.moveBottomBtn].forEach(function (btn) {
        btn.disabled = !enabled;
    });
}
function openTodoDialog(opts) {
    return __awaiter(this, void 0, void 0, function () {
        var mode, todo, status, tagsResponse, tagsMap_1, err_1;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    mode = opts.mode, todo = opts.todo, status = opts.status;
                    (0, mutations_js_1.setEditingTodo)(mode === "edit" ? todo : null);
                    if (!(0, selectors_js_1.getSlug)()) return [3 /*break*/, 5];
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, api_js_1.apiFetch)("/api/board/".concat((0, selectors_js_1.getSlug)(), "/tags"))];
                case 2:
                    tagsResponse = _a.sent();
                    // Extract tag names from the response (tags are objects with name and color)
                    (0, mutations_js_1.setAvailableTags)(tagsResponse.map(function (tag) { return typeof tag === 'string' ? tag : tag.name; }));
                    tagsMap_1 = {};
                    tagsResponse.forEach(function (tag) {
                        var tagName = typeof tag === 'string' ? tag : tag.name;
                        tagsMap_1[tagName.toLowerCase()] = tagName;
                        if (tag.color) {
                            var tagColors = __assign({}, (0, selectors_js_1.getTagColors)());
                            tagColors[tagName] = tag.color;
                            (0, mutations_js_1.setTagColors)(tagColors);
                        }
                    });
                    (0, mutations_js_1.setAvailableTagsMap)(tagsMap_1);
                    return [3 /*break*/, 4];
                case 3:
                    err_1 = _a.sent();
                    console.error("Failed to fetch tags:", err_1);
                    (0, mutations_js_1.setAvailableTags)([]);
                    (0, mutations_js_1.setAvailableTagsMap)({});
                    return [3 /*break*/, 4];
                case 4: return [3 /*break*/, 6];
                case 5:
                    (0, mutations_js_1.setAvailableTags)([]);
                    (0, mutations_js_1.setAvailableTagsMap)({});
                    _a.label = 6;
                case 6:
                    if (mode === "create") {
                        elements_js_1.todoDialogTitle.textContent = "New Todo";
                        elements_js_1.todoTitle.value = "";
                        elements_js_1.todoBody.value = "";
                        elements_js_1.todoTags.value = "";
                        elements_js_1.todoStatus.value = status || "BACKLOG";
                        elements_js_1.deleteTodoBtn.style.display = "none";
                        elements_js_1.moveBtns.style.display = "none";
                        renderTagsChips([]);
                    }
                    else {
                        elements_js_1.todoDialogTitle.textContent = "Edit Todo";
                        elements_js_1.todoTitle.value = todo.title || "";
                        elements_js_1.todoBody.value = todo.body || "";
                        elements_js_1.todoTags.value = "";
                        elements_js_1.todoStatus.value = todo.status || "BACKLOG";
                        elements_js_1.deleteTodoBtn.style.display = "";
                        elements_js_1.moveBtns.style.display = "";
                        setMoveButtonsEnabled(!((0, selectors_js_1.getTag)() && (0, selectors_js_1.getTag)().trim() !== ""));
                        renderTagsChips(todo.tags || []);
                    }
                    elements_js_1.todoDialog.showModal();
                    elements_js_1.todoTitle.focus();
                    // Setup autocomplete for tags input
                    setupTagAutocomplete();
                    return [2 /*return*/];
            }
        });
    });
}
