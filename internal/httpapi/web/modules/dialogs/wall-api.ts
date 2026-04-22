// Pure network surface for the Scrumbaby/Wall feature.
//
// Every function here is slug-keyed and returns exactly what the server
// returns. Orchestration, toasts, 409 refetch, and DOM updates live in
// `wall.ts`; this module only knows how to talk to the HTTP API.
//
// Keeping this tiny and mock-friendly is what gives us URL/body regression
// coverage in `wall-api.test.ts`.

import { apiFetch } from "../api.js";
import type { WallDocument, WallEdge, WallNote } from "./wall-rendering.js";

export type NoteCreateInput = {
  x: number;
  y: number;
  width: number;
  height: number;
  color: string;
  text: string;
};

export type NotePatchInput = {
  /** Optimistic concurrency guard. */
  ifVersion: number;
} & Partial<Pick<WallNote, "x" | "y" | "width" | "height" | "color" | "text">>;

export type TransientInput = {
  noteId: string;
  x: number;
  y: number;
};

function wallBase(slug: string): string {
  return `/api/board/${encodeURIComponent(slug)}/wall`;
}

export function fetchWall(slug: string): Promise<WallDocument> {
  return apiFetch<WallDocument>(wallBase(slug));
}

export function createNote(slug: string, body: NoteCreateInput): Promise<WallNote> {
  return apiFetch<WallNote>(`${wallBase(slug)}/notes`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function patchNoteRemote(slug: string, id: string, patch: NotePatchInput): Promise<WallNote> {
  return apiFetch<WallNote>(`${wallBase(slug)}/notes/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(patch),
  });
}

export function deleteNoteRemote(slug: string, id: string): Promise<void> {
  return apiFetch<void>(`${wallBase(slug)}/notes/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function createEdgeRemote(slug: string, from: string, to: string): Promise<WallEdge> {
  return apiFetch<WallEdge>(`${wallBase(slug)}/edges`, {
    method: "POST",
    body: JSON.stringify({ from, to }),
  });
}

export function deleteEdgeRemote(slug: string, id: string): Promise<void> {
  return apiFetch<void>(`${wallBase(slug)}/edges/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

/**
 * Post a transient (non-durable) drag update.
 *
 * Contract: **never rejects.** Callers may `void postTransient(...)` freely.
 * Failures are counted and logged at most once every THROTTLE_MS to keep
 * transient noise out of the main console during a flaky network burst.
 *
 * Observability:
 *   - `__getTransientFailureCount()` returns the lifetime failure counter
 *     (useful for tests; not intended for production reads).
 *   - `__getTransientPostsSent()` returns the lifetime *success* counter
 *     (Phase 0 debug baseline; used to measure transient pressure).
 *   - `window.__scrumboyWallDebug === true` elevates the log level to
 *     `console.warn` so operators can surface otherwise-silent failures.
 */
const TRANSIENT_LOG_THROTTLE_MS = 3000;
const transientFailureState = { count: 0, lastLoggedAt: 0 };
const transientSuccessState = { count: 0 };

export async function postTransient(slug: string, body: TransientInput): Promise<void> {
  try {
    await apiFetch(`${wallBase(slug)}/transient`, {
      method: "POST",
      body: JSON.stringify(body),
    });
    transientSuccessState.count += 1;
  } catch (err) {
    transientFailureState.count += 1;
    const now = performance.now();
    const firstFailure = transientFailureState.lastLoggedAt === 0;
    if (firstFailure || now - transientFailureState.lastLoggedAt > TRANSIENT_LOG_THROTTLE_MS) {
      transientFailureState.lastLoggedAt = now;
      const debug = (globalThis as any).__scrumboyWallDebug === true;
      const log = debug ? console.warn : console.debug;
      log("wall transient post failed", { count: transientFailureState.count, err });
    }
  }
}

/** Test helper: read the lifetime transient failure counter. */
export function __getTransientFailureCount(): number {
  return transientFailureState.count;
}

/** Test helper: read the lifetime successful transient-post counter. */
export function __getTransientPostsSent(): number {
  return transientSuccessState.count;
}

/** Test helper: reset the internal counters/log state between test cases. */
export function __resetTransientFailureState(): void {
  transientFailureState.count = 0;
  transientFailureState.lastLoggedAt = 0;
  transientSuccessState.count = 0;
}
