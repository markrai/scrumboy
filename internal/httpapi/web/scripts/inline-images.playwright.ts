/**
 * End-to-end: inline images in story notes.
 *
 * Builds the server once and runs three instances: inline images on (main), on with a 1 MiB
 * project budget, and off. Covers picker and drop uploads, the save lock, rendering in the member
 * and public dialogs, public eligibility, archive export/import and the flag-off baseline.
 */
import { spawn, execFileSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import { DatabaseSync } from "node:sqlite";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");
const OWNER = { email: "images-owner-e2e@example.com", password: "password123", name: "Image Owner" };
const VIEWER = { email: "images-viewer-e2e@example.com", password: "password123", name: "Image Viewer" };
const OUTSIDER = { email: "images-outsider-e2e@example.com", password: "password123", name: "Image Outsider" };

type Cookie = { name: string; value: string; path?: string; httpOnly?: boolean };
type Instance = { proc: ChildProcessWithoutNullStreams; url: string; dataDir: string };

let binary = "";
let tmpRoot = "";
const instances: Instance[] = [];
let main: Instance;
let budget: Instance;
let off: Instance;
let owner: ReturnType<typeof apiClient>;
let slug = "";

// ---- image fixtures ----

function crc32(buf: Buffer): number {
  let c = ~0;
  for (const b of buf) {
    c ^= b;
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const td = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(td));
  return Buffer.concat([len, td, crc]);
}

/** A valid RGB PNG; seed changes the pixels; pad adds a private ancillary chunk of that size. */
function makePng(w: number, h: number, seed: number, pad = 0): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w * 3; x++) raw[y * (w * 3 + 1) + 1 + x] = (x * 7 + y * 13 + seed * 31) & 0xff;
  }
  const parts = [Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw))];
  if (pad > 0) parts.push(chunk("prVt", crypto.randomBytes(pad)));
  parts.push(chunk("IEND", Buffer.alloc(0)));
  return Buffer.concat(parts);
}

const GIF_1X1 = Buffer.from("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7", "base64");

// ---- server helpers ----

