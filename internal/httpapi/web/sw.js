// Service Worker for Scrumboy PWA
// Version injected at serve time: {{VERSION}}
const CACHE_VERSION = '{{VERSION}}';
const CACHE_NAME = 'scrumboy-' + (CACHE_VERSION || '0');

const urlsToCache = [
  '/',
  '/index.html',
  '/styles.css',
  '/app.js',
  '/manifest.json',
  '/favicon.ico',
  '/icon-512.png',
  '/scrumboytext.png',
  '/vendor/sortable.min.js',
  '/dist/api.js',
  '/dist/dom/elements.js',
  '/dist/theme.js',
  '/dist/utils.js',
  '/dist/router.js',
  '/dist/state/state.js',
  '/dist/state/selectors.js',
  '/dist/state/mutations.js',
  '/dist/views/index.js',
  '/dist/views/auth.js',
  '/dist/views/board.js',
  '/dist/views/dashboard.js',
  '/dist/views/notfound.js',
  '/dist/views/projects.js',
  '/dist/dialogs/settings.js',
  '/dist/dialogs/todo.js',
  '/dist/dialogs/bulk-edit.js',
  '/dist/features/drag-drop.js',
  '/dist/features/context-menu.js',
  '/dist/features/context-menu-button.js',
  '/dist/orchestration/board-refresh.js',
  '/dist/pwaUpdate.js',
  '/dist/types.js'
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => {
        return Promise.allSettled(
          urlsToCache.map((url) => cache.add(url).catch((err) => console.warn('Cache miss:', url, err)))
        );
      })
      .catch((err) => console.error('SW install failed:', err))
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((cacheNames) => {
      return Promise.all(
        cacheNames.map((name) => {
          if (name !== CACHE_NAME) {
            console.log('Deleting old cache:', name);
            return caches.delete(name);
          }
        })
      );
    }).then(() => self.clients.claim())
  );
});

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

self.addEventListener('push', (event) => {
  event.waitUntil(
    (async () => {
      let data = {};
      try {
        if (event.data) {
          data = await event.data.json();
        }
      } catch (e) {
        /* ignore */
      }
      if (data.type !== 'todo_assigned' && !data.scrumboyPush) {
        return;
      }
      const clientsList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      for (const client of clientsList) {
        if (client.visibilityState === 'visible' && client.focused) {
          if (data.debug) {
            console.log('[push] skipped (foreground window focused)');
          }
          return;
        }
      }
      const title = data.title || 'Scrumboy';
      const body = typeof data.body === 'string' ? data.body : '';
      // Stash payload for a future cross-project notification center / deep-links — not used for click routing yet.
      await self.registration.showNotification(title, {
        body: body || undefined,
        data: {
          type: data.type,
          projectSlug: data.projectSlug,
          todoId: data.todoId,
        },
      });
    })()
  );
});

// Tap: deep-link to assigned todo when push data includes projectSlug + todoId; else home.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const d = event.notification.data || {};
      const slug = d.projectSlug;
      const todoId = d.todoId;
      const slugOk = typeof slug === 'string' && slug.length > 0;
      const todoOk =
        (typeof todoId === 'number' && Number.isFinite(todoId)) ||
        (typeof todoId === 'string' && todoId.length > 0);
      let targetUrl = self.location.origin + '/';
      if (slugOk && todoOk) {
        const idPart = typeof todoId === 'number' ? String(todoId) : todoId;
        targetUrl =
          self.location.origin +
          '/' +
          encodeURIComponent(slug) +
          '?openTodoId=' +
          encodeURIComponent(idPart);
      }

      const clientsArr = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      for (const client of clientsArr) {
        try {
          const u = new URL(client.url);
          if (u.origin === self.location.origin) {
            const focused = await client.focus();
            if (focused) {
              if (typeof focused.navigate === 'function') {
                await focused.navigate(targetUrl);
              } else {
                await self.clients.openWindow(targetUrl);
              }
              return focused;
            }
          }
        } catch (e) {
          /* ignore */
        }
      }
      return self.clients.openWindow(targetUrl);
    })()
  );
});

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;

  // Cache API only supports GET; do not try to cache POST (e.g. form login)
  const isGet = event.request.method === 'GET';

  // Bypass SW for API requests — let browser fetch directly to network.
  // API responses are user/session scoped and must never be cached or revalidated by the SW.
  if (url.pathname.startsWith('/api/')) {
    return;
  }

  // Network-first for HTML and main app script so updates are seen after reload
  if (isGet && (event.request.destination === 'document' || event.request.mode === 'navigate' ||
      url.pathname === '/' || url.pathname.endsWith('.html') ||
      url.pathname === '/app.js' || url.pathname === '/styles.css' || url.pathname.startsWith('/dist/'))) {
    event.respondWith(
      fetch(event.request)
        .then((response) => {
          if (isGet && response.ok) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
          }
          return response;
        })
        .catch(() => caches.match(event.request).then((cached) => cached || caches.match('/index.html')))
    );
    return;
  }

  // Cache-first for other GET assets
  if (!isGet) return;
  event.respondWith(
    caches.match(event.request)
      .then((cached) => {
        if (cached) return cached;
        return fetch(event.request).then((response) => {
          if (response.ok) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
          }
          return response;
        }).catch(() => new Response('Offline', { status: 503 }));
      })
  );
});
