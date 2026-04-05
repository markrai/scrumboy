/**
 * Managed EventSource with explicit recycle, generation guards, debounced restart,
 * stale watchdog (data-line pings), and bounded exponential backoff on errors.
 * Server tick interval must match internal/httpapi/sse.go heartbeatInterval (25s).
 */
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
export class SseConnectionManager {
    constructor(url, handlers) {
        this.es = null;
        /** Incremented when closing or starting a new connection; handlers capture myGen at creation. */
        this.generation = 0;
        this.restartDebounceTimer = null;
        this.staleTimer = null;
        this.backoffTimer = null;
        this.consecutiveErrors = 0;
        this.url = url;
        this.handlers = handlers;
    }
    label() {
        return this.handlers.label ?? this.url;
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
        const es = new EventSource(this.url);
        this.es = es;
        es.onopen = () => {
            if (myGen !== this.generation)
                return;
            dbg(this.label(), "onopen gen=", myGen);
            this.consecutiveErrors = 0;
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
            const delay = Math.min(MAX_BACKOFF_MS, INITIAL_BACKOFF_MS * Math.pow(2, Math.min(this.consecutiveErrors - 1, 8)));
            dbg(this.label(), "backoff ms=", delay, "scheduleAt=", scheduleAt);
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
        this.clearBackoffTimer();
        if (this.es) {
            this.es.close();
            this.es = null;
        }
        this.generation++;
    }
    armStaleTimer(myGen) {
        this.clearStaleTimer();
        this.staleTimer = setTimeout(() => {
            if (myGen !== this.generation)
                return;
            dbg(this.label(), "stale watchdog gen=", myGen);
            this.restartRequested("stale");
        }, SSE_STALE_AFTER_MS);
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
