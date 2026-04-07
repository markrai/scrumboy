/**
 * Client-only assignment notifications: legacy unread counter + inbox list + floating panel.
 */

import { emit, on } from '../events.js';
import { apiFetch } from '../api.js';
import { getProjectId, getProjects } from '../state/selectors.js';
import { escapeHTML } from '../utils.js';
import type { Project } from '../types.js';
import { resolveNotificationProjectSlugCore } from './notification-slug-resolve.js';

const STORAGE_PREFIX = 'scrumboy_unread_v1_';
const LIST_STORAGE_PREFIX = 'scrumboy_notifications_v1_';

export type NotificationItem = {
  id: string;
  type: 'todo.assigned';
  title: string;
  projectId: number;
  projectSlug: string | null;
  todoId: number;
  timestamp: number;
  read: boolean;
};

/** Wire shape for todo.assigned from GET /api/me/realtime (subset). */
export type TodoAssignedRealtimePayload = {
  id?: string;
  type?: string;
  projectId?: number;
  projectSlug?: string | null;
  payload?: { todoId?: number; title?: string; assigneeId?: number; actorUserId?: number };
};

/**
 * Resolves project slug: in-memory map → getProjects() catalog → event/localStorage slug.
 * Central entry; do not read item.projectSlug or parsed.projectSlug for navigation without this.
 */
export function resolveNotificationProjectSlug(projectId: number, eventSlug?: string | null): string | null {
  const mapSlug = projectSlugById.get(projectId);
  const projects = getProjects();
  const row = projects?.find((x) => x.id === projectId);
  const catalogSlug =
    row && typeof row.slug === 'string' && row.slug.length > 0 ? row.slug : null;
  return resolveNotificationProjectSlugCore(eventSlug, mapSlug ?? null, catalogSlug);
}

let unreadCount = 0;
let currentUserId: number | null = null;
let badgeInitialized = false;

let notificationItems: NotificationItem[] = [];
const projectSlugById = new Map<number, string>();
let projectsLoadPromise: Promise<void> | null = null;
let projectsCatalogLoaded = false;

let panelEl: HTMLDivElement | null = null;
let listEl: HTMLDivElement | null = null;
let badgeEl: HTMLButtonElement | null = null;
let notificationPanelOpen = false;
let outsideCloseAttached = false;
let escCloseAttached = false;

function listStorageKey(userId: number): string {
  return `${LIST_STORAGE_PREFIX}${userId}`;
}

function storageKey(userId: number): string {
  return `${STORAGE_PREFIX}${userId}`;
}

function readStoredCount(userId: number): number {
  try {
    const raw = localStorage.getItem(storageKey(userId));
    if (raw == null) return 0;
    const n = parseInt(raw, 10);
    return Number.isFinite(n) && n >= 0 ? n : 0;
  } catch {
    return 0;
  }
}

function persistCount(): void {
  if (currentUserId == null) return;
  try {
    localStorage.setItem(storageKey(currentUserId), String(unreadCount));
  } catch {
    /* ignore */
  }
}

function normalizeItem(raw: unknown): NotificationItem | null {
  if (!raw || typeof raw !== 'object') return null;
  const o = raw as Record<string, unknown>;
  const id = typeof o.id === 'string' ? o.id : '';
  const type = o.type === 'todo.assigned' ? 'todo.assigned' : null;
  const title = typeof o.title === 'string' ? o.title : '';
  const projectId = typeof o.projectId === 'number' ? o.projectId : NaN;
  const todoId = typeof o.todoId === 'number' ? o.todoId : NaN;
  const timestamp = typeof o.timestamp === 'number' ? o.timestamp : Date.now();
  const read = typeof o.read === 'boolean' ? o.read : false;
  let projectSlug: string | null = null;
  if (o.projectSlug === null) projectSlug = null;
  else if (typeof o.projectSlug === 'string') projectSlug = o.projectSlug;
  if (!id || !type || !Number.isFinite(projectId) || !Number.isFinite(todoId)) return null;
  return {
    id,
    type: 'todo.assigned',
    title,
    projectId,
    projectSlug,
    todoId,
    timestamp,
    read,
  };
}

