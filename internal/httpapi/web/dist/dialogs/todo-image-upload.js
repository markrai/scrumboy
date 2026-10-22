import { apiFetchForm } from "../api.js";
import { hasI18nKey, t } from "../i18n/index.js";
import { showToast } from "../utils.js";
export const ACCEPTED_IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif"];
export const MAX_FILES_PER_ACTION = 5;
/** Advisory client check; the server limit is authoritative. */
export const CLIENT_MAX_BYTES = 10 * 1024 * 1024;
const MAX_ALT_LENGTH = 100;
const PLACEHOLDER_SCHEME = "scrumboy-upload:";
function message(key, fallback, values = {}) {
    return hasI18nKey(key) ? t(key, values) : fallback;
}
function isAcceptedImage(file) {
    return ACCEPTED_IMAGE_TYPES.includes(file.type);
}
function uploadId() {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
        return crypto.randomUUID();
    }
    return `${Date.now().toString(16)}-${Math.random().toString(16).slice(2)}`;
}
/** Alt text from the filename: no extension, no Markdown-breaking characters, bounded length. */
export function altTextForFile(name) {
    const base = name.replace(/\.[^.]*$/, "");
    const cleaned = base
        .replace(/[\u0000-\u001f\u007f[\]()\\]/g, " ")
        .replace(/\s+/g, " ")
        .trim()
        .slice(0, MAX_ALT_LENGTH)
        .trim();
    if (!cleaned || cleaned.toLowerCase() === "image") {
        return message("todo.images.defaultAlt", "image");
    }
    return cleaned;
}
function uploadErrorMessage(err) {
    const status = err?.status;
    const reason = err?.data?.error?.details?.reason;
    if (status === 413)
        return message("todo.images.tooLarge", "Image is too large (max 10 MB).");
    if (status === 400 && reason === "unsupported_image_type") {
        return message("todo.images.unsupported", "Only PNG, JPEG and GIF images are supported.");
    }
    if (status === 429)
        return message("todo.images.rateLimited", "Too many uploads. Please wait and try again.");
    if (status === 503)
        return message("todo.images.busy", "The server is busy. Please try again.");
    if (status === 507)
        return message("todo.images.storageFull", "Image storage for this board is full.");
    if (status === 401 || status === 403 || status === 404) {
        return message("todo.images.notAllowed", "You cannot add images to this story.");
    }
    return message("todo.images.uploadFailed", "Image upload failed.");
}
function isAbort(err) {
    return err?.name === "AbortError";
}
/**
 * Handles image paste, drop and picker uploads for the notes textarea. Each upload inserts a
 * unique placeholder that is replaced with canonical Markdown on success or removed on failure.
 */
