/**
 * Web Push subscription (VAPID). After login, auto-subscribe when the server exposes a VAPID public key
 * (both keys configured). Optional override: Settings -> turn Web Push off or back on for this browser.
 */
import { apiFetch } from '../api.js';
import { getAppVersion } from '../utils.js';
const LS_PUSH = 'scrumboy_push_enabled';
/** Per signed-in user: durable auto-subscribe outcomes only (`done` | `denied`). See maybeAutoSubscribePushAfterLogin. */
const LS_PUSH_AUTOSUB_USER_PREFIX = 'scrumboy_push_autosub_v1_u';
const AUTOSUB_STATE_DONE = 'done';
const AUTOSUB_STATE_DENIED = 'denied';
function autosubKeyForUser(userId) {
    return `${LS_PUSH_AUTOSUB_USER_PREFIX}${userId}`;
}
function getAutosubState(userId) {
    try {
        return localStorage.getItem(autosubKeyForUser(userId));
    }
    catch {
        return null;
    }
}
function setAutosubState(userId, state) {
    try {
        localStorage.setItem(autosubKeyForUser(userId), state);
    }
    catch {
        /* ignore */
    }
}
function urlBase64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    const rawData = atob(base64);
    const outputArray = new Uint8Array(rawData.length);
    for (let i = 0; i < rawData.length; ++i) {
        outputArray[i] = rawData.charCodeAt(i);
    }
    return outputArray;
}
/** Ensure SW is registered (e.g. localhost may skip auto-register until user enables push). */
async function ensureServiceWorkerRegistration() {
    if (!('serviceWorker' in navigator))
        return null;
    let reg = await navigator.serviceWorker.getRegistration();
    if (reg)
        return reg;
    const v = getAppVersion() || '0';
    try {
        return await navigator.serviceWorker.register('/sw.js?v=' + encodeURIComponent(v));
    }
    catch {
        return null;
    }
}
function pushDebug() {
    try {
        return typeof localStorage !== 'undefined' && localStorage.getItem('scrumboy_debug_push') === '1';
    }
    catch {
        return false;
    }
}
/**
 * If GET /api/push/vapid-public-key returns a public key (VAPID configured), attempt automatic
 * subscription for this user (may prompt for notification permission). Scoped per `userId` in
 * localStorage; only marks a durable outcome for success, already subscribed, or explicit permission
 * denied - not for transient failures or dismissed prompts (`default`), so a later visit can retry.
 */
export async function maybeAutoSubscribePushAfterLogin(userId) {
    if (!Number.isFinite(userId) || userId <= 0) {
        return;
    }
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
        return;
    }
    const prior = getAutosubState(userId);
    if (prior === AUTOSUB_STATE_DONE || prior === AUTOSUB_STATE_DENIED) {
        return;
    }
    try {
        const keyResp = await fetch('/api/push/vapid-public-key', { credentials: 'same-origin' });
        if (!keyResp.ok) {
            return;
        }
        const j = (await keyResp.json());
        if (!j.publicKey) {
            return;
        }
    }
    catch {
        return;
    }
    try {
        if (await isPushSubscribed()) {
            setAutosubState(userId, AUTOSUB_STATE_DONE);
            return;
        }
    }
    catch {
        /* ignore - treat as transient, do not persist */
    }
    if (typeof Notification !== 'undefined' && Notification.permission === 'denied') {
        setAutosubState(userId, AUTOSUB_STATE_DENIED);
        return;
    }
    let ok = false;
    try {
        ok = await subscribeToPush();
    }
    catch {
        /* network / server errors during subscribe POST - retry on a later load */
        return;
    }
    if (ok) {
        setAutosubState(userId, AUTOSUB_STATE_DONE);
        return;
    }
    if (typeof Notification !== 'undefined') {
        if (Notification.permission === 'denied') {
            setAutosubState(userId, AUTOSUB_STATE_DENIED);
            return;
        }
        if (Notification.permission === 'default') {
            /* dismissed prompt or still undecided - allow retry on a future page load */
            return;
        }
    }
    /* granted but subscribe failed (e.g. SW registration, push subscribe) - retry later */
}
/** True if this browser has an active push subscription for our SW. */
export async function isPushSubscribed() {
    try {
        if (!('serviceWorker' in navigator) || !('PushManager' in window))
            return false;
        const reg = await ensureServiceWorkerRegistration();
        if (!reg)
            return false;
        const sub = await reg.pushManager.getSubscription();
        return !!sub;
    }
    catch {
        return false;
    }
}
/**
 * Subscribe to Web Push (user gesture should precede this). Returns false if unsupported or server misconfigured.
 */
export async function subscribeToPush() {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
        return false;
    }
    if (typeof Notification !== 'undefined') {
        if (Notification.permission === 'denied') {
            return false;
        }
        if (Notification.permission === 'default') {
            const p = await Notification.requestPermission();
            if (p !== 'granted') {
                return false;
            }
        }
    }
    const keyResp = await fetch('/api/push/vapid-public-key', { credentials: 'same-origin' });
    if (!keyResp.ok) {
        if (pushDebug())
            console.warn('[push] vapid key unavailable', keyResp.status);
        return false;
    }
    const j = (await keyResp.json());
    const publicKey = j.publicKey;
    if (!publicKey) {
        return false;
    }
    const reg = await ensureServiceWorkerRegistration();
    if (!reg) {
        return false;
    }
    const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(publicKey),
    });
    const json = sub.toJSON();
    if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth) {
        return false;
    }
    await apiFetch('/api/push/subscribe', {
        method: 'POST',
        body: JSON.stringify({
            endpoint: json.endpoint,
            keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
        }),
    });
    try {
        localStorage.setItem(LS_PUSH, '1');
    }
    catch {
        /* ignore */
    }
    if (pushDebug())
        console.log('[push] subscribed');
    return true;
}
/**
 * Remove this browser's push subscription only (one endpoint). Server DELETE is best-effort so
 * local PushManager.unsubscribe still runs if the session is already gone (e.g. after logout).
 */
export async function unsubscribeFromPush() {
    try {
        if (!('serviceWorker' in navigator)) {
            try {
                localStorage.removeItem(LS_PUSH);
            }
            catch {
                /* ignore */
            }
            return;
        }
        const reg = await ensureServiceWorkerRegistration();
        if (!reg) {
            try {
                localStorage.removeItem(LS_PUSH);
            }
            catch {
                /* ignore */
            }
            return;
        }
        const sub = await reg.pushManager.getSubscription();
        if (!sub) {
            try {
                localStorage.removeItem(LS_PUSH);
            }
            catch {
                /* ignore */
            }
            return;
        }
        const endpoint = sub.endpoint;
        try {
            await apiFetch('/api/push/unsubscribe', {
                method: 'DELETE',
                body: JSON.stringify({ endpoint }),
            });
        }
        catch {
            /* still try to unsubscribe locally */
        }
        await sub.unsubscribe();
        try {
            localStorage.removeItem(LS_PUSH);
        }
        catch {
            /* ignore */
        }
        if (pushDebug())
            console.log('[push] unsubscribed');
    }
    catch {
        try {
            localStorage.removeItem(LS_PUSH);
        }
        catch {
            /* ignore */
        }
    }
}