function loadListFromStorage(userId: number): void {
  try {
    const raw = localStorage.getItem(listStorageKey(userId));
    if (raw == null) {
      notificationItems = [];
      return;
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) {
      notificationItems = [];
      return;
    }
    const items: NotificationItem[] = [];
    for (const row of parsed) {
      const n = normalizeItem(row);
      if (n) items.push(n);
    }
    notificationItems = items.length > 100 ? items.slice(0, 100) : items;
  } catch {
    notificationItems = [];
  }
}

function persistList(): void {
  if (currentUserId == null) return;
  try {
    localStorage.setItem(listStorageKey(currentUserId), JSON.stringify(notificationItems));
  } catch {
    /* ignore */
  }
}

/** Coalesce localStorage + bus work during bursty SSE (startup replay, many assignments). */
let persistEmitTimer: ReturnType<typeof setTimeout> | null = null;

function schedulePersistAndEmit(): void {
  if (persistEmitTimer != null) return;
  persistEmitTimer = setTimeout(() => {
    persistEmitTimer = null;
    persistList();
    emitListUpdated();
  }, 48);
}

function flushPersistAndEmit(): void {
  if (persistEmitTimer != null) {
    clearTimeout(persistEmitTimer);
    persistEmitTimer = null;
  }
  persistList();
  emitListUpdated();
}

function capList(): void {
  if (notificationItems.length > 100) {
    notificationItems = notificationItems.slice(0, 100);
  }
}

function isDuplicate(candidate: NotificationItem): boolean {
  if (notificationItems.some((i) => i.id === candidate.id)) return true;
  return notificationItems.some(
    (i) => i.type === candidate.type && i.projectId === candidate.projectId && i.todoId === candidate.todoId
  );
}

function emitListUpdated(): void {
  emit('notifications:updated', getListUnreadCount());
}

/** Unread count from inbox list (badge should use this). */
export function getListUnreadCount(): number {
  return notificationItems.filter((i) => !i.read).length;
}

function resetProjectSlugCache(): void {
  projectSlugById.clear();
  projectsCatalogLoaded = false;
  projectsLoadPromise = null;
}

function loadProjectsIntoMap(): Promise<void> {
  return apiFetch<Project[]>('/api/projects').then((projects) => {
    for (const p of projects ?? []) {
      if (typeof p.id === 'number' && typeof p.slug === 'string' && p.slug.length > 0) {
        projectSlugById.set(p.id, p.slug);
      }
    }
    projectsCatalogLoaded = true;
  });
}

function handleListClick(ev: MouseEvent): void {
  const btn = (ev.target as HTMLElement | null)?.closest?.('button[data-notification-id]') as HTMLButtonElement | null;
  if (!btn) return;
  const id = btn.getAttribute('data-notification-id');
  if (!id) return;
  const item = notificationItems.find((i) => i.id === id);
  if (!item) return;

  void (async () => {
    ingestProjectsFromApp(getProjects() ?? undefined);
    let slug = resolveNotificationProjectSlug(item.projectId, item.projectSlug);
    if (slug == null || slug === '') {
      await ensureProjectsLoaded();
      slug = resolveNotificationProjectSlug(item.projectId, item.projectSlug);
    }
    if (slug == null || slug === '') return;

    item.projectSlug = slug;
    item.read = true;
    flushPersistAndEmit();
    closePanel();
    const path = `/${slug}?openTodoId=${item.todoId}`;
    void import('../router.js').then((m) => m.navigate(path));
  })();
}

