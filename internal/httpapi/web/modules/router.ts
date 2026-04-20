import { apiFetch } from './api.js';
import { renderAuth, renderResetPassword, renderProjects, renderDashboard, renderBoard, renderNotFound, stopBoardEvents } from './views/index.js';
import { startGlobalRealtime, stopGlobalRealtime, initForegroundLifecycle } from './core/realtime.js';
import { hydrateNotificationsForUser, initNotificationBadge } from './core/notifications.js';
import { unsubscribeFromPush, maybeAutoSubscribePushAfterLogin } from './core/push.js';
import { getAuthStatusChecked, getUser, getBootstrapAvailable, getAuthStatusAvailable, getBoard, getOidcEnabled, getLocalAuthEnabled } from './state/selectors.js';
import { setAuthStatusChecked, setAuthStatusAvailable, setUser, setBootstrapAvailable, setOidcEnabled, setLocalAuthEnabled, setRoute, setTag, setSearch, setSlug, setProjectId, setBoard, resetUserScopedState, setTagColors, setOpenTodoSegment, hydrateDashboardTodoSortFromServer } from './state/mutations.js';
import type { Board } from './types.js';
import { RouteName, AuthStatusResponse, User } from './types.js';
import { loadUserTheme } from './theme.js';
import { applyWallpaperForAuthContext, loadUserWallpaper } from './wallpaper.js';
import {
  hydrateVoiceFlowEnabledFromServer,
  hydrateVoiceFlowHandsFreeConfirmationFromServer,
  hydrateVoiceFlowModeFromServer,
  VOICE_FLOW_ENABLED_PREFERENCE_KEY,
  VOICE_FLOW_HANDS_FREE_CONFIRMATION_PREFERENCE_KEY,
  VOICE_FLOW_MODE_PREFERENCE_KEY,
} from './core/voiceflow-preferences.js';

// Attach foreground listeners once at module load (idempotent guard lives in initForegroundLifecycle).
initForegroundLifecycle();

type ParsedRoute = {
  name: RouteName;
  slug?: string;
  tag?: string;
  search?: string;
  sprintId?: string | null;
  openTodoId?: string;
  openTodoSegment?: string;
  token?: string;
};

let isRouting = false;
let rerouteRequested = false;
let lastHandledBoardRoute: { slug: string; tag: string; search: string; sprintId: string | null; openTodoSegment: string | null } | null = null;

function navigate(path: string, options?: { state?: object }): void {
  console.log("navigate called with path:", path);
  history.pushState(options?.state ?? {}, "", path);
  router().catch((err) => {
    console.error("Router error:", err);
  });
}

function parseRoute(): ParsedRoute {
  const path = window.location.pathname;
  const url = new URL(window.location.href);
  const tag = url.searchParams.get("tag") || "";
  const search = url.searchParams.get("search") || "";
  const sprintIdRaw = url.searchParams.get("sprintId");
  const sprintId = sprintIdRaw === "" ? null : (sprintIdRaw || null);
  const openTodoId = url.searchParams.get("openTodoId") || undefined;

  if (path === "/") return { name: "projects" };
  if (path === "/dashboard") return { name: "dashboard" };
  if (path === "/auth/reset-password") return { name: "reset-password", token: url.searchParams.get("token") || undefined };
  const tm = path.match(/^\/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)\/t\/(\d+)\/?$/);
  if (tm && !tm[1].includes("--")) return { name: "boardBySlug", slug: tm[1], tag, search, sprintId, openTodoSegment: tm[2] };
  // Canonical: /{slug} only (lowercase, digits, hyphens; max 32; no leading/trailing hyphen; no consecutive hyphens).
  const sm = path.match(/^\/([a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?)\/?$/);
  if (sm && !sm[1].includes("--")) return { name: "boardBySlug", slug: sm[1], tag, search, sprintId, openTodoId };
  return { name: "notfound" };
}

function normalize(v?: string | null): string {
  return v || "";
}

function shouldDoLightweightBoardUpdate(r: ParsedRoute): boolean {
  if (r.name !== "boardBySlug" || !r.slug || !getBoard()) return false;
  if (!lastHandledBoardRoute) return false;
  const openSeg = r.openTodoSegment || null;
  const rSprintId = r.sprintId ?? null;
  return (
    lastHandledBoardRoute.slug === r.slug &&
    normalize(lastHandledBoardRoute.tag) === normalize(r.tag) &&
    normalize(lastHandledBoardRoute.search) === normalize(r.search) &&
    (lastHandledBoardRoute.sprintId ?? null) === rSprintId &&
    lastHandledBoardRoute.openTodoSegment !== openSeg
  );
}

