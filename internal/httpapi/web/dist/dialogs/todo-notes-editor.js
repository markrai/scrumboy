import { hasI18nKey, t } from "../i18n/index.js";
import { parseNoteMarkdownTokens } from "../markdown-preview.js";
import { NOTE_IMAGE_DEFAULT_MAX_WIDTH, NOTE_IMAGE_MIN_WIDTH, clampImageWidth, findNoteImageRefs, formatImageAlt, } from "../note-images.js";
import { getAppRuntime } from "../platform/runtime.js";
// Unused image files are released after this long without a widget showing them, so scrolling
// an image out of view and back does not acquire it again.
const RESOURCE_GRACE_MS = 30000;
const KEYBOARD_RESIZE_FACTOR = 1.1;
const KEYBOARD_RESIZE_MIN_STEP = 16;
function getCodeMirror() {
    const api = typeof window !== "undefined" ? window.ScrumboyCodeMirror : undefined;
    return api && typeof api.EditorView === "function" ? api : null;
}
const LINE_BREAK_RE = /\r\n|\r|\n/g;
/**
 * The note after edits made to its LF-normalized text (changes, from the loaded text to the
 * current one). Every character the edits did not touch is copied from the original, so its line
 * breaks (CRLF, LF or a lone CR, mixed or not) are kept. A line break the user types or pastes
 * takes the form of the one ending the line it is added to (on the last line, the one before it).
 * The one exception: when an edit leaves lone CRs right before an LF, those CRs become LFs, so
 * the line breaks stay separate instead of a CR and the LF reading as one CRLF.
 */