/** Single-flight: at most one /api/projects in flight; catalog reused for all slugs. */
function ensureProjectsLoaded(): Promise<void> {
  if (projectsCatalogLoaded) return Promise.resolve();
  if (projectsLoadPromise) return projectsLoadPromise;
  projectsLoadPromise = loadProjectsIntoMap()
    .catch(() => {
      /* keep partial map */
    })
    .finally(() => {
      projectsLoadPromise = null;
    });
  return projectsLoadPromise;
}

function reconcileNotificationItemsFromResolver(): void {
  let itemsChanged = false;
  for (const it of notificationItems) {
    const resolved = resolveNotificationProjectSlug(it.projectId, it.projectSlug);
    if (resolved !== it.projectSlug) {
      it.projectSlug = resolved;
      itemsChanged = true;
    }
  }
  if (itemsChanged) {
    schedulePersistAndEmit();
  }
}

/**
 * Merge id→slug from app state (dashboard / projects / board load). No network.
 * Reconciles stored rows when catalog/map disagrees with persisted slug.
 */
export function ingestProjectsFromApp(projects: Project[] | null | undefined): void {
  if (!projects?.length) return;
  for (const p of projects) {
    if (typeof p.id === 'number' && typeof p.slug === 'string' && p.slug.length > 0) {
      projectSlugById.set(p.id, p.slug);
    }
  }
  reconcileNotificationItemsFromResolver();
}

function tryHydrateSlugForNewItem(item: NotificationItem): void {
  const projects = getProjects();
  if (!projects?.length) return;
  const p = projects.find((x) => x.id === item.projectId);
  if (p?.slug) {
    item.projectSlug = p.slug;
    projectSlugById.set(item.projectId, p.slug);
  }
}

