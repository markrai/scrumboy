/**
 * Real-browser regression: stale over-limit title validity must not block
 * saving another story after the dialog is closed and reused.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { expect, test } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const EMAIL = "todo-codepoint-e2e@example.com";
const PASSWORD = "password123";
const NAME = "Codepoint E2E";

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-todo-codepoint-"));
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;

  server = spawn("go", ["run", "./cmd/scrumboy"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
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

  try {
    await waitForServer(baseUrl, 90_000);
    const api = await apiClient(baseUrl);
    await api.json("POST", "/api/auth/bootstrap", { name: NAME, email: EMAIL, password: PASSWORD });
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

test("discarding an over-limit title does not block saving another story", async ({ page, context }) => {
  test.setTimeout(180_000);

  const api = await apiClient(baseUrl);
  await api.json("POST", "/api/auth/login", { email: EMAIL, password: PASSWORD });
  const project = await api.json<{ slug: string }>("POST", "/api/projects", { name: "Codepoint Dialog Board" });
  const created = await api.json<{ localId: number; title: string }>("POST", `/api/board/${project.slug}/todos`, {
    title: "Valid story title",
  });

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

  await page.locator("#newTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  const overLimit = "x".repeat(201);
  await page.locator("#todoTitle").fill(overLimit);
  await expect
    .poll(async () => page.locator("#todoTitle").evaluate((el: HTMLInputElement) => el.validationMessage))
    .toMatch(/200|characters/i);

  await page.locator("#closeTodoBtn").click();
  await expect(page.locator("#confirmDialogConfirm")).toBeVisible({ timeout: 10_000 });
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator("#todoDialog")).toBeHidden({ timeout: 15_000 });

  await page.locator(`.card[data-todo-local-id="${created.localId}"]`).click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  await expect(page.locator("#todoTitle")).toHaveValue("Valid story title");
  await expect
    .poll(async () => page.locator("#todoTitle").evaluate((el: HTMLInputElement) => el.validationMessage))
    .toBe("");

  await page.locator("#todoTags").fill("reuse-ok");
  await page.locator("#todoTags").press("Enter");
  const save = page.waitForResponse(
    (response) =>
      response.request().method() === "PATCH" &&
      new URL(response.url()).pathname === `/api/board/${project.slug}/todos/${created.localId}`,
  );
  await page.locator("#saveTodoBtn").click();
  expect((await save).status()).toBe(200);
  await expect(page.locator("#todoDialog")).toBeHidden();
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
