/**
 * Managed EventSource with explicit recycle, generation guards, debounced restart,
 * stale watchdog (data-line pings), and bounded exponential backoff on errors.
 * Server tick interval must match internal/httpapi/sse.go heartbeatInterval (25s).
 *
 * The default policy is the authenticated/temporary-board contract. Streams
 * with a different heartbeat contract (the public board stream sends
 * comment-only heartbeats that never reach onmessage) pass an explicit policy.
 */
import { getAppRuntime } from '../platform/runtime.js';
/** Must match server heartbeatInterval (25s). */
export const SSE_SERVER_TICK_MS = 25000;
/** Three missed server ticks → force reconnect. */
export const SSE_STALE_AFTER_MS = 3 * SSE_SERVER_TICK_MS;
const RESTART_DEBOUNCE_MS = 400;
const INITIAL_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 60000;
export function isSseDebugEnabled() {
    try {
        return typeof localStorage !== "undefined" && localStorage.getItem("scrumboy_debug_realtime") === "1";
    }
    catch {
        return false;
    }
}
function dbg(label, ...args) {
    if (isSseDebugEnabled()) {
        console.log(`[sse:${label}]`, ...args);
    }
}
const DEFAULT_POLICY = {
    staleAfterMs: SSE_STALE_AFTER_MS,
    initialBackoffMs: INITIAL_BACKOFF_MS,
    maxBackoffMs: MAX_BACKOFF_MS,
    jitterRatio: 0,
    stableAfterMs: null,
};
export class SseConnectionManager {
    constructor(path, handlers, policy = {}) {
        this.es = null;
        /** Incremented when closing or starting a new connection; handlers capture myGen at creation. */
        this.generation = 0;
        this.restartDebounceTimer = null;
        this.staleTimer = null;
        this.backoffTimer = null;
        this.stableTimer = null;
        this.consecutiveErrors = 0;
        this.path = path;
        this.handlers = handlers;
        this.policy = { ...DEFAULT_POLICY, ...policy };
    }
    label() {
        return this.handlers.label ?? this.path;
    }
    /** Start or recycle the connection (closes any existing socket first). */
    open() {
        this.clearBackoffTimer();
        if (this.es) {
            dbg(this.label(), "close before open");
            this.es.close();
            this.es = null;
        }
        this.generation++;
        const myGen = this.generation;
        dbg(this.label(), "open gen=", myGen);
        const es = getAppRuntime().transport().openEventStream(this.path);
        this.es = es;
        es.onopen = () => {
            if (myGen !== this.generation)
                return;
            dbg(this.label(), "onopen gen=", myGen);
            if (this.policy.stableAfterMs === null) {
                this.consecutiveErrors = 0;
            }
            else {
                this.armStableTimer(myGen);
            }
            this.armStaleTimer(myGen);
            this.handlers.onOpen?.();
        };
        es.onmessage = (ev) => {
            if (myGen !== this.generation)
                return;
            let parsed = null;
            try {
                parsed = JSON.parse(ev.data);
            }
            catch {
                this.resetStaleTimer(myGen);
                this.handlers.onMessage(ev);
                return;
            }
            if (parsed?.type === "ping") {
                this.resetStaleTimer(myGen);
                return;
            }
            this.resetStaleTimer(myGen);
            this.handlers.onMessage(ev);
        };
        es.onerror = () => {
            if (myGen !== this.generation)
                return;
            this.consecutiveErrors++;
            dbg(this.label(), "onerror gen=", myGen, "count=", this.consecutiveErrors);
            this.clearStaleTimer();
            this.clearStableTimer();
            try {
                es.close();
            }
            catch {
                /* ignore */
            }
            if (this.es === es) {
                this.es = null;
            }
            // Bump generation so stale callbacks from this socket never match; then backoff reconnect.
            this.generation++;
            const scheduleAt = this.generation;
            const baseDelay = Math.min(this.policy.maxBackoffMs, this.policy.initialBackoffMs * Math.pow(2, Math.min(this.consecutiveErrors - 1, 8)));
            const delay = this.policy.jitterRatio > 0
                ? Math.round(baseDelay * (1 + Math.random() * this.policy.jitterRatio))
                : baseDelay;
            dbg(this.label(), "backoff ms=", delay, "scheduleAt=", scheduleAt);
            this.handlers.onTransportError?.();
            if (scheduleAt !== this.generation)
                return; // stopped by the error handler
            this.clearBackoffTimer();
            this.backoffTimer = setTimeout(() => {
                this.backoffTimer = null;
                if (scheduleAt !== this.generation)
                    return;
                this.open();
            }, delay);
        };
    }
    /** Debounced teardown + open. Only path that should schedule reconnect bursts. */
    restartRequested(reason) {
        dbg(this.label(), "restartRequested", reason);
        if (this.restartDebounceTimer !== null) {
            clearTimeout(this.restartDebounceTimer);
        }
        this.restartDebounceTimer = setTimeout(() => {
            this.restartDebounceTimer = null;
            this.clearBackoffTimer();
            this.consecutiveErrors = 0;
            this.open();
        }, RESTART_DEBOUNCE_MS);
    }
    stop() {
        dbg(this.label(), "stop");
        if (this.restartDebounceTimer !== null) {
            clearTimeout(this.restartDebounceTimer);
            this.restartDebounceTimer = null;
        }
        this.clearStaleTimer();
        this.clearStableTimer();
        this.clearBackoffTimer();
        if (this.es) {
            this.es.close();
            this.es = null;
        }
        this.generation++;
    }
    armStaleTimer(myGen) {
        this.clearStaleTimer();
        const staleAfterMs = this.policy.staleAfterMs;
        if (staleAfterMs === null)
            return;
        this.staleTimer = setTimeout(() => {
            if (myGen !== this.generation)
                return;
            dbg(this.label(), "stale watchdog gen=", myGen);
            this.restartRequested("stale");
        }, staleAfterMs);
    }
    armStableTimer(myGen) {
        this.clearStableTimer();
        const stableAfterMs = this.policy.stableAfterMs;
        if (stableAfterMs === null)
            return;
        this.stableTimer = setTimeout(() => {
            this.stableTimer = null;
            if (myGen !== this.generation)
                return;
            this.consecutiveErrors = 0;
        }, stableAfterMs);
    }
    clearStableTimer() {
        if (this.stableTimer !== null) {
            clearTimeout(this.stableTimer);
            this.stableTimer = null;
        }
    }
    resetStaleTimer(myGen) {
        if (myGen !== this.generation)
            return;
        this.clearStaleTimer();
        this.armStaleTimer(myGen);
    }
    clearStaleTimer() {
        if (this.staleTimer !== null) {
            clearTimeout(this.staleTimer);
            this.staleTimer = null;
        }
    }
    clearBackoffTimer() {
        if (this.backoffTimer !== null) {
            clearTimeout(this.backoffTimer);
            this.backoffTimer = null;
        }
    }
}
