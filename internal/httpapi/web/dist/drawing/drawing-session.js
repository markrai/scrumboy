import { cloneDrawing, MAX_SUBMITTED_DRAWING_BYTES } from "./drawing-types.js";
import { drawingByteSize, sanitizeDrawingDocument, sanitizeNotesInk } from "./drawing-validate.js";
export class DrawingBudgetError extends Error {
    constructor(message) {
        super(message);
        this.name = "DrawingBudgetError";
    }
}
export class DrawingSession {
    constructor() {
        this.hydrated = new Map();
        this.serverIds = new Set();
        this.drafts = new Map();
        this.listeners = new Set();
        this.controller = null;
        this.loading = null;
        this.rev = 0;
        this.failed = false;
        this.serverNotesInk = null;
    }
    get notesInk() {
        return this.serverNotesInk ? cloneDrawing(this.serverNotesInk) : null;
    }
    get hydrationFailed() {
        return this.failed;
    }
    get revision() {
        return this.rev;
    }
    get isLoading() {
        return this.loading !== null;
    }
    whenReady() {
        return this.loading ?? Promise.resolve();
    }
    get(id) {
        return this.drafts.get(id) ?? this.hydrated.get(id);
    }
    has(id) {
        return this.drafts.has(id) || this.hydrated.has(id) || this.serverIds.has(id);
    }
    isDraft(id) {
        return this.drafts.has(id);
    }
    setDraft(id, doc) {
        this.drafts.set(id, cloneDrawing(doc));
        this.rev += 1;
        this.emit(id);
    }
    subscribe(listener) {
        this.listeners.add(listener);
        return () => this.listeners.delete(listener);
    }
    hydrate(url, fetcher) {
        this.controller?.abort();
        const controller = new AbortController();
        this.controller = controller;
        this.failed = false;
        const task = fetcher(url, controller.signal)
            .then((res) => {
            if (controller.signal.aborted)
                return;
            const map = res?.drawings ?? {};
            this.serverNotesInk = sanitizeNotesInk(res?.notesInk ?? null);
            for (const [id, raw] of Object.entries(map)) {
                this.serverIds.add(id);
                const doc = sanitizeDrawingDocument(raw);
                if (doc)
                    this.hydrated.set(id, doc);
            }
        })
            .catch(() => {
            if (!controller.signal.aborted)
                this.failed = true;
        })
            .finally(() => {
            if (this.controller === controller) {
                this.controller = null;
                this.loading = null;
                this.emit(null);
            }
        });
        this.loading = task;
        return task;
    }
    savePayload(referenced) {
        const out = {};
        let total = 0;
        let count = 0;
        for (const id of referenced) {
            const doc = this.drafts.get(id);
            if (!doc)
                continue;
            total += drawingByteSize(doc);
            if (total > MAX_SUBMITTED_DRAWING_BYTES) {
                throw new DrawingBudgetError("Changed drawings are too large to save at once. Save fewer drawing changes at a time.");
            }
            out[id] = doc;
            count += 1;
        }
        return count > 0 ? out : null;
    }
    clear() {
        this.controller?.abort();
        this.controller = null;
        this.loading = null;
        this.hydrated.clear();
        this.serverIds.clear();
        this.serverNotesInk = null;
        this.drafts.clear();
        this.rev = 0;
        this.failed = false;
        this.emit(null);
    }
    emit(id) {
        for (const l of Array.from(this.listeners)) {
            try {
                l(id);
            }
            catch {
                continue;
            }
        }
    }
}
const CLIPBOARD_LIMIT = 50;
const clipboardDocs = new Map();
export function rememberClipboardDrawing(id, doc) {
    clipboardDocs.delete(id);
    clipboardDocs.set(id, cloneDrawing(doc));
    while (clipboardDocs.size > CLIPBOARD_LIMIT) {
        const oldest = clipboardDocs.keys().next().value;
        clipboardDocs.delete(oldest);
    }
}
export function clipboardDrawing(id) {
    const doc = clipboardDocs.get(id);
    return doc ? cloneDrawing(doc) : undefined;
}
export function __resetClipboardDrawingsForTest() {
    clipboardDocs.clear();
}
