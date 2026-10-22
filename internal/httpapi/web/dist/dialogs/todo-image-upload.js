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
/**
 * Alt text from the filename: no extension, no Markdown-breaking characters, bounded length.
 * "|" is removed too, so a filename can never look like a stored display width.
 */
export function altTextForFile(name) {
    const base = name.replace(/\.[^.]*$/, "");
    const cleaned = base
        .replace(/[\u0000-\u001f\u007f[\]()\\|`]/g, " ")
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
function placeholderFor(id) {
    return `![${message("todo.images.uploadPlaceholder", "Uploading image…")}](${PLACEHOLDER_SCHEME}${id})`;
}
/**
 * The textarea target: each upload inserts a unique placeholder line at the caret (in place of
 * any selected text), replaced with the image's Markdown on success. On failure the placeholder
 * is removed and the text it replaced is put back, keeping the caret stable.
 */
export function createTextareaUploadTarget(textarea) {
    const dispatchInput = () => textarea.dispatchEvent(new Event("input", { bubbles: true }));
    const replacedText = new Map();
    return {
        element: textarea,
        isReadOnly: () => textarea.readOnly,
        begin(id) {
            const placeholder = placeholderFor(id);
            const max = textarea.maxLength > 0 ? textarea.maxLength : Infinity;
            if (textarea.value.length + placeholder.length + 2 > max) {
                return false;
            }
            const start = textarea.selectionStart ?? textarea.value.length;
            const end = textarea.selectionEnd ?? start;
            if (end > start)
                replacedText.set(id, textarea.value.slice(start, end));
            textarea.setRangeText(placeholder + "\n", start, end, "end");
            dispatchInput();
            return true;
        },
        finish(id, markdown) {
            const restored = replacedText.get(id) ?? "";
            replacedText.delete(id);
            const replacement = markdown ?? restored;
            const placeholder = placeholderFor(id);
            const value = textarea.value;
            const start = value.indexOf(placeholder);
            if (start < 0) {
                // The user removed the placeholder: nothing to insert, and nothing went wrong.
                return true;
            }
            let end = start + placeholder.length;
            if (markdown === null && value[end] === "\n") {
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
        },
        setDropTarget(active) {
            textarea.classList.toggle("todo-notes-input--drop-target", active);
        },
    };
}
/**
 * Handles image paste and drop uploads for the notes. Each upload reserves its place in the
 * target, which receives the image's canonical Markdown on success; failures remove it.
 */
export function createTodoImageUploader(opts) {
    const target = opts.target ?? createTextareaUploadTarget(opts.textarea);
    const inflight = new Map();
    let batchTotal = 0;
    let attached = false;
    const notify = () => {
        if (inflight.size === 0) {
            batchTotal = 0;
        }
        opts.onPendingChange(inflight.size, batchTotal);
    };
    const notesFull = () => showToast(message("todo.images.notesFull", "The notes are too long to add another image."));
    const startUpload = (file, ctx, batch, index) => {
        const id = uploadId();
        const alt = altTextForFile(file.name || "");
        // The final Markdown, ![alt](/api/assets/<36-character id>), plus line breaks around it.
        const length = alt.length + "![]()".length + "/api/assets/".length + 36 + 2;
        if (!target.begin(id, { batch, index, length })) {
            notesFull();
            return;
        }
        const controller = new AbortController();
        inflight.set(id, { id, controller });
        batchTotal += 1;
        notify();
        const form = new FormData();
        form.append("file", file, file.name || "image");
        const query = ctx.todoLocalId != null ? `?todo=${encodeURIComponent(String(ctx.todoLocalId))}` : "";
        apiFetchForm(`/api/board/${encodeURIComponent(ctx.slug)}/assets${query}`, form, { signal: controller.signal })
            .then((res) => {
            if (!inflight.has(id)) {
                return;
            }
            if (!target.finish(id, `![${alt}](${res.url})`)) {
                notesFull();
            }
        })
            .catch((err) => {
            target.finish(id, null);
            if (isAbort(err) || controller.signal.aborted) {
                return;
            }
            showToast(uploadErrorMessage(err));
        })
            .finally(() => {
            inflight.delete(id);
            notify();
        });
    };
    const uploadFiles = (files) => {
        const ctx = opts.getContext();
        if (!ctx || target.isReadOnly()) {
            return;
        }
        if (files.length > MAX_FILES_PER_ACTION) {
            showToast(message("todo.images.tooMany", `You can add up to ${MAX_FILES_PER_ACTION} images at a time.`, { count: MAX_FILES_PER_ACTION }));
        }
        const batch = uploadId();
        let index = 0;
        for (const file of files.slice(0, MAX_FILES_PER_ACTION)) {
            if (!isAcceptedImage(file)) {
                showToast(message("todo.images.unsupported", "Only PNG, JPEG and GIF images are supported."));
                continue;
            }
            if (file.size > CLIENT_MAX_BYTES) {
                showToast(message("todo.images.tooLarge", "Image is too large (max 10 MB)."));
                continue;
            }
            startUpload(file, ctx, batch, index);
            index += 1;
        }
    };
    const handlePaste = (event) => {
        if (target.isReadOnly() || !opts.getContext())
            return false;
        const data = event.clipboardData;
        if (!data)
            return false;
        const images = Array.from(data.files ?? []).filter(isAcceptedImage);
        // Text plus an image (for example a spreadsheet copy) pastes as text, as the user expects.
        if (images.length === 0 || (data.getData("text/plain") || "").trim() !== "")
            return false;
        event.preventDefault();
        uploadFiles(images);
        return true;
    };
    const imageFiles = (dt) => {
        if (!dt || !Array.from(dt.types ?? []).includes("Files"))
            return [];
        return Array.from(dt.files ?? []).filter(isAcceptedImage);
    };
    const handleDragOver = (event) => {
        if (target.isReadOnly() || !opts.getContext())
            return false;
        if (!Array.from(event.dataTransfer?.types ?? []).includes("Files"))
            return false;
        event.preventDefault();
        target.setDropTarget(true);
        return true;
    };
    const handleDragLeave = () => target.setDropTarget(false);
    const handleDrop = (event) => {
        target.setDropTarget(false);
        if (target.isReadOnly() || !opts.getContext())
            return false;
        const images = imageFiles(event.dataTransfer);
        if (images.length === 0)
            return false;
        event.preventDefault();
        uploadFiles(images);
        return true;
    };
    const onPaste = (event) => void handlePaste(event);
    const onDragOver = (event) => void handleDragOver(event);
    const onDrop = (event) => void handleDrop(event);
    return {
        handlePaste,
        handleDragOver,
        handleDragLeave,
        handleDrop,
        attach() {
            if (attached)
                return;
            attached = true;
            target.element.addEventListener("paste", onPaste);
            target.element.addEventListener("dragover", onDragOver);
            target.element.addEventListener("dragleave", handleDragLeave);
            target.element.addEventListener("drop", onDrop);
        },
        detach() {
            if (!attached)
                return;
            attached = false;
            target.element.removeEventListener("paste", onPaste);
            target.element.removeEventListener("dragover", onDragOver);
            target.element.removeEventListener("dragleave", handleDragLeave);
            target.element.removeEventListener("drop", onDrop);
        },
        abortAll() {
            for (const upload of Array.from(inflight.values())) {
                upload.controller.abort();
            }
        },
        pending() {
            return inflight.size;
        },
    };
}
