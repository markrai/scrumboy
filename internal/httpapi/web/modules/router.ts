import { apiFetch } from './api.js';
import { renderAuth, renderResetPassword, renderProjects, renderDashboard, renderBoard, renderNotFound, stopBoardEvents } from './views/index.js';
import { getAuthStatusChecked, getUser, getBootstrapAvailable, getAuthStatusAvailable, getBoard } from './state/selectors.js';
import { setAuthStatusChecked, setAuthStatusAvailable, setUser, setBootstrapAvailable, setRoute, setTag, setSearch, setSlug, setProjectId, setBoard, resetUserScopedState, setTagColors, setOpenTodoSegment, hydrateDashboardTodoSortFromServer } from './state/mutations.js';
import type { Board } from './types.js';
import { RouteName, AuthStatusResponse, User } from './types.js';
import { loadUserTheme } from './theme.js';

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
    
    // Detect user change and reset state if user ID changed
    const oldUser = getUser();
    const newUser = st && st.user ? st.user : null;
    const oldUserId = oldUser?.id || null;
    const newUserId = newUser?.id || null;
    
    if (oldUserId !== newUserId) {
      // User changed (logout, login as different user, or initial load)
      resetUserScopedState();
    }
    
    setUser(newUser);
    setBootstrapAvailable(!!(st && st.bootstrapAvailable));
    
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

  // Show auth UI when not logged in (full mode): on projects list or on any board path (backend returns 404 when unauthenticated).
  if (getUser() == null && getAuthStatusChecked() && getAuthStatusAvailable()) {
    if (r.name === "projects" || r.name === "dashboard" || r.name === "boardBySlug") {
      console.log("Router: showing auth UI (not logged in)");
      renderAuth({ next: window.location.pathname + window.location.search, bootstrap: getBootstrapAvailable() });
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
        renderAuth({ next: window.location.pathname + window.location.search, bootstrap: false });
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
