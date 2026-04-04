/**
 * Client-only unread counter for assignment notifications (V1).
 * Future: server persistence, per-event read state, inbox.
 */
import { emit, on } from '../events.js';
const STORAGE_PREFIX = 'scrumboy_unread_v1_';
let unreadCount = 0;
let currentUserId = null;
let badgeInitialized = false;
function storageKey(userId) {
    return `${STORAGE_PREFIX}${userId}`;
}
function readStoredCount(userId) {
    try {
        const raw = localStorage.getItem(storageKey(userId));
        if (raw == null)
            return 0;
        const n = parseInt(raw, 10);
        return Number.isFinite(n) && n >= 0 ? n : 0;
    }
    catch {
        return 0;
    }
}
function persistCount() {
    if (currentUserId == null)
        return;
    try {
        localStorage.setItem(storageKey(currentUserId), String(unreadCount));
    }
    catch {
        /* ignore */
    }
}
function emitUpdated() {
    emit('notifications:updated', unreadCount);
}
/** Call when the logged-in user id is known or changes. Restores count from localStorage. */
export function hydrateNotificationsForUser(userId) {
    currentUserId = userId;
    if (userId == null) {
        unreadCount = 0;
        emitUpdated();
        return;
    }
    unreadCount = readStoredCount(userId);
    emitUpdated();
}
export function getUnreadCount() {
    return unreadCount;
}
export function incrementUnread() {
    unreadCount += 1;
    emitUpdated();
    persistCount();
}
export function clearUnread() {
    if (unreadCount === 0)
        return;
    unreadCount = 0;
    emitUpdated();
    if (currentUserId != null) {
        try {
            localStorage.removeItem(storageKey(currentUserId));
        }
        catch {
            /* ignore */
        }
    }
}
function assignmentHoverText(count) {
    if (count === 1)
        return '1 todo has been assigned to you';
    return `${count} todos have been assigned to you`;
}
function renderBadgeEl(el, count) {
    if (count <= 0) {
        el.style.display = 'none';
        el.textContent = '';
        el.removeAttribute('title');
        el.setAttribute('aria-hidden', 'true');
        return;
    }
    el.style.display = 'flex';
    el.textContent = String(count > 99 ? '99+' : count);
    el.setAttribute('aria-hidden', 'false');
    const tip = assignmentHoverText(count);
    el.setAttribute('title', tip);
    el.setAttribute('aria-label', tip);
}
/** One-time DOM + bus subscription for the floating badge. */
export function initNotificationBadge() {
    if (badgeInitialized)
        return;
    badgeInitialized = true;
    let el = document.getElementById('global-notification-badge');
    if (!el) {
        el = document.createElement('button');
        el.id = 'global-notification-badge';
        el.type = 'button';
        el.style.cssText = [
            'display:none',
            'position:fixed',
            'right:16px',
            'bottom:16px',
            'z-index:9999',
            'min-width:28px',
            'height:28px',
            'padding:0 8px',
            'border:none',
            'border-radius:999px',
            'background:#dc2626',
            'color:#fff',
            'font-size:13px',
            'font-weight:600',
            'align-items:center',
            'justify-content:center',
            'cursor:pointer',
            'box-shadow:0 2px 8px rgba(0,0,0,.2)',
        ].join(';');
        el.addEventListener('click', () => clearUnread());
        document.body.appendChild(el);
    }
    on('notifications:updated', (count) => {
        const n = typeof count === 'number' ? count : getUnreadCount();
        renderBadgeEl(el, n);
    });
}
