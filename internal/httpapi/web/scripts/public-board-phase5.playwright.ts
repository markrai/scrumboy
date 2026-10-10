/**
 * End-to-end: Phase 5 publication management, sign-in return, and the Full
 * Mode landing/workspace split. Spawns a temporary Full Mode server with both
 * SCRUMBOY_PUBLIC_PROJECTS_ENABLED and SCRUMBOY_LANDING_PAGE_ENABLED.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const OWNER = { email: "phase5-owner@example.com", password: "password123", name: "Phase Five Owner" };
const OUTSIDER = { email: "phase5-outsider@example.com", password: "password123", name: "Outsider" };

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let slug = "";

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-phase5-"));
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;
  server = spawn("go", ["run", "./cmd/scrumboy"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1",
      SCRUMBOY_LANDING_PAGE_ENABLED: "1",
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
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Phase Five Board" });
  slug = project.slug;
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Deep linked story" });
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

async function signIn(page: Page, account: { email: string; password: string }): Promise<void> {
  await expect(page.locator("#authForm")).toBeVisible({ timeout: 30_000 });
  await page.locator("#authEmail").fill(account.email);
  await page.locator("#authPassword").fill(account.password);
  await page.locator("#loginBtn").click();
}

async function signedInContext(context: BrowserContext, account: { email: string; password: string }): Promise<void> {
  const client = apiClient(baseUrl);
  await client.json("POST", "/api/auth/login", { email: account.email, password: account.password });
  await context.addCookies(client.cookies().map((c) => ({
    name: c.name, value: c.value, domain: "127.0.0.1", path: c.path || "/", httpOnly: c.httpOnly, secure: false, sameSite: "Lax" as const,
  })));
}

test("landing at / links to the /_app workspace, which shows sign-in without a loop", async ({ page }) => {
  await page.goto(`${baseUrl}/`);
  const workspaceLink = page.locator('a[data-workspace-entry][href="/_app"]');
  await expect(workspaceLink).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#authForm")).toHaveCount(0);
  await workspaceLink.click();
  await expect(page).toHaveURL(`${baseUrl}/_app`);
  await signIn(page, OWNER);
  await expect(page).toHaveURL(new RegExp(`/_app(\\?.*)?$`), { timeout: 30_000 });
  await expect(page.locator(`[data-open="${slug}"]`).first()).toBeVisible({ timeout: 30_000 });
});

test("a private board is not public before publication", async ({ page }) => {
  await page.goto(`${baseUrl}/${slug}`);
  await expect(page.locator("#authForm")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".public-board-badge")).toHaveCount(0);
});

test("a Maintainer publishes from Settings → Sharing, copies the link, and visitors can read it", async ({ browser }) => {
  const context = await browser.newContext({ permissions: ["clipboard-read", "clipboard-write"] });
  await signedInContext(context, OWNER);
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${slug}`);
  await expect(page.locator("#newTodoBtn")).toBeVisible({ timeout: 30_000 });
  await page.keyboard.press("Shift+KeyS");
  await page.locator('.settings-tab[data-tab="sharing"]').click();
  await expect(page.locator('[data-sharing-state="private"]')).toBeVisible();
  await page.locator('[data-sharing-toggle="publish"]').click();
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator('[data-sharing-state="public"]')).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#sharingPublicUrl")).toHaveValue(`${baseUrl}/${slug}`);
  await page.locator("[data-sharing-copy]").click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`${baseUrl}/${slug}`);

  const visitor = await browser.newPage();
  await visitor.goto(`${baseUrl}/${slug}`);
  await expect(visitor.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await visitor.close();
  await context.close();
});

test("a visitor signs in from a public deep link and returns to it with ordinary resolution", async ({ browser }) => {
  // Member: lands back on the same story with the editable member board.
  const memberPage = await browser.newPage();
  await memberPage.goto(`${baseUrl}/${slug}/t/1?tag=none`);
  await expect(memberPage.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  // The story view is open on a deep link; Sign In is offered inside it.
  await expect(memberPage.locator("#publicTodoDialog")).toBeVisible();
  await memberPage.locator("[data-public-todo-sign-in]").click();
  await expect(memberPage).toHaveURL(new RegExp(`/auth/login\\?next=`));
  await signIn(memberPage, OWNER);
  await expect(memberPage).toHaveURL(new RegExp(`/${slug}/t/1\\?tag=none`), { timeout: 30_000 });
  await expect(memberPage.locator("#newTodoBtn")).toBeVisible({ timeout: 30_000 });
  await expect(memberPage.locator(".public-board-badge")).toHaveCount(0);
  await memberPage.close();

  // Nonmember: returns to the same story, still read-only.
  const outsiderPage = await browser.newPage();
  await outsiderPage.goto(`${baseUrl}/${slug}`);
  await expect(outsiderPage.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await outsiderPage.locator("[data-public-local-id]").first().click();
  await expect(outsiderPage).toHaveURL(new RegExp(`/${slug}/t/1`));
  await outsiderPage.locator("[data-public-todo-sign-in]").click();
  await signIn(outsiderPage, OUTSIDER);
  await expect(outsiderPage).toHaveURL(new RegExp(`/${slug}/t/1`), { timeout: 30_000 });
  await expect(outsiderPage.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(outsiderPage.locator("#publicTodoDialog")).toBeVisible();
  await expect(outsiderPage.locator("#newTodoBtn, #publicSignInBtn")).toHaveCount(0);
  await expect(outsiderPage.locator("[data-public-todo-sign-in]")).toBeHidden();
  await outsiderPage.close();
});

test("unsafe sign-in return destinations stay same-origin", async ({ page }) => {
  await page.goto(`${baseUrl}/auth/login?next=${encodeURIComponent("//evil.example/steal")}`);
  await signIn(page, OUTSIDER);
  await page.waitForURL((url) => url.origin === baseUrl && !url.pathname.startsWith("/auth/login"), { timeout: 30_000 });
  expect(new URL(page.url()).origin).toBe(baseUrl);
});

test("unpublishing from Sharing disconnects a live public reader immediately", async ({ browser }) => {
  const visitor = await browser.newPage();
  await visitor.goto(`${baseUrl}/${slug}`);
  await expect(visitor.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });

  const context = await browser.newContext();
  await signedInContext(context, OWNER);
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${slug}`);
  await expect(page.locator("#newTodoBtn")).toBeVisible({ timeout: 30_000 });
  await page.keyboard.press("Shift+KeyS");
  await page.locator('.settings-tab[data-tab="sharing"]').click();
  await page.locator('[data-sharing-toggle="unpublish"]').click();
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator('[data-sharing-state="private"]')).toBeVisible({ timeout: 15_000 });

  await expect(visitor.locator('[data-public-board-state="unavailable"]')).toBeVisible({ timeout: 10_000 });
  await expect(visitor.locator("[data-public-local-id]")).toHaveCount(0);
  await visitor.reload();
  await expect(visitor.locator("#authForm")).toBeVisible({ timeout: 30_000 });
  await context.close();
});

test("Contributors do not see the Sharing tab", async ({ browser }) => {
  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  const users = await owner.json<Array<{ id: number; email: string }>>("GET", "/api/admin/users");
  const outsider = users.find((u) => u.email === OUTSIDER.email)!;
  const projects = await owner.json<Array<{ id: number; slug: string }>>("GET", "/api/projects");
  const project = projects.find((p) => p.slug === slug)!;
  await owner.json("POST", `/api/projects/${project.id}/members`, { user_id: outsider.id, role: "contributor" });

  const context = await browser.newContext();
  await signedInContext(context, OUTSIDER);
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${slug}`);
  await expect(page.locator(".col__list").first()).toBeVisible({ timeout: 30_000 });
  await page.keyboard.press("Shift+KeyS");
  await expect(page.locator("#settingsDialog")).toBeVisible();
  await expect(page.locator('.settings-tab[data-tab="sharing"]')).toHaveCount(0);
  await context.close();
});

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
    for (const part of header.split(/,(?=\s*[^;=]+=[^;]+)/)) {
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
        headers: { "Content-Type": "application/json", "X-Scrumboy": "1", Cookie: [...jar.values()].map((c) => `${c.name}=${c.value}`).join("; ") },
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