function formatRelativeTime(ts: number): string {
  const sec = Math.floor((Date.now() - ts) / 1000);
  if (sec < 45) return 'Just now';
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

function renderPanelList(): void {
  if (!listEl) return;
  if (notificationItems.length === 0) {
    listEl.innerHTML = `<div class="notification-panel__empty muted" style="padding:16px;font-size:14px;">No notifications yet.</div>`;
    return;
  }
  const rows: string[] = [];
  for (const it of notificationItems) {
    const unreadCls = it.read ? '' : ' notification-panel__row--unread';
    const resolvedSlug = resolveNotificationProjectSlug(it.projectId, it.projectSlug);
    const hasSlug = resolvedSlug != null && resolvedSlug.length > 0;
    const opacity = hasSlug ? '1' : '0.75';
    rows.push(`<button type="button" class="notification-panel__row${unreadCls}" data-notification-id="${escapeHTML(it.id)}" style="display:block;width:100%;text-align:left;padding:12px 14px;border:none;background:transparent;cursor:pointer;opacity:${opacity};border-bottom:1px solid var(--border, rgba(0,0,0,.08));font:inherit;" tabindex="0">
      <div style="font-weight:${it.read ? '500' : '700'};font-size:14px;color:var(--text, #111);">${escapeHTML(it.title)}</div>
      <div class="muted" style="font-size:12px;margin-top:4px;">Assigned to you</div>
      <div class="muted" style="font-size:11px;margin-top:6px;">${escapeHTML(formatRelativeTime(it.timestamp))}</div>
    </button>`);
  }
  listEl.innerHTML = rows.join('');
}

function markAllRead(): void {
  let any = false;
  for (const it of notificationItems) {
    if (!it.read) {
      it.read = true;
      any = true;
    }
  }
  if (!any) return;
  flushPersistAndEmit();
  if (notificationPanelOpen) renderPanelList();
}

function closePanel(): void {
  if (!panelEl) return;
  notificationPanelOpen = false;
  panelEl.classList.remove('notification-panel--open');
  panelEl.setAttribute('aria-hidden', 'true');
}

function openPanel(): void {
  if (!panelEl) return;
  notificationPanelOpen = true;
  panelEl.classList.add('notification-panel--open');
  panelEl.setAttribute('aria-hidden', 'false');
  ingestProjectsFromApp(getProjects() ?? undefined);
  renderPanelList();
}

function togglePanel(): void {
  if (notificationPanelOpen) closePanel();
  else openPanel();
}

function onDocumentPointerDown(ev: MouseEvent): void {
  if (!notificationPanelOpen) return;
  const t = ev.target as Node | null;
  if (!t) return;
  if (badgeEl && badgeEl.contains(t)) return;
  if (panelEl && panelEl.contains(t)) return;
  closePanel();
}

function onDocumentKeydown(ev: KeyboardEvent): void {
  if (!notificationPanelOpen) return;
  if (ev.key === 'Escape') {
    ev.preventDefault();
    closePanel();
  }
}

function ensureOutsideCloseListeners(): void {
  if (outsideCloseAttached) return;
  outsideCloseAttached = true;
  document.addEventListener('pointerdown', onDocumentPointerDown, true);
}

function ensureEscListener(): void {
  if (escCloseAttached) return;
  escCloseAttached = true;
  document.addEventListener('keydown', onDocumentKeydown);
}

function injectPanelStyles(): void {
  if (document.getElementById('notification-panel-styles')) return;
  const style = document.createElement('style');
  style.id = 'notification-panel-styles';
  style.textContent = `
    #global-notification-panel {
      position: fixed;
      z-index: 9998;
      background: var(--panel, #fff);
      color: var(--text, #111);
      border-radius: 12px 12px 0 0;
      box-shadow: 0 -4px 24px rgba(0,0,0,.15);
      display: flex;
      flex-direction: column;
      max-height: 50vh;
      left: 0;
      right: 0;
      bottom: 0;
      width: 100%;
      transform: translateY(100%);
      transition: transform 0.25s ease;
      padding-bottom: env(safe-area-inset-bottom, 0);
      pointer-events: none;
    }
    #global-notification-panel.notification-panel--open {
      transform: translateY(0);
      pointer-events: auto;
    }
    .notification-panel__header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 12px 14px;
      border-bottom: 1px solid var(--border, rgba(0,0,0,.08));
      flex-shrink: 0;
    }
    .notification-panel__header h2 {
      margin: 0;
      font-size: 16px;
      font-weight: 600;
    }
    .notification-panel__scroll {
      overflow-y: auto;
      flex: 1;
      min-height: 0;
    }
    .notification-panel__row--unread {
      box-shadow: inset 3px 0 0 #dc2626;
    }
    @media (min-width: 641px) {
      #global-notification-panel {
        left: auto;
        right: 16px;
        bottom: 72px;
        width: 360px;
        max-height: 60vh;
        border-radius: 12px;
        box-shadow: 0 8px 24px rgba(0,0,0,.15);
        transform: translateY(16px);
        opacity: 0;
        transition: transform 0.25s ease, opacity 0.25s ease;
      }
      #global-notification-panel.notification-panel--open {
        transform: translateY(0);
        opacity: 1;
      }
    }
  `;
  document.head.appendChild(style);
}

function createPanel(): void {
  injectPanelStyles();
  const wrap = document.createElement('div');
  wrap.id = 'global-notification-panel';
  wrap.setAttribute('role', 'dialog');
  wrap.setAttribute('aria-label', 'Notifications');
  wrap.setAttribute('aria-hidden', 'true');
  wrap.innerHTML = `
    <div class="notification-panel__header">
      <h2>Notifications</h2>
      <button type="button" class="btn btn--small" id="notification-panel-mark-all">Mark all as read</button>
    </div>
    <div class="notification-panel__scroll" id="global-notification-panel-list"></div>
  `;
  wrap.addEventListener('click', (e) => e.stopPropagation());
  wrap.addEventListener('pointerdown', (e) => e.stopPropagation());
  document.body.appendChild(wrap);
  panelEl = wrap;
  listEl = wrap.querySelector('#global-notification-panel-list') as HTMLDivElement;
  listEl.addEventListener('click', (e) => handleListClick(e as MouseEvent));
  const markAll = wrap.querySelector('#notification-panel-mark-all');
  markAll?.addEventListener('click', (e) => {
    e.stopPropagation();
    markAllRead();
  });
}

function emitUpdated(): void {
  emit('notifications:updated', unreadCount);
}

/** Call when the logged-in user id is known or changes. Restores count from localStorage. */
export function hydrateNotificationsForUser(userId: number | null): void {
  const prev = currentUserId;
  // Flush debounced persist while `currentUserId` / `notificationItems` still match the outgoing
  // session; avoids a late timer firing after swap and prevents losing last ~48ms of inbox writes.
  flushPersistAndEmit();
  currentUserId = userId;
  resetProjectSlugCache();
  if (prev !== userId) {
    closePanel();
  }
  if (userId == null) {
    unreadCount = 0;
    notificationItems = [];
    emitListUpdated();
    return;
  }
  unreadCount = readStoredCount(userId);
  loadListFromStorage(userId);
  reconcileNotificationItemsFromResolver();
  emitListUpdated();
}

export function getUnreadCount(): number {
  return unreadCount;
}

export function incrementUnread(): void {
  unreadCount += 1;
  persistCount();
}

export function clearUnread(): void {
  if (unreadCount === 0) return;
  unreadCount = 0;
  emitUpdated();
  if (currentUserId != null) {
    try {
      localStorage.removeItem(storageKey(currentUserId));
    } catch {
      /* ignore */
    }
  }
}

function assignmentHoverText(count: number): string {
  if (count === 1) return '1 todo has been assigned to you';
  return `${count} todos have been assigned to you`;
}

function renderBadgeEl(el: HTMLButtonElement, count: number): void {
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

/**
 * Append a todo.assigned notification (after toast/sound/desktop in realtime).
 * Stays synchronous and cheap: no network; slugs come from app state via tryHydrateSlugForNewItem / ingestProjectsFromApp.
 */
export function appendTodoAssignedNotification(parsed: TodoAssignedRealtimePayload): void {
  if (parsed.type !== 'todo.assigned') return;
  if (currentUserId == null) return;
  const inner = parsed.payload;
  if (!inner || typeof inner.todoId !== 'number') return;
  const projectId = typeof parsed.projectId === 'number' ? parsed.projectId : null;
  if (projectId == null) return;

  const wireId =
    typeof parsed.id === 'string' && parsed.id !== ''
      ? parsed.id
      : `assign-${projectId}-${inner.todoId}-${Date.now()}`;
  const titleStr = typeof inner.title === 'string' ? inner.title : '';
  const read = getProjectId() === projectId;

  const item: NotificationItem = {
    id: wireId,
    type: 'todo.assigned',
    title: titleStr || 'Todo',
    projectId,
    projectSlug: null,
    todoId: inner.todoId,
    timestamp: Date.now(),
    read,
  };

  if (isDuplicate(item)) return;

  tryHydrateSlugForNewItem(item);
  item.projectSlug = resolveNotificationProjectSlug(projectId, parsed.projectSlug ?? null);
  notificationItems = [item, ...notificationItems];
  capList();
  schedulePersistAndEmit();
}

/** One-time DOM + bus subscription for the floating badge and panel. */
export function initNotificationBadge(): void {
  if (badgeInitialized) return;
  badgeInitialized = true;

  let el = document.getElementById('global-notification-badge') as HTMLButtonElement | null;
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
    document.body.appendChild(el);
  }
  badgeEl = el;

  if (!document.getElementById('global-notification-panel')) {
    createPanel();
  }

  el.addEventListener('click', (e) => {
    e.stopPropagation();
    togglePanel();
    ensureOutsideCloseListeners();
    ensureEscListener();
  });
  el.addEventListener('pointerdown', (e) => e.stopPropagation());

  on('notifications:updated', () => {
    renderBadgeEl(el!, getListUnreadCount());
    if (notificationPanelOpen) renderPanelList();
  });

  renderBadgeEl(el, getListUnreadCount());
}
