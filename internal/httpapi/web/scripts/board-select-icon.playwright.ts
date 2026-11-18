/**
 * Responsive browser validation for the mobile-only board Select icon (Lucide
 * copy-check). Covers visibility and touch-target size on mobile, hiding on
 * desktop, the full select/deselect tap flow, accessible naming, viewport
 * crossing out of select mode, and desktop modifier clicks.
 *
 * Spawns a temporary full-mode Scrumboy server (go run) against a temp DATA_DIR.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const OWNER = { email: "board-select-icon-e2e@example.com", password: "password123", name: "Select Icon Owner" };
const TITLES = ["Icon story one", "Icon story two", "Icon story three"];

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let slug = "";

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-board-select-icon-"));
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
  server.stdout.on("data", () => {});
  try {
    await waitForServer(baseUrl, 120_000);
  } catch (err) {
    throw new Error(`server failed to start: ${err}\nstderr:\n${stderr}`);
  }

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Select Icon Board" });
  slug = project.slug;
  for (const title of TITLES) {
    await owner.json("POST", `/api/board/${slug}/todos`, { title });
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
  if (dataDir) await fs.promises.rm(dataDir, { recursive: true, force: true }).catch(() => {});
});

let ownerCookies: Cookie[] | null = null;

async function openBoard(browser: Browser, width: number): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({
    viewport: { width, height: 800 },
    isMobile: width <= 620,
    hasTouch: width < 768,
  });
  ownerCookies ??= (await ownerClient()).cookies();
  await context.addCookies(toBrowserCookies(ownerCookies));
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${slug}`);
  await expect(page.locator("#list_backlog [data-todo-id]")).toHaveCount(TITLES.length, { timeout: 30_000 });
  if (width <= 620) await expect(page.locator(".col--mobile-active #list_backlog")).toHaveCount(1);
  return { context, page };
}

function card(page: Page, title: string) {
  return page.locator(`#list_backlog [data-todo-id]`, { hasText: title }).first();
}

async function expectSelectMode(page: Page, on: boolean): Promise<void> {
  await expect(page.locator("#boardSelectToggle")).toHaveAttribute("aria-pressed", on ? "true" : "false");
  if (on) await expect(page.locator(".board.board--select-mode")).toHaveCount(1);
  else await expect(page.locator(".board.board--select-mode")).toHaveCount(0);
}

for (const width of [375, 620]) {
  test(`select icon at ${width}px: visible 40px target, full tap flow, accessible naming`, async ({ browser }) => {
    const { context, page } = await openBoard(browser, width);
    const toggle = page.locator("#boardSelectToggle");
    const icon = toggle.locator("svg.lucide-copy-check");

    await expect(toggle).toBeVisible();
    const box = (await toggle.boundingBox())!;
    expect(box.width).toBe(48);
    expect(box.height).toBe(48);
    await expect(icon).toBeVisible();
    await expect(toggle).toHaveAccessibleName("Select");
    await expectSelectMode(page, false);

    await toggle.click();
    await expect(icon).toBeVisible();
    await expect(toggle).toHaveAccessibleName("Done");
    await expectSelectMode(page, true);

    await card(page, TITLES[0]).click();
    await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);
    await expect(page.locator("#todoDialog")).toBeHidden();

    await card(page, TITLES[1]).click();
    await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);
    await expect(card(page, TITLES[1])).toHaveClass(/card--selected/);
    await expect(page.locator("#todoDialog")).toBeHidden();

    await toggle.click();
    await expect(icon).toBeVisible();
    await expect(toggle).toHaveAccessibleName("Select");
    await expectSelectMode(page, false);
    await expect(card(page, TITLES[0])).not.toHaveClass(/card--selected/);
    await expect(card(page, TITLES[1])).not.toHaveClass(/card--selected/);

    await card(page, TITLES[2]).click();
    await expect(page.locator("#todoDialog")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("#todoDialog")).toBeHidden();
    await context.close();
  });
}

for (const width of [375, 620]) {
test(`FAB stays anchored while selecting, scrolling, and showing dialogs at ${width}px`, async ({ browser }) => {
  const height = 800;
  const { context, page } = await openBoard(browser, width);
  const row = page.locator(".board-select-row");
  const toggle = page.locator("#boardSelectToggle");
  const bar = page.locator("#bulkEditBar");

  // Out of flow: fixed to the viewport corner with no residual row spacing.
  expect(await row.evaluate((el) => getComputedStyle(el).position)).toBe("fixed");
  expect(await row.evaluate((el) => getComputedStyle(el).marginBottom)).toBe("0px");
  const base = (await toggle.boundingBox())!;
  const sameBox = async () => {
    const box = (await toggle.boundingBox())!;
    expect(Math.abs(box.x - base.x)).toBeLessThanOrEqual(1);
    expect(Math.abs(box.y - base.y)).toBeLessThanOrEqual(1);
  };
  expect(base.x + base.width).toBe(width - 16);
  expect(base.y + base.height).toBe(height - 16);
  expect(await toggle.evaluate((el) => getComputedStyle(el).borderRadius)).toBe("50%");
  expect(await toggle.evaluate((el) => getComputedStyle(el).boxShadow)).not.toBe("none");
  expect(await toggle.evaluate((el) => getComputedStyle(el).backgroundColor)).not.toBe("rgba(0, 0, 0, 0)");

  // Fixed while the lane scrolls underneath.
  const list = page.locator(".col--mobile-active .col__list");
  await list.evaluate((el) => { el.scrollTop = 200; });
  await sameBox();

  // The FAB paints above cards: hit-testing its center reaches the toggle.
  const hitIdAt = ([x, y]: number[]) => page.evaluate(([px, py]) => {
    const el = document.elementFromPoint(px, py) as HTMLElement | null;
    return el?.closest("button")?.id ?? el?.id ?? "";
  }, [x, y]);
  const fabCenter = () => toggle.boundingBox().then((b) => [b!.x + b!.width / 2, b!.y + b!.height / 2]);
  expect(await hitIdAt(await fabCenter())).toBe("boardSelectToggle");

  // Entering select mode and selecting two cards never moves the FAB, and the
  // stationary toolbar leaves every visible bulk action reachable.
  await toggle.click();
  await sameBox();
  await card(page, TITLES[0]).click();
  await sameBox();
  await card(page, TITLES[1]).click();
  await sameBox();
  await expect(page.locator("#todoDialog")).toBeHidden();
  await expect(bar).toBeVisible();
  const barBox = (await bar.boundingBox())!;
  const fabBox = (await toggle.boundingBox())!;
  expect(fabBox.x + fabBox.width <= barBox.x + 1 || barBox.x + barBox.width <= fabBox.x + 1
    || fabBox.y + fabBox.height <= barBox.y + 1 || barBox.y + barBox.height <= fabBox.y + 1).toBe(true);
  expect(await hitIdAt(await fabCenter())).toBe("boardSelectToggle");
  for (const btn of await bar.locator("button:not([hidden])").all()) {
    const box = (await btn.boundingBox())!;
    expect(await hitIdAt([box.x + box.width / 2, box.y + box.height / 2])).toBe(await btn.getAttribute("id"));
  }

  // Clear removes the selection without moving the FAB; the FAB stays usable and exits select mode.
  await page.locator("#bulkClearBtn").click();
  await sameBox();
  await expect(card(page, TITLES[0])).not.toHaveClass(/card--selected/);
  await expect(toggle).toHaveAccessibleName("Done");
  await toggle.click();
  await expect(bar).toBeHidden();
  await sameBox();

  // A story dialog opens modally above the FAB's stacking level.
  await card(page, TITLES[2]).click();
  const dialog = page.locator("#todoDialog");
  await expect(dialog).toBeVisible();
  expect(await dialog.evaluate((el) => el.matches(":modal"))).toBe(true);
  expect(await row.evaluate((el) => getComputedStyle(el).zIndex)).toBe("40");
  await sameBox();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await context.close();
});
}

for (const width of [621, 768, 1280]) {
  test(`select row at ${width}px stays hidden on desktop`, async ({ browser }) => {
    const { context, page } = await openBoard(browser, width);
    const row = page.locator(".board-select-row");
    await expect(row).toBeHidden();
    await expect(row).toHaveCount(1);
    expect(await row.evaluate((el) => getComputedStyle(el).display)).toBe("none");
    expect(await row.boundingBox()).toBeNull();
    await expectSelectMode(page, false);
    await context.close();
  });
}

test("crossing from 620px to 621px while selecting exits select mode and keeps the selection", async ({ browser }) => {
  const { context, page } = await openBoard(browser, 620);
  const toggle = page.locator("#boardSelectToggle");
  await toggle.click();
  await expectSelectMode(page, true);
  await card(page, TITLES[0]).click();
  await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);

  await page.setViewportSize({ width: 621, height: 800 });
  await expect(page.locator(".board-select-row")).toBeHidden();
  await expectSelectMode(page, false);
  await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);

  await card(page, TITLES[1]).click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#todoDialog")).toBeHidden();

  await page.keyboard.press("Escape");
  await expect(card(page, TITLES[0])).not.toHaveClass(/card--selected/);
  await context.close();
});

test("desktop Ctrl-click and Shift-click selection still work at 1280px", async ({ browser }) => {
  const { context, page } = await openBoard(browser, 1280);
  await expectSelectMode(page, false);

  await card(page, TITLES[0]).click({ modifiers: ["Control"] });
  await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);
  await expect(page.locator("#todoDialog")).toBeHidden();

  await card(page, TITLES[2]).click({ modifiers: ["Shift"] });
  await expect(card(page, TITLES[0])).toHaveClass(/card--selected/);
  await expect(card(page, TITLES[2])).toHaveClass(/card--selected/);
  await expect(page.locator("#todoDialog")).toBeHidden();
  await context.close();
});

let ownerSession: Promise<ReturnType<typeof apiClient>> | null = null;

/** One shared owner login, so the suite stays under the server's login rate limit. */
function ownerClient(): Promise<ReturnType<typeof apiClient>> {
  ownerSession ??= (async () => {
    const owner = apiClient(baseUrl);
    await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
    return owner;
  })();
  return ownerSession;
}

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
