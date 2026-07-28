/**
 * End-to-end: cards-per-lane preference drives initial board page size;
 * "Load more" remains available when a lane still has more cards.
 *
 * Spawns a temporary full-mode Scrumboy server (go run) against a temp DATA_DIR.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const EMAIL = "cards-per-lane-e2e@example.com";
const PASSWORD = "password123";
const NAME = "Cards E2E";

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-cards-per-lane-"));
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;

  server = spawn("go", ["run", "./cmd/scrumboy"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      DATA_DIR: dataDir,
      BIND_ADDR: `127.0.0.1:${port}`,
      // Force HTTP even if repo-root cert.pem/key.pem exist from local HTTPS setups.
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
    await waitForServer(baseUrl, 90_000);
  } catch (err) {
    throw new Error(`server failed to start: ${err}\nstderr:\n${stderr}`);
  }
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
  if (dataDir) {
    await fs.promises.rm(dataDir, { recursive: true, force: true }).catch(() => {});
  }
});

test("cards-per-lane preference changes default page size; Load more still works", async ({ page, context }) => {
  test.setTimeout(120_000);

  const api = await apiClient(baseUrl);
  await api.json("POST", "/api/auth/bootstrap", { name: NAME, email: EMAIL, password: PASSWORD });
  const project = await api.json<{ slug: string }>("POST", "/api/projects", { name: "Lane Size Board" });
  for (let i = 1; i <= 35; i++) {
    await api.json("POST", `/api/board/${project.slug}/todos`, { title: `Card ${i}` });
  }

  const cookies = api.cookies();
  await context.addCookies(
    cookies.map((c) => ({
      name: c.name,
      value: c.value,
      domain: "127.0.0.1",
      path: c.path || "/",
      httpOnly: c.httpOnly,
      secure: false,
      sameSite: "Lax" as const,
    })),
  );

  await page.goto(`${baseUrl}/${project.slug}`);
  await expect(page.locator(".col__list").first()).toBeVisible({ timeout: 30_000 });

  const backlogList = page.locator('.col[data-column="backlog"] .col__list');
  await expect.poll(async () => backlogList.locator("[data-todo-local-id]").count()).toBe(20);
  await expect(page.locator('.col[data-column="backlog"] [data-load-more="backlog"]')).toBeVisible();

  await page.keyboard.press("Shift+KeyS");
  await expect(page.locator("#settingsDialog")).toBeVisible();
  await page.locator('.settings-tab[data-tab="customization"]').click();
  const select = page.locator("#cardsPerLaneSelect");
  await expect(select).toBeVisible();

  const reloadWith50 = page.waitForRequest(
    (req) => {
      try {
        const u = new URL(req.url());
        return u.pathname === `/api/board/${project.slug}` && u.searchParams.get("limitPerLane") === "50";
      } catch {
        return false;
      }
    },
    { timeout: 30_000 },
  );
  await select.selectOption("50");
  await reloadWith50;

  await expect.poll(async () => backlogList.locator("[data-todo-local-id]").count()).toBe(35);
  await expect(page.locator('.col[data-column="backlog"] [data-load-more="backlog"]')).toHaveCount(0);

  // Preference survives a full reload.
  await page.reload();
  await expect(page.locator(".col__list").first()).toBeVisible({ timeout: 30_000 });
  await expect.poll(async () => backlogList.locator("[data-todo-local-id]").count()).toBe(35);

  // Drop back to 20: Load more returns and appends more cards.
  await page.keyboard.press("Shift+KeyS");
  await expect(page.locator("#settingsDialog")).toBeVisible();
  await page.locator('.settings-tab[data-tab="customization"]').click();
  const reloadWith20 = page.waitForRequest(
    (req) => {
      try {
        const u = new URL(req.url());
        return u.pathname === `/api/board/${project.slug}` && u.searchParams.get("limitPerLane") === "20";
      } catch {
        return false;
      }
    },
    { timeout: 30_000 },
  );
  await page.locator("#cardsPerLaneSelect").selectOption("20");
  await reloadWith20;
  await expect.poll(async () => backlogList.locator("[data-todo-local-id]").count()).toBe(20);
  await expect(page.locator('.col[data-column="backlog"] [data-load-more="backlog"]')).toBeVisible();

  await page.locator("#closeSettingsBtn").click();
  await expect(page.locator("#settingsDialog")).toBeHidden();

  await page.locator('.col[data-column="backlog"] [data-load-more="backlog"] .col__load-more--desktop').click();
  await expect.poll(async () => backlogList.locator("[data-todo-local-id]").count()).toBeGreaterThan(20);
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
    const parts = header.split(/,(?=\s*[^;=]+=[^;]+)/);
    for (const part of parts) {
      const [nv, ...attrs] = part.split(";").map((s) => s.trim());
      const eq = nv.indexOf("=");
      if (eq < 0) continue;
      const name = nv.slice(0, eq);
      const value = nv.slice(eq + 1);
      const cookie: Cookie = { name, value, path: "/", httpOnly: false };
      for (const a of attrs) {
        const lower = a.toLowerCase();
        if (lower.startsWith("path=")) cookie.path = a.slice(5);
        if (lower === "httponly") cookie.httpOnly = true;
      }
      jar.set(name, cookie);
    }
  }

  function cookieHeader(): string {
    return [...jar.values()].map((c) => `${c.name}=${c.value}`).join("; ");
  }

  return {
    cookies: () => [...jar.values()],
    async json<T = unknown>(method: string, pathname: string, body?: unknown): Promise<T> {
      const res = await fetch(`${url}${pathname}`, {
        method,
        headers: {
          "Content-Type": "application/json",
          "X-Scrumboy": "1",
          Cookie: cookieHeader(),
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
      if (!res.ok) {
        throw new Error(`${method} ${pathname} -> ${res.status} ${JSON.stringify(data)}`);
      }
      return data as T;
    },
  };
}
