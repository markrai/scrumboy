// Pure network surface for the Scrumbaby/Wall feature.
//
// Every function here is slug-keyed and returns exactly what the server
// returns. Orchestration, toasts, 409 refetch, and DOM updates live in
// `wall.ts`; this module only knows how to talk to the HTTP API.
//
// Keeping this tiny and mock-friendly is what gives us URL/body regression
// coverage in `wall-api.test.ts`.
import { apiFetch } from "../api.js";
function wallBase(slug) {
    return `/api/board/${encodeURIComponent(slug)}/wall`;
}
export function fetchWall(slug) {
    return apiFetch(wallBase(slug));
}
export function createNote(slug, body) {
    return apiFetch(`${wallBase(slug)}/notes`, {
        method: "POST",
        body: JSON.stringify(body),
    });
}
export function patchNoteRemote(slug, id, patch) {
    return apiFetch(`${wallBase(slug)}/notes/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: JSON.stringify(patch),
    });
}
export function deleteNoteRemote(slug, id) {
    return apiFetch(`${wallBase(slug)}/notes/${encodeURIComponent(id)}`, {
        method: "DELETE",
    });
}
export function createEdgeRemote(slug, from, to) {
    return apiFetch(`${wallBase(slug)}/edges`, {
        method: "POST",
        body: JSON.stringify({ from, to }),
    });
}
export function deleteEdgeRemote(slug, id) {
    return apiFetch(`${wallBase(slug)}/edges/${encodeURIComponent(id)}`, {
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
export async function postTransient(slug, body) {
    try {
        await apiFetch(`${wallBase(slug)}/transient`, {
            method: "POST",
            body: JSON.stringify(body),
        });
        transientSuccessState.count += 1;
    }
    catch (err) {
        transientFailureState.count += 1;
        const now = performance.now();
        const firstFailure = transientFailureState.lastLoggedAt === 0;
        if (firstFailure || now - transientFailureState.lastLoggedAt > TRANSIENT_LOG_THROTTLE_MS) {
            transientFailureState.lastLoggedAt = now;
            const debug = globalThis.__scrumboyWallDebug === true;
            const log = debug ? console.warn : console.debug;
            log("wall transient post failed", { count: transientFailureState.count, err });
        }
    }
}
/** Test helper: read the lifetime transient failure counter. */
export function __getTransientFailureCount() {
    return transientFailureState.count;
}
/** Test helper: read the lifetime successful transient-post counter. */
export function __getTransientPostsSent() {
    return transientSuccessState.count;
}
/** Test helper: reset the internal counters/log state between test cases. */
export function __resetTransientFailureState() {
    transientFailureState.count = 0;
    transientFailureState.lastLoggedAt = 0;
    transientSuccessState.count = 0;
}
