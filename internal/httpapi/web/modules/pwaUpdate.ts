/**
 * PWA update dialog: detect when a new service worker is available and prompt
 * the user to reload so they get the latest app (avoids stale cache on mobile).
 * Pattern: C:\dev\project\pattern\pwa_update_dialog.md
 */

import { getAppVersion } from './utils.js';

const PWA_UPDATE_PENDING_KEY = 'pwaUpdatePending';

let serviceWorkerRegistration: ServiceWorkerRegistration | null = null;
let updateNotificationShown = false;

function showUpdateNotification(): void {
  if (document.getElementById('updateNotification') || updateNotificationShown) return;
  updateNotificationShown = true;
  const notification = document.createElement('div');
  notification.id = 'updateNotification';
  notification.setAttribute('role', 'status');
  notification.style.cssText = `
    position: fixed;
    bottom: 20px;
    left: 50%;
    transform: translateX(-50%);
    background: var(--panel, #111827);
    color: var(--text, #e5e7eb);
    border: 1px solid var(--border, rgba(255,255,255,0.08));
    padding: 12px 20px;
    border-radius: var(--radius, 14px);
    box-shadow: var(--shadow, 0 10px 30px rgba(0,0,0,0.35));
    z-index: 10000;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
    font-size: 0.9rem;
    max-width: 90%;
  `;
  notification.innerHTML = `
    <span>New version available!</span>
    <span style="display: inline-flex; gap: 8px; flex-shrink: 0;">
      <button type="button" onclick="reloadForUpdate()" class="btn">Update now</button>
      <button type="button" onclick="dismissUpdateNotification()" class="btn btn--ghost">Later</button>
    </span>
  `;
  document.body.appendChild(notification);
  localStorage.setItem(PWA_UPDATE_PENDING_KEY, '1');
}

function reloadForUpdate(): void {
  const notification = document.getElementById('updateNotification');
  if (notification) notification.remove();
  localStorage.removeItem(PWA_UPDATE_PENDING_KEY);
  if (serviceWorkerRegistration?.waiting) {
    serviceWorkerRegistration.waiting.postMessage({ type: 'SKIP_WAITING' });
  }
  sessionStorage.setItem('updateApplied', 'true');
  setTimeout(() => window.location.reload(), 100);
}

function dismissUpdateNotification(): void {
  const notification = document.getElementById('updateNotification');
  if (notification) notification.remove();
  updateNotificationShown = false;
}

function registerServiceWorker(): void {
  if (!('serviceWorker' in navigator)) return;

  const isLocalhost =
    window.location.hostname === 'localhost' ||
    window.location.hostname === '127.0.0.1' ||
    window.location.hostname === '[::1]';
  if (isLocalhost && localStorage.getItem('enableServiceWorkerDev') !== 'true') {
    return;
  }

  window.addEventListener('load', () => {
    const version = getAppVersion() || '0';
    const swUrl = '/sw.js?v=' + version;
    navigator.serviceWorker
      .register(swUrl)
      .then((registration) => {
        serviceWorkerRegistration = registration;

        if (sessionStorage.getItem('updateApplied')) {
          sessionStorage.removeItem('updateApplied');
          localStorage.removeItem(PWA_UPDATE_PENDING_KEY);
        } else if (registration.waiting) {
          showUpdateNotification();
        } else if (localStorage.getItem(PWA_UPDATE_PENDING_KEY)) {
          registration.update();
        }

        registration.addEventListener('updatefound', () => {
          const newWorker = registration.installing;
          if (!newWorker) return;
          newWorker.addEventListener('statechange', () => {
            if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
              if (!document.getElementById('updateNotification') && !updateNotificationShown) {
                showUpdateNotification();
              }
            }
          });
        });

        document.addEventListener('visibilitychange', () => {
          if (document.visibilityState === 'visible' && registration.waiting && !document.getElementById('updateNotification')) {
            showUpdateNotification();
          }
        });

        setInterval(() => {
          if (registration.waiting && !document.getElementById('updateNotification')) {
            showUpdateNotification();
          }
        }, 60 * 1000);

        setInterval(() => registration.update(), 5 * 60 * 1000);
      })
      .catch((err) => console.warn('ServiceWorker registration failed:', err));
  });
}

export function registerPwaGlobals(): void {
  (window as unknown as { reloadForUpdate: () => void }).reloadForUpdate = reloadForUpdate;
  (window as unknown as { dismissUpdateNotification: () => void }).dismissUpdateNotification = dismissUpdateNotification;
  registerServiceWorker();
}