async function startInstance(env: Record<string, string>): Promise<Instance> {
  const dataDir = await fs.promises.mkdtemp(path.join(tmpRoot, "data-"));
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const proc = spawn(binary, [], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1",
      SCRUMBOY_MARKDOWN_NOTES_ENABLED: "1",
      SCRUMBOY_TRUST_PROXY: "1",
      DATA_DIR: dataDir,
      BIND_ADDR: `127.0.0.1:${port}`,
      SCRUMBOY_TLS_CERT: path.join(dataDir, "no-cert"),
      SCRUMBOY_TLS_KEY: path.join(dataDir, "no-key"),
      ...env,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  proc.stderr.on("data", (c) => (stderr += String(c)));
  proc.stdout.on("data", () => {});
  const inst = { proc, url, dataDir };
  instances.push(inst);
  try {
    await waitForServer(url, 120_000);
  } catch (err) {
    throw new Error(`server failed to start: ${err}\n${stderr}`);
  }
  return inst;
}

function setPublication(inst: Instance, projectSlug: string, enabled: boolean): void {
  const db = new DatabaseSync(path.join(inst.dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.prepare("UPDATE projects SET public_view_enabled = ? WHERE slug = ?").run(enabled ? 1 : 0, projectSlug);
  } finally {
    db.close();
  }
}

async function uploadViaApi(inst: Instance, client: ReturnType<typeof apiClient>, projectSlug: string, name: string, bytes: Buffer, query = ""): Promise<{ status: number; body: any }> {
  const form = new FormData();
  form.append("file", new Blob([bytes], { type: "image/png" }), name);
  const res = await fetch(`${inst.url}/api/board/${projectSlug}/assets${query}`, {
    method: "POST",
    headers: { "X-Scrumboy": "1", Cookie: client.cookieHeader() },
    body: form,
  });
  return { status: res.status, body: await res.json().catch(() => null) };
}

async function signedInContext(page: Page, client: ReturnType<typeof apiClient>): Promise<BrowserContext> {
  const context = page.context();
  await context.addCookies(toBrowserCookies(client.cookies()));
  return context;
}

async function openStory(page: Page, inst: Instance, projectSlug: string, title: string): Promise<void> {
  await page.goto(`${inst.url}/${projectSlug}`);
  await page.locator(".card[data-todo-local-id]", { hasText: title }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  test.setTimeout(300_000);
  tmpRoot = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-images-"));
  binary = path.join(tmpRoot, process.platform === "win32" ? "scrumboy.exe" : "scrumboy");
  execFileSync("go", ["build", "-o", binary, "./cmd/scrumboy"], { cwd: repoRoot, stdio: "inherit" });
  [main, budget, off] = await Promise.all([
    startInstance({ SCRUMBOY_INLINE_IMAGES_ENABLED: "1" }),
    startInstance({ SCRUMBOY_INLINE_IMAGES_ENABLED: "1", SCRUMBOY_ASSET_PROJECT_BUDGET_MB: "1" }),
    startInstance({ SCRUMBOY_INLINE_IMAGES_ENABLED: "0" }),
  ]);
  owner = apiClient(main.url);
  await owner.json("POST", "/api/auth/bootstrap", OWNER);
  const viewerUser = await owner.json<{ id: number }>("POST", "/api/admin/users", VIEWER);
  await owner.json("POST", "/api/admin/users", OUTSIDER);
  const project = await owner.json<{ slug: string; id: number }>("POST", "/api/projects", { name: "Image Board" });
  slug = project.slug;
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Screenshot story" });
  await owner.json("POST", `/api/projects/${project.id}/members`, { user_id: viewerUser.id, role: "viewer" });
});

test.afterAll(async () => {
  for (const inst of instances) {
    inst.proc.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        try {
          inst.proc.kill("SIGKILL");
        } catch {
          /* ignore */
        }
        resolve();
      }, 10_000);
      inst.proc.once("exit", () => {
        clearTimeout(t);
        resolve();
      });
    });
  }
  if (tmpRoot) await fs.promises.rm(tmpRoot, { recursive: true, force: true }).catch(() => {});
});

