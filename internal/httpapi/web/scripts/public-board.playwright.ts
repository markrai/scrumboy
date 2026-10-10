/**
 * End-to-end: public read-only boards (Phase 4).
 *
 * Spawns a temporary Full Mode Scrumboy server with public projects enabled.
 * Publication has no HTTP control before Phase 5, so the test flips the
 * persisted per-project flag directly in the server's SQLite file.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { expect, test, type Page, type Request } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const OWNER = { email: "public-owner-e2e@example.com", password: "password123", name: "Owner Secret Name" };
const OUTSIDER = { email: "public-outsider-e2e@example.com", password: "password123", name: "Outsider" };

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let publicSlug = "";
let privateSlug = "";

function setPublication(slug: string, enabled: boolean): void {
  const db = new DatabaseSync(path.join(dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.prepare("UPDATE projects SET public_view_enabled = ? WHERE slug = ?").run(enabled ? 1 : 0, slug);
  } finally {
    db.close();
  }
}

function trackApi(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (req: Request) => {
    const u = new URL(req.url());
    if (u.pathname.startsWith("/api/")) seen.push(u.pathname);
  });
  return seen;
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-public-board-"));
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;
  server = spawn("go", ["run", "./cmd/scrumboy"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1",
      DATA_DIR: dataDir,
      BIND_ADDR: `127.0.0.1:${port}`,
      SCRUMBOY_TLS_CERT: path.join(dataDir, "no-cert"),
      SCRUMBOY_TLS_KEY: path.join(dataDir, "no-key"),
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  server.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });
  server.stdout.on("data", () => {});
  try {
    await waitForServer(baseUrl, 120_000);
  } catch (err) {
    throw new Error(`server failed to start: ${err}\nstderr:\n${stderr}`);
  }

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/bootstrap", OWNER);
  await owner.json("POST", "/api/admin/users", OUTSIDER);
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Public Ignite" });
  const hidden = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Private Scrumboy" });
  publicSlug = project.slug;
  privateSlug = hidden.slug;
  await owner.json("POST", `/api/board/${publicSlug}/todos`, {
    title: 'Readable <img src=x onerror="window.__publicXss=1">',
    body: 'Notes <img src=x onerror="window.__publicXss=2"> plain',
  });
  await owner.json("POST", `/api/board/${publicSlug}/todos`, { title: "Second story", columnKey: "done" });
  await owner.json("POST", `/api/board/${privateSlug}/todos`, { title: "Private secret story" });
  setPublication(publicSlug, true);
});

test.afterAll(async () => {
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        try {
          server?.kill("SIGKILL");
        } catch {
          /* ignore */
        }
        resolve();
      }, 10_000);
      server?.once("exit", () => {
        clearTimeout(t);
        resolve();
      });
    });
  }
  if (dataDir) await fs.promises.rm(dataDir, { recursive: true, force: true }).catch(() => {});
});

test("anonymous visitor gets a read-only public board with no member requests", async ({ page }) => {
  const api = trackApi(page);
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("[data-public-local-id]")).toHaveCount(2);
  await expect(page.locator("#newTodoBtn, #wallBtn, #archiveBtn, #manageMembersBtn, .card__drag-handle, [data-todo-id]")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText(OWNER.name);

  // Context menu on a card is not hijacked; no write menu appears.
  await page.locator("[data-public-local-id]").first().click({ button: "right" });
  await expect(page.locator("#contextMenu")).toBeHidden();
  await page.keyboard.press("Escape");
  await page.keyboard.press("n");
  await expect(page.locator("#todoDialog")).toBeHidden();

  // Only the single member-first probe and public endpoints are requested.
  const nonPublic = api.filter((p) => !p.startsWith(`/api/public/board/${publicSlug}`) && p !== "/api/auth/status");
  expect(nonPublic).toEqual([`/api/board/${publicSlug}`]);
});

