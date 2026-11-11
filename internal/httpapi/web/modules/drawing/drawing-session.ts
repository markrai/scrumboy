import { cloneDrawing, MAX_SUBMITTED_DRAWING_BYTES, type DrawingDocument } from "./drawing-types.js";
import { drawingByteSize, sanitizeDrawingDocument, sanitizeNotesInk } from "./drawing-validate.js";

export type DrawingFetcher = (url: string, signal: AbortSignal) => Promise<unknown>;

export class DrawingBudgetError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DrawingBudgetError";
  }
}

export class DrawingSession {
  private hydrated = new Map<string, DrawingDocument>();
  private serverIds = new Set<string>();
  private drafts = new Map<string, DrawingDocument>();
  private listeners = new Set<(id: string | null) => void>();
  private controller: AbortController | null = null;
  private loading: Promise<void> | null = null;
  private rev = 0;
  private failed = false;
  private serverNotesInk: DrawingDocument | null = null;

  get notesInk(): DrawingDocument | null {
    return this.serverNotesInk ? cloneDrawing(this.serverNotesInk) : null;
  }

  get hydrationFailed(): boolean {
    return this.failed;
  }

  get revision(): number {
    return this.rev;
  }

  get isLoading(): boolean {
    return this.loading !== null;
  }

  whenReady(): Promise<void> {
    return this.loading ?? Promise.resolve();
  }

  get(id: string): DrawingDocument | undefined {
    return this.drafts.get(id) ?? this.hydrated.get(id);
  }

  has(id: string): boolean {
    return this.drafts.has(id) || this.hydrated.has(id) || this.serverIds.has(id);
  }

  isDraft(id: string): boolean {
    return this.drafts.has(id);
  }

  setDraft(id: string, doc: DrawingDocument): void {
    this.drafts.set(id, cloneDrawing(doc));
    this.rev += 1;
    this.emit(id);
  }

  subscribe(listener: (id: string | null) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  hydrate(url: string, fetcher: DrawingFetcher): Promise<void> {
    this.controller?.abort();
    const controller = new AbortController();
    this.controller = controller;
    this.failed = false;
    const task = fetcher(url, controller.signal)
      .then((res) => {
        if (controller.signal.aborted) return;
        const map = (res as { drawings?: Record<string, unknown> } | null)?.drawings ?? {};
        this.serverNotesInk = sanitizeNotesInk((res as { notesInk?: unknown } | null)?.notesInk ?? null);
        for (const [id, raw] of Object.entries(map)) {
          this.serverIds.add(id);
          const doc = sanitizeDrawingDocument(raw);
          if (doc) this.hydrated.set(id, doc);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) this.failed = true;
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

  savePayload(referenced: readonly string[]): Record<string, DrawingDocument> | null {
    const out: Record<string, DrawingDocument> = {};
    let total = 0;
    let count = 0;
    for (const id of referenced) {
      const doc = this.drafts.get(id);
      if (!doc) continue;
      total += drawingByteSize(doc);
      if (total > MAX_SUBMITTED_DRAWING_BYTES) {
        throw new DrawingBudgetError("Changed drawings are too large to save at once. Save fewer drawing changes at a time.");
      }
      out[id] = doc;
      count += 1;
    }
    return count > 0 ? out : null;
  }

  clear(): void {
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

  private emit(id: string | null): void {
    for (const l of Array.from(this.listeners)) {
      try {
        l(id);
      } catch {
        continue;
      }
    }
  }
}

const CLIPBOARD_LIMIT = 50;
const clipboardDocs = new Map<string, DrawingDocument>();

export function rememberClipboardDrawing(id: string, doc: DrawingDocument): void {
  clipboardDocs.delete(id);
  clipboardDocs.set(id, cloneDrawing(doc));
  while (clipboardDocs.size > CLIPBOARD_LIMIT) {
    const oldest = clipboardDocs.keys().next().value as string;
    clipboardDocs.delete(oldest);
  }
}

export function clipboardDrawing(id: string): DrawingDocument | undefined {
  const doc = clipboardDocs.get(id);
  return doc ? cloneDrawing(doc) : undefined;
}

export function __resetClipboardDrawingsForTest(): void {
  clipboardDocs.clear();
}