test("a. picker upload locks save, inserts canonical markdown and renders after reopen", async ({ page }) => {
  await signedInContext(page, owner);
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/board/*/assets*", async (route) => {
    await gate;
    await route.continue();
  });
  await openStory(page, main, slug, "Screenshot story");
  await expect(page.locator("#todoBodyInsertImage")).toBeVisible();
  await page.locator("#todoBodyImageInput").setInputFiles({ name: "screen.png", mimeType: "image/png", buffer: makePng(32, 24, 1) });
  await expect(page.locator("#todoBody")).toHaveValue(/scrumboy-upload:/);
  await expect(page.locator("#saveTodoBtn")).toBeDisabled();
  release();
  await expect(page.locator("#todoBody")).toHaveValue(/!\[screen\]\(\/api\/assets\/[0-9a-f-]{36}\)/);
  await expect(page.locator("#saveTodoBtn")).toBeEnabled();
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  await page.unroute("**/api/board/*/assets*");

  await openStory(page, main, slug, "Screenshot story");
  await page.locator("#todoBodyPreviewTab").click();
  const img = page.locator("#todoBodyPreview img");
  await expect(img).toHaveCount(1);
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(32);
});

test("b. dropping an image file inserts markdown", async ({ page }) => {
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Screenshot story");
  const b64 = makePng(10, 10, 2).toString("base64");
  await page.locator("#todoBody").evaluate((el, data) => {
    const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "dropped.png", { type: "image/png" }));
    el.dispatchEvent(new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }));
  }, b64);
  await expect(page.locator("#todoBody")).toHaveValue(/!\[dropped\]\(\/api\/assets\//);
});

test("c. a GIF is served byte for byte", async () => {
  const res = await uploadViaApi(main, owner, slug, "tiny.gif", GIF_1X1);
  expect(res.status).toBe(201);
  const served = await fetch(`${main.url}${res.body.url}`, { headers: { Cookie: owner.cookieHeader() } });
  expect(served.headers.get("content-type")).toBe("image/gif");
  expect(Buffer.from(await served.arrayBuffer()).equals(GIF_1X1)).toBe(true);
});

test("d/f. public visibility follows publication and canonical embedding", async ({ browser }) => {
  const embedded = await uploadViaApi(main, owner, slug, "pub.png", makePng(8, 8, 3));
  const codeOnly = await uploadViaApi(main, owner, slug, "code.png", makePng(8, 8, 4));
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Public image story", body: `![pub](${embedded.body.url})` });
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Code mention", body: "```\n![c](" + codeOnly.body.url + ")\n```" });
  setPublication(main, slug, true);

  const anon = await browser.newContext();
  const outsider = apiClient(main.url);
  await outsider.json("POST", "/api/auth/login", { email: OUTSIDER.email, password: OUTSIDER.password });
  const signedOut = await browser.newContext();
  await signedOut.addCookies(toBrowserCookies(outsider.cookies()));
  for (const context of [anon, signedOut]) {
    const page = await context.newPage();
    await page.goto(`${main.url}/${slug}`);
    await page.locator("[data-public-local-id]", { hasText: "Public image story" }).click();
    const img = page.locator("#publicTodoDialog .public-todo__notes img");
    await expect(img).toHaveCount(1);
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(8);
    expect((await fetch(`${main.url}${codeOnly.body.url}`)).status).toBe(404);
  }
  setPublication(main, slug, false);
  expect((await fetch(`${main.url}${embedded.body.url}`)).status).toBe(404);
  await anon.close();
  await signedOut.close();
});

test("e. a viewer sees images but cannot upload", async ({ page }) => {
  const viewer = apiClient(main.url);
  await viewer.json("POST", "/api/auth/login", { email: VIEWER.email, password: VIEWER.password });
  await signedInContext(page, viewer);
  await openStory(page, main, slug, "Public image story");
  await expect(page.locator("#todoBodyInsertImage")).toBeHidden();
  const res = await uploadViaApi(main, viewer, slug, "x.png", makePng(4, 4, 5));
  expect([403, 404]).toContain(res.status);
});

test("g/k. export .sbz, delete the project, import a copy: images restored, code text kept", async () => {
  const embedded = await uploadViaApi(main, owner, slug, "keep.png", makePng(12, 12, 10));
  await owner.json("POST", `/api/board/${slug}/todos`, {
    title: "Mixed references",
    body: "![keep](" + embedded.body.url + ")\n\n```\n" + embedded.body.url + "\n```",
  });
  const exportRes = await fetch(`${main.url}/api/backup/export`, { headers: { "X-Scrumboy": "1", Cookie: owner.cookieHeader() } });
  expect(exportRes.headers.get("content-type")).toBe("application/zip");
  const archive = Buffer.from(await exportRes.arrayBuffer());

  const projects = await owner.json<any[]>("GET", "/api/projects");
  const source = projects.find((p: any) => p.slug === slug);
  await owner.json("DELETE", `/api/projects/${source.id}`);
  expect((await fetch(`${main.url}${embedded.body.url}`, { headers: { Cookie: owner.cookieHeader() } })).status).toBe(404);

  const form = new FormData();
  form.append("importMode", "copy");
  form.append("archive", new Blob([archive]), "backup.sbz");
  const res = await fetch(`${main.url}/api/backup/import`, { method: "POST", headers: { "X-Scrumboy": "1", Cookie: owner.cookieHeader() }, body: form });
  expect(res.status).toBe(200);

  const after = await owner.json<any[]>("GET", "/api/projects");
  const copy = after.find((p: any) => String(p.name).startsWith("Image Board"));
  expect(copy).toBeTruthy();
  slug = copy.slug;
  const board = await owner.json<any>("GET", `/api/board/${copy.slug}`);
  const stories = Object.values(board.columns as Record<string, any[]>).flat();
  const mixed = stories.find((t) => t.title === "Mixed references");
  const match = /^!\[keep\]\(\/api\/assets\/([0-9a-f-]{36})\)/.exec(mixed.body);
  expect(match).toBeTruthy();
  expect(match![1]).not.toBe(embedded.body.assetId);
  const served = await fetch(`${main.url}/api/assets/${match![1]}`, { headers: { Cookie: owner.cookieHeader() } });
  expect(served.status).toBe(200);
  // The fenced mention still names the original id: only image destinations are remapped.
  expect(mixed.body).toContain("```\n" + embedded.body.url + "\n```");
});

test("i. closing the dialog during an upload aborts it", async ({ page }) => {
  await signedInContext(page, owner);
  await page.route("**/api/board/*/assets*", () => new Promise(() => {}));
  await openStory(page, main, slug, "Screenshot story");
  await page.locator("#todoBodyImageInput").setInputFiles({ name: "slow.png", mimeType: "image/png", buffer: makePng(4, 4, 6) });
  await expect(page.locator("#todoBody")).toHaveValue(/scrumboy-upload:/);
  await page.evaluate(() => (document.getElementById("todoDialog") as HTMLDialogElement).close());
  await page.unroute("**/api/board/*/assets*");
  await openStory(page, main, slug, "Screenshot story");
  await expect(page.locator("#todoBody")).not.toHaveValue(/scrumboy-upload:/);
});

test("j. the storage budget rejects uploads past the limit", async ({ page }) => {
  const client = apiClient(budget.url);
  await client.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await client.json<{ slug: string }>("POST", "/api/projects", { name: "Budget Board" });
  await client.json("POST", `/api/board/${project.slug}/todos`, { title: "Budget story" });
  expect((await uploadViaApi(budget, client, project.slug, "a.png", makePng(16, 16, 7, 700_000))).status).toBe(201);
  await signedInContext(page, client);
  await openStory(page, budget, project.slug, "Budget story");
  await page.locator("#todoBodyImageInput").setInputFiles({ name: "b.png", mimeType: "image/png", buffer: makePng(16, 16, 8, 700_000) });
  await expect(page.locator("#todoBody")).not.toHaveValue(/scrumboy-upload:|\/api\/assets\//);
  const res = await uploadViaApi(budget, client, project.slug, "c.png", makePng(16, 16, 9, 700_000));
  expect(res.status).toBe(507);
});

test("h. with the flag off, image syntax stays text and there is no insert button", async ({ page }) => {
  const client = apiClient(off.url);
  await client.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await client.json<{ slug: string }>("POST", "/api/projects", { name: "Plain Board" });
  await client.json("POST", `/api/board/${project.slug}/todos`, { title: "Plain story", body: "![x](/api/assets/11111111-1111-4111-8111-111111111111)" });
  const status = await (await fetch(`${off.url}/api/auth/status`)).json();
  expect(status.inlineImagesEnabled).toBe(false);
  await signedInContext(page, client);
  await openStory(page, off, project.slug, "Plain story");
  await expect(page.locator("#todoBodyInsertImage")).toBeHidden();
  await page.locator("#todoBodyPreviewTab").click();
  await expect(page.locator("#todoBodyPreview img")).toHaveCount(0);
  await expect(page.locator("#todoBodyPreview")).toContainText("![x](/api/assets/");
});

// ---- shared helpers (same as public-board.playwright.ts) ----

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
  const cookieHeader = () => [...jar.values()].map((c) => `${c.name}=${c.value}`).join("; ");
  return {
    cookies: () => [...jar.values()],
    cookieHeader,
    async json<T = unknown>(method: string, pathname: string, body?: unknown): Promise<T> {
      const res = await fetch(`${url}${pathname}`, {
        method,
        headers: { "Content-Type": "application/json", "X-Scrumboy": "1", Cookie: cookieHeader() },
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