export function applyEditsPreservingLineBreaks(original, changes) {
    const breaks = [];
    let extra = 0;
    for (const match of original.matchAll(LINE_BREAK_RE)) {
        extra += match[0].length - 1;
        breaks.push({ lf: match.index - (extra - (match[0].length - 1)), extra, text: match[0] });
    }
    const lf = original.replace(LINE_BREAK_RE, "\n");
    // Original offset of an LF-text position; called with ascending positions only.
    let seen = 0;
    const toOriginal = (pos) => {
        while (seen < breaks.length && breaks[seen].lf < pos)
            seen++;
        return pos + (seen > 0 ? breaks[seen - 1].extra : 0);
    };
    let out = "";
    const append = (piece) => {
        if (piece.startsWith("\n") && out.endsWith("\r")) {
            const crs = out.length - out.replace(/\r+$/, "").length;
            out = out.slice(0, -crs) + "\n".repeat(crs);
        }
        out += piece;
    };
    let copied = 0;
    changes.iterChanges((fromA, toA, _fromB, _toB, inserted) => {
        const text = inserted.toString();
        // Leave out what the change keeps as it was (such as text an undo restored), so those
        // characters keep their original bytes too.
        let head = 0;
        while (fromA + head < toA && head < text.length && lf[fromA + head] === text[head])
            head++;
        let tail = 0;
        while (toA - tail > fromA + head && text.length - tail > head && lf[toA - 1 - tail] === text[text.length - 1 - tail])
            tail++;
        const from = toOriginal(fromA + head);
        const lineBreak = (breaks[seen] ?? breaks[seen - 1])?.text ?? "\n";
        append(original.slice(copied, from));
        append(text.slice(head, text.length - tail).replace(/\n/g, lineBreak));
        copied = toOriginal(toA - tail);
    });
    append(original.slice(copied));
    return out;
}
/** True when the CodeMirror vendor bundle loaded; otherwise the plain textarea stays in use. */
export function notesEditorAvailable() {
    return getCodeMirror() !== null;
}
function text(key, fallback, values = {}) {
    return hasI18nKey(key) ? t(key, values) : fallback.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? ""));
}
// ImageResourcePool acquires each image path once through the platform transport (a native
// file handle on Android) and releases it when no widget shows it any more.
class ImageResourcePool {
    constructor() {
        this.entries = new Map();
    }
    attach(path, wrapper, img, alt) {
        let entry = this.entries.get(path);
        if (!entry) {
            entry = { users: new Set(), resource: null, failed: false, controller: new AbortController(), timer: null };
            this.entries.set(path, entry);
            this.acquire(path, entry);
        }
        if (entry.timer) {
            clearTimeout(entry.timer);
            entry.timer = null;
        }
        entry.users.add(wrapper);
        img.addEventListener("load", () => {
            wrapper.classList.remove("todo-notes-image--loading");
            wrapper.dataset.naturalWidth = String(img.naturalWidth || "");
        }, { once: true });
        img.addEventListener("error", () => markUnavailable(wrapper, alt), { once: true });
        if (entry.resource) {
            img.src = entry.resource.url;
        }
        else if (entry.failed) {
            markUnavailable(wrapper, alt);
        }
    }
    detach(path, wrapper) {
        const entry = this.entries.get(path);
        if (!entry || !entry.users.delete(wrapper) || entry.users.size > 0)
            return;
        entry.timer = setTimeout(() => this.drop(path, entry), RESOURCE_GRACE_MS);
    }
    releaseAll() {
        for (const [path, entry] of Array.from(this.entries))
            this.drop(path, entry);
    }
    drop(path, entry) {
        if (this.entries.get(path) !== entry)
            return;
        this.entries.delete(path);
        if (entry.timer)
            clearTimeout(entry.timer);
        entry.controller.abort();
        for (const wrapper of entry.users) {
            wrapper.querySelector("img")?.removeAttribute("src");
        }
        if (entry.resource) {
            try {
                entry.resource.release();
            }
            catch {
                // release() is best-effort and idempotent.
            }
        }
    }
    acquire(path, entry) {
        getAppRuntime()
            .transport()
            .acquireResource(path, { signal: entry.controller.signal })
            .then((resource) => {
            if (this.entries.get(path) !== entry) {
                resource.release();
                return;
            }
            entry.resource = resource;
            for (const wrapper of entry.users) {
                const img = wrapper.querySelector("img");
                if (img)
                    img.src = resource.url;
            }
        })
            .catch(() => {
            if (this.entries.get(path) !== entry)
                return;
            entry.failed = true;
            for (const wrapper of entry.users)
                markUnavailable(wrapper, wrapper.querySelector("img")?.alt ?? "");
        });
    }
}
function markUnavailable(wrapper, alt) {
    wrapper.classList.remove("todo-notes-image--loading");
    wrapper.classList.add("todo-notes-image--unavailable");
    const label = text("todo.markdownPreview.imageUnavailable", "Image unavailable");
    const note = wrapper.querySelector(".todo-notes-image__unavailable") ?? document.createElement("span");
    note.className = "todo-notes-image__unavailable";
    note.textContent = alt ? `${label}: ${alt}` : label;
    wrapper.querySelector("img")?.remove();
    if (!note.parentElement)
        wrapper.prepend(note);
}
export function createNotesEditor(opts) {
    const CM = getCodeMirror();
    if (!CM)
        throw new Error("Notes editor is unavailable: missing /vendor/codemirror.min.js");
    const { Decoration, EditorSelection, EditorState, EditorView, StateEffect, StateField, WidgetType } = CM;
    const pool = new ImageResourcePool();
    const readOnlyCompartment = new CM.Compartment();
    let uploadHandlers = null;
    let original = "";
    let originalLF = "";
    const announce = (message) => opts.announce?.(message);
    // ---- image widgets ----
    class ImageWidget extends WidgetType {
        constructor(path, alt, width) {
            super();
            this.path = path;
            this.alt = alt;
            this.width = width;
        }
        eq(other) {
            return other.path === this.path && other.alt === this.alt && other.width === this.width;
        }
        toDOM(view) {
            const wrapper = document.createElement("span");
            wrapper.className = "todo-notes-image todo-notes-image--editor todo-notes-image--loading";
            wrapper.dataset.path = this.path;
            const img = document.createElement("img");
            img.className = "todo-notes-image__img";
            img.decoding = "async";
            img.referrerPolicy = "no-referrer";
            img.draggable = false;
            wrapper.append(img);
            // One resize handle, on the bottom end corner (bottom-right; bottom-left in right-to-left text).
            const handle = document.createElement("span");
            handle.className = "todo-notes-image__handle";
            handle.setAttribute("aria-hidden", "true");
            handle.addEventListener("pointerdown", (event) => startResize(view, wrapper, event));
            wrapper.append(handle);
            wrapper.addEventListener("mousedown", (event) => event.preventDefault());
            wrapper.addEventListener("click", (event) => {
                event.preventDefault();
                selectImageAt(view, wrapper);
            });
            this.apply(wrapper, img);
            // The widget changes size once the image loads: let CodeMirror update its line heights.
            img.addEventListener("load", () => view.requestMeasure(), { once: true });
            pool.attach(this.path, wrapper, img, this.alt);
            return wrapper;
        }
        updateDOM(dom) {
            if (dom.dataset.path !== this.path)
                return false;
            const img = dom.querySelector("img");
            if (img)
                this.apply(dom, img);
            return true;
        }
        apply(wrapper, img) {
            img.alt = this.alt;
            wrapper.setAttribute("aria-label", text("todo.images.editorImageLabel", "Image: {alt}", { alt: this.alt }));
            wrapper.classList.toggle("todo-notes-image--sized", this.width != null);
            img.style.width = this.width != null ? `${this.width}px` : "";
        }
        destroy(dom) {
            pool.detach(this.path, dom);
        }
        ignoreEvent() {
            return true;
        }
    }
    class UploadingWidget extends WidgetType {
        constructor(id) {
            super();
            this.id = id;
        }
        eq(other) {
            return other.id === this.id;
        }
        toDOM() {
            const el = document.createElement("span");
            el.className = "todo-notes-image todo-notes-image--editor todo-notes-image--uploading";
            el.textContent = text("todo.images.uploadPlaceholder", "Uploading image…");
            return el;
        }
    }
    const computeImages = (state) => {
        let refs = [];
        try {
            const doc = state.doc.toString();
            refs = findNoteImageRefs(doc, parseNoteMarkdownTokens(doc));
        }
        catch {
            refs = [];
        }
        const deco = Decoration.set(refs.map((ref) => Decoration.replace({ widget: new ImageWidget(ref.path, ref.alt, ref.width) }).range(ref.from, ref.to)));
        return { refs, deco };
    };
    const imageField = StateField.define({
        create: computeImages,
        update: (value, tr) => (tr.docChanged ? computeImages(tr.state) : value),
        provide: (field) => [
            EditorView.decorations.from(field, (value) => value.deco),
            EditorView.atomicRanges.of((view) => view.state.field(field).deco),
        ],
    });
    // ---- uploads in progress: shown at their insertion point, never written into the text ----
    const addPending = StateEffect.define();
    const removePending = StateEffect.define();
    const uploadField = StateField.define({
        create: () => ({ pending: [], inserted: [], selections: [] }),
        update(value, tr) {
            let { pending, inserted, selections } = value;
            if (tr.docChanged) {
                pending = pending.map((p) => ({ ...p, pos: tr.changes.mapPos(p.pos, -1) }));
                inserted = inserted.map((r) => ({ ...r, from: tr.changes.mapPos(r.from, 1), to: tr.changes.mapPos(r.to, -1) }));
                selections = selections.map((r) => {
                    const from = tr.changes.mapPos(r.from, 1);
                    return { ...r, from, to: Math.max(from, tr.changes.mapPos(r.to, -1)) };
                });
            }
            for (const effect of tr.effects) {
                if (effect.is(addPending)) {
                    pending = [...pending, effect.value.pending];
                    if (effect.value.selection)
                        selections = [...selections, effect.value.selection];
                }
                else if (effect.is(removePending)) {
                    const done = pending.find((p) => p.id === effect.value.id);
                    pending = pending.filter((p) => p.id !== effect.value.id);
                    const batch = effect.value.inserted?.batch;
                    if (effect.value.inserted)
                        inserted = [...inserted, effect.value.inserted];
                    // The batch's first image took the selection's place (or found it edited).
                    if (batch !== undefined)
                        selections = selections.filter((r) => r.batch !== batch);
                    // A batch's insertion order matters only while some of it is still uploading.
                    if (done && !pending.some((p) => p.batch === done.batch)) {
                        inserted = inserted.filter((r) => r.batch !== done.batch);
                        selections = selections.filter((r) => r.batch !== done.batch);
                    }
                }
            }
            return pending === value.pending && inserted === value.inserted && selections === value.selections
                ? value
                : { pending, inserted, selections };
        },
        provide: (field) => EditorView.decorations.from(field, (value) => Decoration.set(value.pending.map((p) => Decoration.widget({ widget: new UploadingWidget(p.id), side: 1 }).range(p.pos)), true)),
    });
    // ---- selection, resize and keyboard ----
    const refAt = (state, from) => state.field(imageField).refs.find((ref) => ref.from === from);
    const selectedRef = (state) => {
        const sel = state.selection.main;
        if (sel.empty)
            return undefined;
        return state.field(imageField).refs.find((ref) => ref.from === sel.from && ref.to === sel.to);
    };
    const widgetFor = (view, ref) => {
        for (const el of Array.from(view.contentDOM.querySelectorAll(".todo-notes-image[data-path]"))) {
            if (view.posAtDOM(el) === ref.from)
                return el;
        }
        return null;
    };
    const displayedWidth = (view, ref) => {
        const img = widgetFor(view, ref)?.querySelector("img");
        const measured = img ? Math.round(img.getBoundingClientRect().width) : 0;
        return ref.width ?? (measured > 0 ? measured : NOTE_IMAGE_DEFAULT_MAX_WIDTH);
    };
    // widthCeiling: the editor's content width, and the image's own width (images are not enlarged).
    // Widths are rendered widths, so the image's own width includes its border.
    const widthCeiling = (view, wrapper) => {
        const style = getComputedStyle(view.contentDOM);
        const content = view.contentDOM.clientWidth - parseFloat(style.paddingLeft || "0") - parseFloat(style.paddingRight || "0");
        const img = wrapper?.querySelector("img");
        const border = img ? img.offsetWidth - img.clientWidth : 0;
        const natural = Number(wrapper?.dataset.naturalWidth || 0);
        let ceiling = content > 0 ? content : Infinity;
        if (natural > 0)
            ceiling = Math.min(ceiling, Math.max(natural + border, NOTE_IMAGE_MIN_WIDTH));
        return Number.isFinite(ceiling) ? ceiling : NOTE_IMAGE_DEFAULT_MAX_WIDTH;
    };
    const selectionStatus = (view, ref) => text("todo.images.selectedStatus", "Image selected: {alt}. {width} px wide. Press + or - to resize, 0 to reset, Delete to remove, Escape to deselect.", { alt: ref.alt, width: displayedWidth(view, ref) });
    const selectImageAt = (view, wrapper) => {
        const ref = refAt(view.state, view.posAtDOM(wrapper));
        if (!ref)
            return;
        view.dispatch({ selection: EditorSelection.single(ref.from, ref.to), userEvent: "select.pointer" });
        view.focus();
    };
    // setWidth rewrites only the alt text's width suffix and keeps the image selected.
    const setWidth = (view, ref, width) => {
        const alt = formatImageAlt(ref.alt, width);
        const delta = alt.length - (ref.altTo - ref.altFrom);
        if (delta === 0 && view.state.sliceDoc(ref.altFrom, ref.altTo) === alt)
            return;
        view.dispatch({
            changes: { from: ref.altFrom, to: ref.altTo, insert: alt },
            selection: EditorSelection.single(ref.from, ref.to + delta),
            userEvent: "input.resize",
        });
        announce(text("todo.images.resizedStatus", "Image resized to {width} px.", { width: width ?? displayedWidth(view, { ...ref, width: null }) }));
    };
    const startResize = (view, wrapper, event) => {
        if (view.state.readOnly || event.button !== 0)
            return;
        event.preventDefault();
        event.stopPropagation();
        const ref = refAt(view.state, view.posAtDOM(wrapper));
        const img = wrapper.querySelector("img");
        if (!ref || !img)
            return;
        if (selectedRef(view.state)?.from !== ref.from)
            view.dispatch({ selection: EditorSelection.single(ref.from, ref.to) });
        const handle = event.currentTarget;
        handle.setPointerCapture?.(event.pointerId);
        const startX = event.clientX;
        const startY = event.clientY;
        const startBox = img.getBoundingClientRect();
        const startWidth = startBox.width || displayedWidth(view, ref);
        const startHeight = startBox.height;
        const ceiling = widthCeiling(view, wrapper);
        // Growing moves the handle's corner away from the opposite top corner: right and down, or
        // left and down in right-to-left text.
        const direction = getComputedStyle(view.contentDOM).direction !== "rtl" ? 1 : -1;
        // The pointer's movement projected onto that diagonal gives one scale factor for both sides,
        // so vertical, horizontal and diagonal drags all resize, always proportionally.
        const diagonal2 = startWidth * startWidth + startHeight * startHeight;
        const scaleFor = (e) => diagonal2 > 0 ? 1 + (direction * (e.clientX - startX) * startWidth + (e.clientY - startY) * startHeight) / diagonal2 : 1;
        const previousWidth = img.style.width;
        const previousSized = wrapper.classList.contains("todo-notes-image--sized");
        let width = Math.round(startWidth);
        let moved = false;
        wrapper.classList.add("is-resizing");
        const onMove = (e) => {
            if (e.pointerId !== event.pointerId)
                return;
            moved = moved || Math.hypot(e.clientX - startX, e.clientY - startY) > 2;
            width = clampImageWidth(startWidth * scaleFor(e), ceiling);
            wrapper.classList.add("todo-notes-image--sized");
            img.style.width = `${width}px`;
        };
        const finish = (commit) => (e) => {
            if (e.pointerId !== event.pointerId)
                return;
            handle.removeEventListener("pointermove", onMove);
            handle.removeEventListener("pointerup", onUp);
            handle.removeEventListener("pointercancel", onCancel);
            wrapper.classList.remove("is-resizing");
            const current = refAt(view.state, ref.from);
            if (commit && moved && current) {
                setWidth(view, current, width);
            }
            else {
                img.style.width = previousWidth;
                wrapper.classList.toggle("todo-notes-image--sized", previousSized);
            }
            view.focus();
        };
        const onUp = finish(true);
        const onCancel = finish(false);
        handle.addEventListener("pointermove", onMove);
        handle.addEventListener("pointerup", onUp);
        handle.addEventListener("pointercancel", onCancel);
    };
    const resizeBy = (factor) => (view) => {
        const ref = selectedRef(view.state);
        if (!ref || view.state.readOnly)
            return false;
        const current = displayedWidth(view, ref);
        const step = Math.max(KEYBOARD_RESIZE_MIN_STEP, Math.abs(current * factor - current));
        setWidth(view, ref, clampImageWidth(current + Math.sign(factor - 1) * step, widthCeiling(view, widgetFor(view, ref))));
        return true;
    };
    const imageKeymap = [
        { key: "+", run: resizeBy(KEYBOARD_RESIZE_FACTOR) },
        { key: "=", run: resizeBy(KEYBOARD_RESIZE_FACTOR) },
        { key: "-", run: resizeBy(1 / KEYBOARD_RESIZE_FACTOR) },
        {
            // Back to the default size. Handled (as a no-op) for an unsized image too, so the key
            // never replaces a selected image with "0".
            key: "0",
            run: (view) => {
                const ref = selectedRef(view.state);
                if (!ref || view.state.readOnly)
                    return false;
                if (ref.width != null)
                    setWidth(view, ref, null);
                return true;
            },
        },
        {
            key: "Escape",
            run: (view) => {
                const ref = selectedRef(view.state);
                if (!ref)
                    return false;
                view.dispatch({ selection: EditorSelection.cursor(ref.to), userEvent: "select" });
                return true;
            },
        },
        ...["Backspace", "Delete"].map((key) => ({
            key,
            run: (view) => {
                const ref = selectedRef(view.state);
                if (!ref || view.state.readOnly)
                    return false;
                view.dispatch({ changes: { from: ref.from, to: ref.to }, selection: EditorSelection.cursor(ref.from), userEvent: "delete.image" });
                announce(text("todo.images.removedStatus", "Image removed. Undo restores it."));
                return true;
            },
        })),
    ];
    // Marks the widget whose exact range is selected and announces it once.
    let shownSelection = "";
    const syncSelection = (view) => {
        const ref = selectedRef(view.state);
        for (const el of Array.from(view.contentDOM.querySelectorAll(".todo-notes-image[data-path]"))) {
            el.classList.toggle("is-selected", !!ref && view.posAtDOM(el) === ref.from);
        }
        view.dom.classList.toggle("has-selected-image", !!ref);
        const key = ref ? `${ref.from}:${ref.to}` : "";
        if (key !== shownSelection) {
            shownSelection = key;
            if (ref)
                announce(selectionStatus(view, ref));
        }
    };
    // ---- paste and drop go to the image uploader; other files are never inserted as text ----
    const hasFiles = (dt) => !!dt && Array.from(dt.types ?? []).includes("Files");
    const domHandlers = EditorView.domEventHandlers({
        paste(event, view) {
            if (!uploadHandlers || view.state.readOnly)
                return false;
            return uploadHandlers.handlePaste(event);
        },
        dragover(event, view) {
            if (!hasFiles(event.dataTransfer))
                return false;
            event.preventDefault();
            if (uploadHandlers && !view.state.readOnly)
                uploadHandlers.handleDragOver(event);
            return true;
        },
        dragleave() {
            uploadHandlers?.handleDragLeave();
            return false;
        },
        drop(event, view) {
            if (!hasFiles(event.dataTransfer))
                return false;
            event.preventDefault();
            if (!uploadHandlers || view.state.readOnly) {
                uploadHandlers?.handleDragLeave();
                return true;
            }
            const pos = view.posAtCoords({ x: event.clientX, y: event.clientY });
            if (pos != null)
                view.dispatch({ selection: EditorSelection.cursor(pos) });
            uploadHandlers.handleDrop(event);
            return true;
        },
    });
    const maxLengthFilter = EditorState.transactionFilter.of((tr) => tr.docChanged && tr.newDoc.length > opts.maxLength && tr.newDoc.length > tr.startState.doc.length ? [] : tr);
    const contentAttributes = { spellcheck: "true", autocapitalize: "sentences", "aria-multiline": "true" };
    if (opts.labelledBy)
        contentAttributes["aria-labelledby"] = opts.labelledBy;
    // Every edit since the note was loaded, composed: getValue applies them to the original text.
    const editsField = StateField.define({
        create: (state) => state.changes([]),
        update: (value, tr) => (tr.docChanged ? value.compose(tr.changes) : value),
    });
    const extensions = (readOnly) => [
        editsField,
        CM.history(),
        CM.keymap.of(imageKeymap),
        CM.keymap.of(CM.notesKeymap),
        EditorView.lineWrapping,
        EditorView.contentAttributes.of(contentAttributes),
        readOnlyCompartment.of([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
        imageField,
        uploadField,
        maxLengthFilter,
        domHandlers,
        EditorView.updateListener.of((update) => {
            if (update.selectionSet || update.docChanged || update.viewportChanged || update.focusChanged)
                syncSelection(update.view);
        }),
    ];
    let readOnly = false;
    const view = new EditorView({ parent: opts.parent, state: EditorState.create({ doc: "", extensions: extensions(false) }) });
    view.dom.classList.add("todo-notes-visual__editor");
    const lfText = () => view.state.doc.toString();
    // ---- the upload target: reservations live in uploadField, finished images become one edit ----
    const uploadTarget = {
        get element() {
            return view.dom;
        },
        isReadOnly: () => view.state.readOnly,
        begin(id, reservation) {
            // The text is not changed until an image arrives: a selection is replaced only then, so a
            // failed or cancelled upload leaves the note as it was and adds no undo step.
            const sel = view.state.selection.main;
            const uploads = view.state.field(uploadField);
            const selection = sel.empty || uploads.selections.some((r) => r.batch === reservation.batch)
                ? undefined
                : { batch: reservation.batch, from: sel.from, to: sel.to, text: view.state.sliceDoc(sel.from, sel.to) };
            const replaced = selection ? selection.to - selection.from : 0;
            if (view.state.doc.length - replaced + reservation.length > opts.maxLength)
                return false;
            view.dispatch({
                effects: addPending.of({
                    pending: { id, batch: reservation.batch, index: reservation.index, pos: sel.from },
                    selection,
                }),
            });
            return true;
        },
        finish(id, markdown) {
            const uploads = view.state.field(uploadField);
            const entry = uploads.pending.find((p) => p.id === id);
            // Gone with a reloaded note: nothing to insert.
            if (!entry)
                return true;
            if (markdown === null) {
                view.dispatch({ effects: removePending.of({ id }) });
                return true;
            }
            const doc = view.state.doc;
            // Keep a batch in paste order: insert after the last earlier sibling already inserted.
            let pos = entry.pos;
            for (const r of uploads.inserted) {
                if (r.batch === entry.batch && r.index < entry.index && r.to > pos)
                    pos = r.to;
            }
            // The batch's first image replaces the text selected at paste time, unless that text was
            // edited or removed meanwhile: then the image is only inserted and the user's edit stays.
            let to = pos;
            const selection = uploads.selections.find((r) => r.batch === entry.batch);
            if (selection && selection.to > selection.from && doc.sliceString(selection.from, selection.to) === selection.text) {
                pos = selection.from;
                to = selection.to;
            }
            // The image goes on its own line: break before it unless at a line start, and after it
            // unless a line break already follows.
            const before = pos > 0 ? doc.sliceString(pos - 1, pos) : "\n";
            const after = doc.sliceString(to, to + 1);
            const insert = `${before === "\n" ? "" : "\n"}${markdown}${after === "\n" ? "" : "\n"}`;
            if (doc.length - (to - pos) + insert.length > opts.maxLength) {
                view.dispatch({ effects: removePending.of({ id }) });
                return false;
            }
            const sel = view.state.selection.main;
            const end = pos + insert.length;
            // The caret moves past the image when it was where the image goes, or on the replaced text.
            const caretThere = sel.from === pos && sel.to === to;
            view.dispatch({
                changes: { from: pos, to, insert },
                selection: caretThere ? EditorSelection.cursor(end) : undefined,
                effects: removePending.of({ id, inserted: { batch: entry.batch, index: entry.index, from: pos, to: end } }),
                userEvent: "input.image",
                // Its own undo step, never merged with typing just before or after it.
                annotations: CM.isolateHistory?.of("full"),
            });
            return true;
        },
        setDropTarget(active) {
            view.dom.classList.toggle("todo-notes-visual__editor--drop-target", active);
        },
    };
    const load = (value) => {
        original = value;
        originalLF = value.replace(LINE_BREAK_RE, "\n");
        shownSelection = "";
        view.setState(EditorState.create({ doc: originalLF, extensions: extensions(readOnly) }));
    };
    return {
        get dom() {
            return view.dom;
        },
        getValue() {
            const current = lfText();
            if (current === originalLF)
                return original;
            return applyEditsPreservingLineBreaks(original, view.state.field(editsField));
        },
        setValue(value) {
            load(value);
        },
        setReadOnly(next) {
            readOnly = next;
            view.dispatch({
                effects: readOnlyCompartment.reconfigure([EditorState.readOnly.of(next), EditorView.editable.of(!next)]),
            });
            view.dom.classList.toggle("is-readonly", next);
        },
        focus() {
            view.focus();
        },
        clear() {
            load("");
            pool.releaseAll();
        },
        destroy() {
            pool.releaseAll();
            view.destroy();
        },
        uploadTarget,
        setUploadHandlers(handlers) {
            uploadHandlers = handlers;
        },
    };
}
