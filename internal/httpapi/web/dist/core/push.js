/**
 * Web Push subscription (VAPID). Enable only from explicit user action (e.g. Settings toggle).
 */
import { apiFetch } from '../api.js';
import { getAppVersion } from '../utils.js';
const LS_PUSH = 'scrumboy_push_enabled';
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