export function createTodoImageUploader(opts) {
    const { textarea } = opts;
    const inflight = new Map();
    let batchTotal = 0;
    let attached = false;
    const notify = () => {
        if (inflight.size === 0) {
            batchTotal = 0;
        }
        opts.onPendingChange(inflight.size, batchTotal);
    };
    const placeholderFor = (id) => `![${message("todo.images.uploadPlaceholder", "Uploading image…")}](${PLACEHOLDER_SCHEME}${id})`;
    const dispatchInput = () => textarea.dispatchEvent(new Event("input", { bubbles: true }));
    // replacePlaceholder swaps the placeholder line (found by its unique id) for replacement,
    // keeping the caret stable. Returns false if the user removed the placeholder.
    const replacePlaceholder = (id, replacement) => {
        const placeholder = placeholderFor(id);
        const value = textarea.value;
        const start = value.indexOf(placeholder);
        if (start < 0) {
            return false;
        }
        let end = start + placeholder.length;
        if (replacement === "" && value[end] === "\n") {
            end += 1;
        }
        const selStart = textarea.selectionStart;
        const selEnd = textarea.selectionEnd;
        const delta = replacement.length - (end - start);
        textarea.value = value.slice(0, start) + replacement + value.slice(end);
        const shift = (pos) => (pos >= end ? pos + delta : Math.min(pos, start + replacement.length));
        textarea.setSelectionRange(shift(selStart), shift(selEnd));
        dispatchInput();
        return true;
    };
    const startUpload = (file, ctx) => {
        const id = uploadId();
        const placeholder = placeholderFor(id);
        const max = textarea.maxLength > 0 ? textarea.maxLength : Infinity;
        if (textarea.value.length + placeholder.length + 2 > max) {
            showToast(message("todo.images.notesFull", "The notes are too long to add another image."));
            return;
        }
        const start = textarea.selectionStart ?? textarea.value.length;
        const end = textarea.selectionEnd ?? start;
        textarea.setRangeText(placeholder + "\n", start, end, "end");
        dispatchInput();
        const controller = new AbortController();
        inflight.set(id, { id, controller });
        batchTotal += 1;
        notify();
        const form = new FormData();
        form.append("file", file, file.name || "image");
        const query = ctx.todoLocalId != null ? `?todo=${encodeURIComponent(String(ctx.todoLocalId))}` : "";
        const alt = altTextForFile(file.name || "");
        apiFetchForm(`/api/board/${encodeURIComponent(ctx.slug)}/assets${query}`, form, { signal: controller.signal })
            .then((res) => {
            if (!inflight.has(id)) {
                return;
            }
            replacePlaceholder(id, `![${alt}](${res.url})`);
        })
            .catch((err) => {
            if (isAbort(err) || controller.signal.aborted) {
                replacePlaceholder(id, "");
                return;
            }
            replacePlaceholder(id, "");
            showToast(uploadErrorMessage(err));
        })
            .finally(() => {
            inflight.delete(id);
            notify();
        });
    };
    const uploadFiles = (files) => {
        const ctx = opts.getContext();
        if (!ctx || textarea.readOnly) {
            return;
        }
        if (files.length > MAX_FILES_PER_ACTION) {
            showToast(message("todo.images.tooMany", `You can add up to ${MAX_FILES_PER_ACTION} images at a time.`, { count: MAX_FILES_PER_ACTION }));
        }
        for (const file of files.slice(0, MAX_FILES_PER_ACTION)) {
            if (!isAcceptedImage(file)) {
                showToast(message("todo.images.unsupported", "Only PNG, JPEG and GIF images are supported."));
                continue;
            }
            if (file.size > CLIENT_MAX_BYTES) {
                showToast(message("todo.images.tooLarge", "Image is too large (max 10 MB)."));
                continue;
            }
            startUpload(file, ctx);
        }
    };
    const onPaste = (event) => {
        if (textarea.readOnly || !opts.getContext())
            return;
        const data = event.clipboardData;
        if (!data)
            return;
        const images = Array.from(data.files ?? []).filter(isAcceptedImage);
        // Text plus an image (for example a spreadsheet copy) pastes as text, as the user expects.
        if (images.length === 0 || (data.getData("text/plain") || "").trim() !== "")
            return;
        event.preventDefault();
        uploadFiles(images);
    };
    const hasImageFiles = (dt) => {
        if (!dt || !Array.from(dt.types ?? []).includes("Files"))
            return [];
        return Array.from(dt.files ?? []).filter(isAcceptedImage);
    };
    const onDragOver = (event) => {
        if (textarea.readOnly || !opts.getContext())
            return;
        if (!Array.from(event.dataTransfer?.types ?? []).includes("Files"))
            return;
        event.preventDefault();
        textarea.classList.add("todo-notes-input--drop-target");
    };
    const onDragLeave = () => textarea.classList.remove("todo-notes-input--drop-target");
    const onDrop = (event) => {
        textarea.classList.remove("todo-notes-input--drop-target");
        if (textarea.readOnly || !opts.getContext())
            return;
        const images = hasImageFiles(event.dataTransfer);
        if (images.length === 0)
            return;
        event.preventDefault();
        uploadFiles(images);
    };
    return {
        attach() {
            if (attached)
                return;
            attached = true;
            textarea.addEventListener("paste", onPaste);
            textarea.addEventListener("dragover", onDragOver);
            textarea.addEventListener("dragleave", onDragLeave);
            textarea.addEventListener("drop", onDrop);
        },
        detach() {
            if (!attached)
                return;
            attached = false;
            textarea.removeEventListener("paste", onPaste);
            textarea.removeEventListener("dragover", onDragOver);
            textarea.removeEventListener("dragleave", onDragLeave);
            textarea.removeEventListener("drop", onDrop);
        },
        abortAll() {
            for (const upload of Array.from(inflight.values())) {
                upload.controller.abort();
            }
        },
        pending() {
            return inflight.size;
        },
        pickFiles(files) {
            uploadFiles(Array.from(files));
        },
    };
}