async function routeOnce(): Promise<void> {
  // Determine auth/bootstrap state deterministically via /api/auth/status.
  // In anonymous mode, returns 200 with user: null, bootstrapAvailable: false (no console errors, clear contract).
  // In full mode, returns 200 with user info and bootstrapAvailable flag.
  if (!getAuthStatusChecked()) {
    setAuthStatusChecked(true);
    const st = await apiFetch<AuthStatusResponse>("/api/auth/status");
    
    // Use explicit mode field from server to determine anonymous vs full mode
    const isAnonymousMode = st.mode === "anonymous";
    setAuthStatusAvailable(!isAnonymousMode);
    // Wallpaper: defer until auth is known (no flash; anonymous deployment never shows wallpaper)
    applyWallpaperForAuthContext(!isAnonymousMode);

    // Detect user change and reset state if user ID changed
    const oldUser = getUser();
    const newUser = st && st.user ? st.user : null;
    const oldUserId = oldUser?.id || null;
    const newUserId = newUser?.id || null;
    
    if (oldUserId !== newUserId) {
      // User changed (logout, login as different user, or initial load)
      resetUserScopedState();
      stopGlobalRealtime();
    }

    setUser(newUser);
    setBootstrapAvailable(!!(st && st.bootstrapAvailable));
    setOidcEnabled(!!(st && st.oidcEnabled));
    setLocalAuthEnabled(st && st.localAuthEnabled !== false);
    
    // Load full profile (including avatar) when logged in; /api/auth/status omits image to keep it lean
    if (newUser) {
      try {
        const me = await apiFetch<User>("/api/me");
        if (me) setUser(me);
      } catch {
        // Ignore — session may be invalid or user logged out
      }
    }
    
    // Load user preferences if user is logged in (full mode only)
    if (newUser) {
      // Load tag colors
      try {
        const resp = await apiFetch<{ value: string }>("/api/user/preferences?key=tagColors");
        if (resp && resp.value) {
          const tagColors = JSON.parse(resp.value);
          setTagColors(tagColors);
        }
      } catch (err) {
        // Ignore errors loading preferences (might not exist yet)
      }
      
      // Load theme
      await loadUserTheme();

      // Load wallpaper
      await loadUserWallpaper();
      
      // Load UI preferences (projectView, projectsTab)
      try {
        const projectViewResp = await apiFetch<{ value: string }>("/api/user/preferences?key=projectView");
        if (projectViewResp && projectViewResp.value) {
          localStorage.setItem("projectView", projectViewResp.value);
        }
      } catch (err) {
        // Ignore errors
      }
      
      try {
        const projectsTabResp = await apiFetch<{ value: string }>("/api/user/preferences?key=projectsTab");
        if (projectsTabResp && projectsTabResp.value) {
          localStorage.setItem("projectsTab", projectsTabResp.value);
        }
      } catch (err) {
        // Ignore errors
      }

      try {
        const sortResp = await apiFetch<{ value: string }>('/api/user/preferences?key=dashboardTodoSort');
        const v = sortResp?.value;
        if (v === 'board' || v === 'activity') {
          hydrateDashboardTodoSortFromServer(v);
        }
      } catch (err) {
        // Ignore errors
      }

      try {
        const enabledResp = await apiFetch<{ value: string }>(`/api/user/preferences?key=${VOICE_FLOW_ENABLED_PREFERENCE_KEY}`);
        if (enabledResp?.value) hydrateVoiceFlowEnabledFromServer(enabledResp.value);
      } catch (err) {
        // Ignore errors
      }

      try {
        const modeResp = await apiFetch<{ value: string }>(`/api/user/preferences?key=${VOICE_FLOW_MODE_PREFERENCE_KEY}`);
        if (modeResp?.value) hydrateVoiceFlowModeFromServer(modeResp.value);
      } catch (err) {
        // Ignore errors
      }

      try {
        const confirmationResp = await apiFetch<{ value: string }>(`/api/user/preferences?key=${VOICE_FLOW_HANDS_FREE_CONFIRMATION_PREFERENCE_KEY}`);
        if (confirmationResp?.value) hydrateVoiceFlowHandsFreeConfirmationFromServer(confirmationResp.value);
      } catch (err) {
        // Ignore errors
      }
    }

    if (getAuthStatusAvailable()) {
      initNotificationBadge();
      const sessionUser = getUser();
      if (sessionUser) {
        hydrateNotificationsForUser(sessionUser.id);
        startGlobalRealtime();
        void maybeAutoSubscribePushAfterLogin(sessionUser.id);
      } else {
        stopGlobalRealtime();
        hydrateNotificationsForUser(null);
        // Logged-out (full mode): best-effort remove this browser's push endpoint only. Server DELETE may
        // fail after auth is gone (harmless); local PushManager.unsubscribe still runs. A stale DB row is
        // acceptable—backend prunes the endpoint on failed send (4xx from the push service). Swallow errors
        // so startup routing never depends on push teardown.
        void unsubscribeFromPush().catch(() => {});
      }
    } else {
      // Anonymous mode: push API is unavailable; do not call unsubscribe here (would local-unsub without a server delete and is unnecessary).
      stopGlobalRealtime();
      hydrateNotificationsForUser(null);
    }
  }

  const r = parseRoute();
  console.log("Router: parsed route:", r);
  setRoute(r.name);
  setTag(r.tag || "");
  setSearch(r.search || "");
  setSlug(r.slug || null);
  setOpenTodoSegment(r.openTodoSegment || null);
  if (r.name !== "boardBySlug") {
    stopBoardEvents();
    setProjectId(null);
    setBoard(null);
    lastHandledBoardRoute = null;
  }

  // Reset password page: no auth required (token is auth)
  if (r.name === "reset-password") {
    renderResetPassword(r.token);
    return;
  }

  // Show auth UI when not logged in (full mode): projects list and dashboard only.
  // Board URLs (/slug) must render for anonymous visitors so shareable temp boards and public reads work; access is enforced by GET /api/board/{slug} (404 when not allowed).
  if (getUser() == null && getAuthStatusChecked() && getAuthStatusAvailable()) {
    if (r.name === "projects" || r.name === "dashboard") {
      console.log("Router: showing auth UI (not logged in)");
      renderAuth({ next: window.location.pathname + window.location.search, bootstrap: getBootstrapAvailable(), oidcEnabled: getOidcEnabled(), localAuthEnabled: getLocalAuthEnabled() });
      return;
    }
  }

  if (r.name === "projects") {
    console.log("Router: rendering projects");
    await renderProjects();
    return;
  }
  if (r.name === "dashboard") {
    console.log("Router: rendering dashboard");
    await renderDashboard();
    return;
  }
  if (r.name === "boardBySlug") {
    // Default: no sprint filter. URL stays e.g. /scrumboy without ?sprintId=scheduled.
    console.log("Router: rendering board, slug:", r.slug, "tag:", r.tag, "search:", r.search, "sprintId:", r.sprintId);
    const prefetchedBoard = (history.state as { boardData?: Board } | null)?.boardData;
    const isLightweight = shouldDoLightweightBoardUpdate(r);
    try {
      if (isLightweight) {
        await renderBoard(r.slug || null, r.tag || "", r.search || "", r.sprintId ?? null, r.openTodoId || null, r.openTodoSegment || null, { skipLoad: true });
      } else {
        await renderBoard(r.slug || null, r.tag || "", r.search || "", r.sprintId ?? null, r.openTodoId || null, r.openTodoSegment || null, {
          skipLoad: false,
          prefetchedBoard: prefetchedBoard?.project && prefetchedBoard?.columns ? (prefetchedBoard as Board) : undefined,
        });
      }
      lastHandledBoardRoute = {
        slug: r.slug || "",
        tag: normalize(r.tag),
        search: normalize(r.search),
        sprintId: r.sprintId ?? null,
        openTodoSegment: r.openTodoSegment || null,
      };
      console.log("Router: board rendered successfully");
      if (!isLightweight && window.matchMedia("(min-width: 621px)").matches) {
        window.scrollTo(0, 0);
      }
    } catch (err) {
      console.error("Router: error rendering board:", err);
      if (err && (err as Error & { status?: number }).status === 401) {
        // Only show auth UI for 401s (entry points). Resource endpoints should generally return 404 when unauthenticated.
        renderAuth({ next: window.location.pathname + window.location.search, bootstrap: false, oidcEnabled: getOidcEnabled(), localAuthEnabled: getLocalAuthEnabled() });
        return;
      }
      throw err;
    }
    return;
  }
  console.log("Router: rendering not found");
  renderNotFound();
}

async function router(): Promise<void> {
  if (isRouting) {
    rerouteRequested = true;
    return;
  }
  isRouting = true;
  try {
    do {
      rerouteRequested = false;
      await routeOnce();
    } while (rerouteRequested);
  } finally {
    isRouting = false;
  }
}

window.addEventListener("popstate", () => {
  router().catch((err) => {
    console.error("Router error:", err);
  });
});

export { navigate, parseRoute, router };