test("story detail is read-only, inert, deep-linkable, and closes back to the board", async ({ page }) => {
  await page.goto(`${baseUrl}/${publicSlug}`);
  await page.locator("[data-public-local-id]").first().click();
  const dialog = page.locator("#publicTodoDialog");
  await expect(dialog).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/${publicSlug}/t/1$`));
  await expect(dialog.locator(".public-todo__notes")).toContainText("Notes <img");
  await expect(dialog.locator("img, input, textarea, form")).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __publicXss?: number }).__publicXss)).toBeUndefined();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`/${publicSlug}$`));

  await page.goto(`${baseUrl}/${publicSlug}/t/2`);
  await expect(page.locator("#publicTodoDialog")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#publicTodoDialogTitle")).toHaveText("#2 Second story");

  await page.goto(`${baseUrl}/${publicSlug}/t/999`);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#publicTodoDialog")).toBeHidden();
  await expect(page).toHaveURL(new RegExp(`/${publicSlug}$`));
});

test("signed-in nonmember receives the same public view; a member keeps the member board", async ({ browser }) => {
  const outsider = apiClient(baseUrl);
  await outsider.json("POST", "/api/auth/login", { email: OUTSIDER.email, password: OUTSIDER.password });
  const outsiderContext = await browser.newContext();
  await outsiderContext.addCookies(toBrowserCookies(outsider.cookies()));
  const outsiderPage = await outsiderContext.newPage();
  const outsiderApi = trackApi(outsiderPage);
  await outsiderPage.goto(`${baseUrl}/${publicSlug}`);
  await expect(outsiderPage.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(outsiderPage.locator("#newTodoBtn")).toHaveCount(0);
  expect(outsiderApi.some((p) => p.startsWith(`/api/board/${publicSlug}/`))).toBe(false);

  // The private project stays private for the same nonmember.
  await outsiderPage.goto(`${baseUrl}/${privateSlug}`);
  await expect(outsiderPage).toHaveURL(`${baseUrl}/`);
  await expect(outsiderPage.locator("body")).not.toContainText("Private secret story");
  await outsiderContext.close();

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  const ownerContext = await browser.newContext();
  await ownerContext.addCookies(toBrowserCookies(owner.cookies()));
  const ownerPage = await ownerContext.newPage();
  const ownerApi = trackApi(ownerPage);
  await ownerPage.goto(`${baseUrl}/${publicSlug}`);
  await expect(ownerPage.locator("#newTodoBtn")).toBeVisible({ timeout: 30_000 });
  await expect(ownerPage.locator(".public-board-badge")).toHaveCount(0);
  expect(ownerApi.some((p) => p.startsWith("/api/public/"))).toBe(false);
  await ownerContext.close();
});

test("private board is not rendered publicly to an anonymous visitor", async ({ page }) => {
  await page.goto(`${baseUrl}/${privateSlug}`);
  await expect(page.locator("#authForm")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".public-board-badge")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("Private secret story");
});

test("mobile touch navigation and both themes work", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator("#publicMobileTabs")).toBeVisible({ timeout: 30_000 });
  await page.locator('[data-public-tab="done"]').tap();
  await expect(page.locator('.col--mobile-active[data-public-column="done"]')).toBeVisible();
  await page.locator('.col--mobile-active [data-public-local-id="2"]').tap();
  await expect(page.locator("#publicTodoDialog")).toBeVisible();
  await page.locator("[data-public-todo-close]").tap();
  await expect(page.locator("#publicTodoDialog")).toBeHidden();

  const badgeColors = async () => page.locator(".public-board-badge").evaluate((el) => {
    const s = getComputedStyle(el);
    return { color: s.color, background: s.backgroundColor };
  });
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
  const dark = await badgeColors();
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
  const light = await badgeColors();
  expect(dark.color).not.toBe(dark.background);
  expect(light.color).not.toBe(light.background);
  expect(light.background).not.toBe(dark.background);
  await context.close();
});

test("a sprint created by a maintainer appears live in the public sprint filter", async ({ page }) => {
  const api = trackApi(page);
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#publicSprintFilter")).toHaveCount(0);

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  const start = Date.now();
  await owner.json("POST", `/api/board/${publicSlug}/sprints`, {
    name: "Live Sprint",
    plannedStartAt: start,
    plannedEndAt: start + 7 * 24 * 60 * 60 * 1000,
  });
  await expect(page.locator("#publicSprintFilter option", { hasText: "Live Sprint" })).toHaveCount(1, { timeout: 20_000 });
  expect(api.filter((p) => !p.startsWith(`/api/public/board/${publicSlug}`) && p !== "/api/auth/status")).toEqual([`/api/board/${publicSlug}`]);
});

test("a previously visited public route uses the cached app shell and reports live data unavailable", async ({ page, context }) => {
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
  await page.reload();
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });

  await context.setOffline(true);
  try {
    await page.goto(`${baseUrl}/${publicSlug}/t/1`, { waitUntil: "domcontentloaded" });
    await expect(page.locator("#app")).toHaveCount(1);
    await expect(page.locator("body")).toContainText("Failed to fetch");
    await expect(page.locator("[data-public-local-id], .public-board-badge")).toHaveCount(0);
    await expect(page.locator("body")).not.toContainText("Readable <img");
  } finally {
    await context.setOffline(false);
  }
});

test("live refresh arrives over the public stream and out-of-band revocation clears the board", async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator("[data-public-local-id]")).toHaveCount(2, { timeout: 30_000 });

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  await owner.json("POST", `/api/board/${publicSlug}/todos`, { title: "Arrived live" });
  await expect(page.locator(".card__title", { hasText: "Arrived live" })).toBeVisible({ timeout: 20_000 });

  // Direct SQL unpublish: detected by the server's 15s per-stream revalidation.
  setPublication(publicSlug, false);
  await expect(page.locator('[data-public-board-state="unavailable"]')).toBeVisible({ timeout: 45_000 });
  await expect(page.locator("[data-public-local-id]")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("Arrived live");
});

function toBrowserCookies(cookies: Cookie[]) {
  return cookies.map((c) => ({
    name: c.name,
    value: c.value,
    domain: "127.0.0.1",
    path: c.path || "/",
    httpOnly: c.httpOnly,
    secure: false,
    sameSite: "Lax" as const,
  }));
}

async function freePort(): Promise<number> {
  return await new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (!addr || typeof addr === "string") {
        srv.close();
        reject(new Error("failed to allocate port"));
        return;
      }
      const { port } = addr;
      srv.close((err) => (err ? reject(err) : resolve(port)));
    });
  });
}

async function waitForServer(url: string, timeoutMs: number): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    try {
      const res = await fetch(`${url}/api/auth/status`, { headers: { "X-Scrumboy": "1" } });
      if (res.ok) return;
    } catch {
      /* retry */
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`timeout waiting for ${url}`);
}

type Cookie = { name: string; value: string; path?: string; httpOnly?: boolean };

function apiClient(url: string) {
  const jar = new Map<string, Cookie>();

  function storeSetCookie(header: string | null) {
    if (!header) return;
    const parts = header.split(/,(?=\s*[^;=]+=[^;]+)/);
    for (const part of parts) {
      const [nv, ...attrs] = part.split(";").map((s) => s.trim());
      const eq = nv.indexOf("=");
      if (eq < 0) continue;
      const cookie: Cookie = { name: nv.slice(0, eq), value: nv.slice(eq + 1), path: "/", httpOnly: false };
      for (const a of attrs) {
        const lower = a.toLowerCase();
        if (lower.startsWith("path=")) cookie.path = a.slice(5);
        if (lower === "httponly") cookie.httpOnly = true;
      }
      jar.set(cookie.name, cookie);
    }
  }

  return {
    cookies: () => [...jar.values()],
    async json<T = unknown>(method: string, pathname: string, body?: unknown): Promise<T> {
      const res = await fetch(`${url}${pathname}`, {
        method,
        headers: {
          "Content-Type": "application/json",
          "X-Scrumboy": "1",
          Cookie: [...jar.values()].map((c) => `${c.name}=${c.value}`).join("; "),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const anyHeaders = res.headers as Headers & { getSetCookie?: () => string[] };
      if (typeof anyHeaders.getSetCookie === "function") {
        for (const c of anyHeaders.getSetCookie()) storeSetCookie(c);
      } else {
        storeSetCookie(res.headers.get("set-cookie"));
      }
      if (res.status === 204) return null as T;
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(`${method} ${pathname} -> ${res.status} ${JSON.stringify(data)}`);
      return data as T;
    },
  };
}
