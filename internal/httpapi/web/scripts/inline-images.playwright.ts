/**
 * End-to-end: inline images in story notes.
 *
 * Builds the server once and runs four instances: built-in defaults (main, all notes features on),
 * images on with a 1 MiB project budget, images off, and Markdown off. Covers the visual notes
 * editor (clipboard paste and drop, images shown in place, initial sizing, selection, mouse,
 * keyboard and touch resizing, deletion, undo/redo), the save lock, rendering in the member and
 * public dialogs, public eligibility, archive export/import with widths, the flag-off baseline
 * and the default/opt-out capability matrix.
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
let markdownOff: Instance;
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
      // Notes features run on their built-in defaults (empty = unset) unless a test overrides them.
      SCRUMBOY_MARKDOWN_NOTES_ENABLED: "",
      SCRUMBOY_MERMAID_NOTES_ENABLED: "",
      SCRUMBOY_INLINE_IMAGES_ENABLED: "",
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
  [main, budget, off, markdownOff] = await Promise.all([
    startInstance({}),
    startInstance({ SCRUMBOY_INLINE_IMAGES_ENABLED: "1", SCRUMBOY_ASSET_PROJECT_BUDGET_MB: "1" }),
    startInstance({ SCRUMBOY_INLINE_IMAGES_ENABLED: "0" }),
    startInstance({ SCRUMBOY_MARKDOWN_NOTES_ENABLED: "0", SCRUMBOY_MERMAID_NOTES_ENABLED: "1", SCRUMBOY_INLINE_IMAGES_ENABLED: "1" }),
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

// ---- visual editor helpers ----

const editorSel = "#todoBodyEditor .cm-editor";
const widgetSel = "#todoBodyEditor .todo-notes-image[data-path]";

/** The editor's note text (lines joined with "\n"), read from CodeMirror's state. */
async function editorText(page: Page): Promise<string> {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    return (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(el).state.doc.toString();
  }, editorSel);
}

async function placeCaret(page: Page, pos: number): Promise<void> {
  await page.evaluate(([sel, at]) => {
    const view = (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(document.querySelector(sel as string));
    view.focus();
    view.dispatch({ selection: { anchor: at as number } });
  }, [editorSel, pos] as const);
}

type FileSpec = { name: string; b64: string; type?: string };
const pngFile = (name: string, png: Buffer): FileSpec => ({ name, b64: png.toString("base64") });

/** Dispatches a paste (or a drop at the given point) of files on the editor content. */
async function sendFiles(page: Page, kind: "paste" | "drop", files: FileSpec[], at?: { x: number; y: number }): Promise<void> {
  await page.locator("#todoBodyEditor .cm-content").evaluate(
    (el, { kind, files, at }) => {
      const dt = new DataTransfer();
      for (const f of files) {
        const bytes = Uint8Array.from(atob(f.b64), (c) => c.charCodeAt(0));
        dt.items.add(new File([bytes], f.name, { type: f.type ?? "image/png" }));
      }
      const event =
        kind === "paste"
          ? new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true })
          : new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true, clientX: at?.x ?? 0, clientY: at?.y ?? 0 });
      el.dispatchEvent(event);
    },
    { kind, files, at },
  );
}

async function storyWithBody(title: string, body: string, client = owner, boardSlug = slug): Promise<void> {
  await client.json("POST", `/api/board/${boardSlug}/todos`, { title, body });
}

async function savedBody(title: string, client = owner, boardSlug = slug): Promise<string> {
  const board = await client.json<any>("GET", `/api/board/${boardSlug}`);
  const stories = Object.values(board.columns as Record<string, any[]>).flat();
  return stories.find((t) => t.title === title)?.body ?? "";
}

/** Rendered (border-box) size, which stored widths describe, and the pixel content size. */
async function imageBox(page: Page, nth = 0) {
  const img = page.locator(`${widgetSel} img`).nth(nth);
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true);
  const box = (await img.boundingBox())!;
  const [cw, ch] = await img.evaluate((el: HTMLImageElement) => [el.clientWidth, el.clientHeight]);
  return { width: box.width, height: box.height, cw, ch };
}

test("a. a pasted image appears in the editor at the caret, locks save while uploading, and persists", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: main.url });
  await signedInContext(page, owner);
  await storyWithBody("Paste story", "First line\nSecond line");
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/board/*/assets*", async (route) => {
    await gate;
    await route.continue();
  });
  await openStory(page, main, slug, "Paste story");
  await expect(page.locator("#todoBody")).toBeHidden();
  await expect(page.locator(editorSel)).toBeVisible();
  await placeCaret(page, "First line".length);
  // A real clipboard paste (Ctrl+V), as a user pastes a screenshot.
  await page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    await navigator.clipboard.write([new ClipboardItem({ "image/png": new Blob([bytes], { type: "image/png" }) })]);
  }, makePng(32, 24, 1).toString("base64"));
  await page.keyboard.press("Control+V");
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toBeVisible();
  await expect(page.locator("#saveTodoBtn")).toBeDisabled();
  expect(await editorText(page)).toBe("First line\nSecond line"); // nothing is written until it is uploaded
  release();
  await expect(page.locator(widgetSel)).toHaveCount(1);
  const text = await editorText(page);
  expect(text).toMatch(/^First line\n!\[image\]\(\/api\/assets\/[0-9a-f-]{36}\)\nSecond line$/);
  // The reference is shown as the image, not as Markdown text.
  await expect(page.locator("#todoBodyEditor .cm-content")).not.toContainText("/api/assets/");
  await expect(page.locator("#saveTodoBtn")).toBeEnabled();
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  await page.unroute("**/api/board/*/assets*");
  expect(await savedBody("Paste story")).toBe(text);

  await openStory(page, main, slug, "Paste story");
  await expect(page.locator(widgetSel)).toHaveCount(1);
  expect((await imageBox(page)).cw).toBe(32);
  await page.locator("#todoBodyPreviewTab").click();
  const img = page.locator("#todoBodyPreview img");
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(32);
});

test("b. a dropped image is inserted where it is dropped, several in their order", async ({ page }) => {
  await signedInContext(page, owner);
  await storyWithBody("Drop story", "Top\n\nBottom");
  await openStory(page, main, slug, "Drop story");
  const line = page.locator("#todoBodyEditor .cm-line").nth(2);
  const box = (await line.boundingBox())!;
  await sendFiles(page, "drop", [pngFile("one.png", makePng(10, 10, 2)), pngFile("two.png", makePng(12, 10, 3))], { x: box.x + 1, y: box.y + box.height / 2 });
  await expect(page.locator(widgetSel)).toHaveCount(2);
  expect(await editorText(page)).toMatch(/^Top\n\n!\[one\]\(\/api\/assets\/[0-9a-f-]{36}\)\n!\[two\]\(\/api\/assets\/[0-9a-f-]{36}\)\nBottom$/);
});

test("k. large images start at most 320x240, small ones are not enlarged, narrow layouts fit", async ({ page }) => {
  const large = await uploadViaApi(main, owner, slug, "large.png", makePng(1200, 900, 20));
  const small = await uploadViaApi(main, owner, slug, "small.png", makePng(40, 30, 21));
  const tall = await uploadViaApi(main, owner, slug, "tall.png", makePng(200, 1000, 22));
  await storyWithBody("Sizing story", `![large](${large.body.url})\n![small](${small.body.url})\n![tall](${tall.body.url})`);
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Sizing story");
  const [l, s, t] = [await imageBox(page, 0), await imageBox(page, 1), await imageBox(page, 2)];
  expect(l.width).toBeLessThanOrEqual(321);
  expect(l.height).toBeLessThanOrEqual(241);
  expect(l.cw / l.ch).toBeCloseTo(1200 / 900, 1);
  expect([s.cw, s.ch]).toEqual([40, 30]);
  expect(t.height).toBeLessThanOrEqual(241);
  expect(t.cw / t.ch).toBeCloseTo(0.2, 1);

  await page.setViewportSize({ width: 360, height: 740 });
  const content = (await page.locator("#todoBodyEditor .cm-content").boundingBox())!;
  const narrow = await imageBox(page, 0);
  expect(narrow.width).toBeLessThanOrEqual(content.width);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("l. click selects, drag resizes proportionally, and the width survives save, reopen, preview and public view", async ({ page, browser }) => {
  const up = await uploadViaApi(main, owner, slug, "resize.png", makePng(400, 200, 23));
  const body = `Before\n![resize](${up.body.url})\nAfter`;
  await storyWithBody("Resize story", body);
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Resize story");
  const widget = page.locator(widgetSel).first();
  const start = await imageBox(page);
  expect(Math.round(start.width)).toBe(320);
  await widget.click();
  await expect(widget).toHaveClass(/is-selected/);
  const handle = widget.locator(".todo-notes-image__handle");
  await expect(handle).toBeVisible();
  const hb = (await handle.boundingBox())!;
  await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
  await page.mouse.down();
  await page.mouse.move(hb.x + hb.width / 2 - 60, hb.y + hb.height / 2, { steps: 5 });
  await page.mouse.move(hb.x + hb.width / 2 - 120, hb.y + hb.height / 2, { steps: 5 });
  await page.mouse.up();
  const text = await editorText(page);
  const width = Number(/!\[resize\|(\d+)\]/.exec(text)?.[1]);
  // A horizontal drag moves the corner to the nearest point on the diagonal: 120px left on a 2:1
  // image shrinks it by about 96px.
  const projected = start.width * (1 - (120 * start.width) / (start.width ** 2 + start.height ** 2));
  expect(Math.abs(width - projected)).toBeLessThanOrEqual(2);
  expect(text).toBe(body.replace("![resize]", `![resize|${width}]`)); // only the alt text changed
  const resized = await imageBox(page);
  expect(Math.round(resized.width)).toBe(width);
  expect(resized.cw / resized.ch).toBeCloseTo(2, 1);
  await expect(widget).toHaveClass(/is-selected/);

  // Undo and redo the resize.
  await page.keyboard.press("Control+z");
  expect(await editorText(page)).toBe(body);
  await page.keyboard.press("Control+y");
  expect(await editorText(page)).toBe(body.replace("![resize]", `![resize|${width}]`));

  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Resize story")).toBe(body.replace("![resize]", `![resize|${width}]`));
  await openStory(page, main, slug, "Resize story");
  expect(Math.round((await imageBox(page)).width)).toBe(width);
  await page.locator("#todoBodyPreviewTab").click();
  const preview = page.locator("#todoBodyPreview img");
  await expect.poll(() => preview.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(400);
  expect(Math.round((await preview.boundingBox())!.width)).toBe(width);
  expect(await preview.getAttribute("alt")).toBe("resize");

  setPublication(main, slug, true);
  const anon = await browser.newContext();
  const pub = await anon.newPage();
  await pub.goto(`${main.url}/${slug}`);
  await pub.locator("[data-public-local-id]", { hasText: "Resize story" }).click();
  const pubImg = pub.locator("#publicTodoDialog .public-todo__notes img");
  await expect.poll(() => pubImg.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(400);
  expect(Math.round((await pubImg.boundingBox())!.width)).toBe(width);
  await pub.setViewportSize({ width: 150, height: 600 });
  expect((await pubImg.boundingBox())!.width).toBeLessThan(width); // responsive: capped by the dialog
  await anon.close();
  setPublication(main, slug, false);
});

test("m. Delete removes only the reference, Escape deselects, undo and redo behave naturally", async ({ page }) => {
  const up = await uploadViaApi(main, owner, slug, "del.png", makePng(30, 20, 24));
  const body = `Keep this\n![del](${up.body.url})\nand this`;
  await storyWithBody("Delete story", body);
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Delete story");
  const widget = page.locator(widgetSel).first();
  await widget.click();
  await expect(widget).toHaveClass(/is-selected/);
  await expect(page.locator("#todoBodyEditorStatus")).toContainText("del");
  await page.keyboard.press("Escape");
  await expect(widget).not.toHaveClass(/is-selected/);
  await expect(page.locator("#todoDialog")).toBeVisible(); // Escape deselected; it did not close the dialog
  await widget.click();
  await page.keyboard.press("Delete");
  await expect(page.locator(widgetSel)).toHaveCount(0);
  expect(await editorText(page)).toBe("Keep this\n\nand this");
  // The stored file is not deleted by removing the reference.
  expect((await fetch(`${main.url}${up.body.url}`, { headers: { Cookie: owner.cookieHeader() } })).status).toBe(200);
  await page.keyboard.press("Control+z");
  await expect(page.locator(widgetSel)).toHaveCount(1);
  expect(await editorText(page)).toBe(body);
  await page.keyboard.press("Control+Shift+KeyZ");
  expect(await editorText(page)).toBe("Keep this\n\nand this");
  await page.keyboard.press("Control+z");
  // Backspace right after an image removes it as one unit; typing elsewhere is plain text.
  await placeCaret(page, "Keep this\n".length + `![del](${up.body.url})`.length);
  await page.keyboard.press("Backspace");
  expect(await editorText(page)).toBe("Keep this\n\nand this");
  await page.keyboard.type("x");
  expect(await editorText(page)).toBe("Keep this\nx\nand this");
});

test("n. keyboard: select with Shift+Arrow, resize with + and -, reset with 0", async ({ page }) => {
  const up = await uploadViaApi(main, owner, slug, "keys.png", makePng(300, 150, 25));
  const ref = `![keys](${up.body.url})`;
  await storyWithBody("Keyboard story", `A\n${ref}\nB`);
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Keyboard story");
  await imageBox(page);
  await placeCaret(page, 2);
  await page.keyboard.press("Shift+ArrowRight");
  await expect(page.locator(widgetSel).first()).toHaveClass(/is-selected/);
  await expect(page.locator("#todoBodyEditorStatus")).toContainText("px");
  await page.keyboard.press("-");
  const smaller = Number(/!\[keys\|(\d+)\]/.exec(await editorText(page))?.[1]);
  expect(smaller).toBeLessThan(300);
  await page.keyboard.press("+");
  await page.keyboard.press("+");
  const larger = Number(/!\[keys\|(\d+)\]/.exec(await editorText(page))?.[1]);
  expect(larger).toBeGreaterThan(smaller);
  expect(larger).toBeLessThanOrEqual(302); // never past the image's own width (plus its border)
  await page.keyboard.press("0");
  expect(await editorText(page)).toBe(`A\n${ref}\nB`);
});

test("o. touch: tap selects and dragging a handle resizes", async ({ browser }) => {
  const up = await uploadViaApi(main, owner, slug, "touch.png", makePng(300, 300, 26));
  await storyWithBody("Touch story", `![touch](${up.body.url})`);
  const context = await browser.newContext({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
  await context.addCookies(toBrowserCookies(owner.cookies()));
  const page = await context.newPage();
  await openStory(page, main, slug, "Touch story");
  const widget = page.locator(widgetSel).first();
  const box = await imageBox(page);
  await widget.tap();
  await expect(widget).toHaveClass(/is-selected/);
  const hb = (await widget.locator(".todo-notes-image__handle").boundingBox())!;
  const cdp = await context.newCDPSession(page);
  const x = hb.x + hb.width / 2;
  const y = hb.y + hb.height / 2;
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
  for (let i = 1; i <= 6; i += 1) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: x - i * 15, y }] });
  }
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  const width = Number(/!\[touch\|(\d+)\]/.exec(await editorText(page))?.[1]);
  expect(width).toBeGreaterThan(0);
  expect(width).toBeLessThan(box.width);
  await context.close();
});

test("p. several images, a broken image and references in code", async ({ page }) => {
  const a = await uploadViaApi(main, owner, slug, "a.png", makePng(20, 20, 27));
  const b = await uploadViaApi(main, owner, slug, "b.png", makePng(24, 20, 28));
  const missing = "/api/assets/33333333-3333-4333-8333-333333333333";
  const body = [`![a](${a.body.url}) ![b|100](${b.body.url})`, `![gone](${missing})`, "`![c](" + a.body.url + ")`", "```", `![d](${b.body.url})`, "```"].join("\n");
  await storyWithBody("Mixed story", body);
  await signedInContext(page, owner);
  await openStory(page, main, slug, "Mixed story");
  await expect(page.locator(widgetSel)).toHaveCount(3);
  await expect(page.locator("#todoBodyEditor .todo-notes-image--unavailable")).toHaveCount(1);
  await expect(page.locator("#todoBodyEditor .todo-notes-image--unavailable")).toContainText("gone");
  await expect(page.locator("#todoBodyEditor .cm-content")).toContainText("`![c](" + a.body.url + ")`");
  await expect(page.locator("#todoBodyEditor .cm-content")).toContainText(`![d](${b.body.url})`);
  expect(Math.round((await imageBox(page, 1)).width)).toBe(100);
  // Unchanged notes save byte for byte.
  await page.locator("#todoTitle").fill("Mixed story renamed");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Mixed story renamed")).toBe(body);
});

/** Selects text in the editor by document positions. */
async function selectRange(page: Page, from: number, to: number): Promise<void> {
  await page.evaluate(([sel, a, h]) => {
    const view = (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(document.querySelector(sel as string));
    view.focus();
    view.dispatch({ selection: { anchor: a as number, head: h as number } });
  }, [editorSel, from, to] as const);
}

async function clipboardImage(page: Page, png: Buffer): Promise<void> {
  await page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    await navigator.clipboard.write([new ClipboardItem({ "image/png": new Blob([bytes], { type: "image/png" }) })]);
  }, png.toString("base64"));
}

test("s. pasting an image over selected text keeps the text until the upload succeeds", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: main.url });
  await signedInContext(page, owner);
  await storyWithBody("Selection story", "Keep SELECTED keep");
  await openStory(page, main, slug, "Selection story");
  await expect(page.locator(editorSel)).toBeVisible();

  // A failed upload leaves the selected text, and adds nothing to undo.
  await page.route("**/api/board/*/assets*", (route) => route.fulfill({ status: 500, body: "{}" }));
  await selectRange(page, 5, 13);
  await clipboardImage(page, makePng(16, 12, 40));
  await page.keyboard.press("Control+V");
  await expect(page.getByText(/image upload failed/i).first()).toBeVisible();
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toHaveCount(0);
  expect(await editorText(page)).toBe("Keep SELECTED keep");
  await page.keyboard.press("Control+z");
  expect(await editorText(page)).toBe("Keep SELECTED keep");
  await page.unroute("**/api/board/*/assets*");

  // While an upload runs the selection stays; text typed elsewhere is kept; success replaces it.
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => (release = r));
  await page.route("**/api/board/*/assets*", async (route) => {
    await gate;
    await route.continue();
  });
  await selectRange(page, 5, 13);
  await page.keyboard.press("Control+V");
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toBeVisible();
  expect(await editorText(page)).toBe("Keep SELECTED keep");
  await placeCaret(page, 0);
  await page.keyboard.type(">> ");
  release();
  await expect(page.locator(widgetSel)).toHaveCount(1);
  const text = await editorText(page);
  expect(text).toMatch(/^>> Keep \n!\[image\]\(\/api\/assets\/[0-9a-f-]{36}\)\n keep$/);
  await page.unroute("**/api/board/*/assets*");
  // The replacement is one undo step; typing is another.
  await page.keyboard.press("Control+z");
  expect(await editorText(page)).toBe(">> Keep SELECTED keep");
  await page.keyboard.press("Control+Shift+KeyZ");
  expect(await editorText(page)).toBe(text);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Selection story")).toBe(text);
});

/** The archive member's bytes, read through the ZIP central directory. */
function zipEntry(archive: Buffer, name: string): Buffer {
  let eocd = archive.length - 22;
  while (eocd >= 0 && archive.readUInt32LE(eocd) !== 0x06054b50) eocd -= 1;
  let at = archive.readUInt32LE(eocd + 16);
  for (let i = 0; i < archive.readUInt16LE(eocd + 10); i += 1) {
    const method = archive.readUInt16LE(at + 10);
    const size = archive.readUInt32LE(at + 20);
    const nameLen = archive.readUInt16LE(at + 28);
    const next = at + 46 + nameLen + archive.readUInt16LE(at + 30) + archive.readUInt16LE(at + 32);
    if (archive.toString("utf8", at + 46, at + 46 + nameLen) === name) {
      const local = archive.readUInt32LE(at + 42);
      const start = local + 30 + archive.readUInt16LE(local + 26) + archive.readUInt16LE(local + 28);
      const data = archive.subarray(start, start + size);
      return method === 8 ? zlib.inflateRawSync(data) : data;
    }
    at = next;
  }
  throw new Error(`${name} not in archive`);
}

function findStoryBody(node: unknown, title: string): string | undefined {
  if (Array.isArray(node)) {
    for (const item of node) {
      const found = findStoryBody(item, title);
      if (found !== undefined) return found;
    }
  } else if (node && typeof node === "object") {
    const obj = node as Record<string, unknown>;
    if (obj.title === title && typeof obj.body === "string") return obj.body;
    for (const value of Object.values(obj)) {
      const found = findStoryBody(value, title);
      if (found !== undefined) return found;
    }
  }
  return undefined;
}

test("t. resizing and deleting images keeps every other byte of a mixed line-ending note, through save and export", async ({ page }) => {
  const a = await uploadViaApi(main, owner, slug, "first.png", makePng(40, 30, 41));
  const b = await uploadViaApi(main, owner, slug, "middle.png", makePng(40, 30, 42));
  const c = await uploadViaApi(main, owner, slug, "last.png", makePng(40, 30, 43));
  const first = `![first](${a.body.url})`;
  const middle = `![middle](${b.body.url})`;
  const last = `![last](${c.body.url})`;
  const body =
    `${first}\r\nIntro ünïcode 😀\nline\rcr\r\n\r\n${middle}\n\n` + "```mermaid\r\ngraph TD;A-->B\n```\r\r\n" + `\`${last}\`\r\nend ${last}`;
  await storyWithBody("Line endings story", body);
  expect(await savedBody("Line endings story")).toBe(body); // the API stores the bytes as sent

  await signedInContext(page, owner);
  await openStory(page, main, slug, "Line endings story");
  await expect(page.locator(widgetSel)).toHaveCount(3); // the one in code stays text
  // Resize the middle image from the keyboard, delete the first and the last.
  await page.locator(widgetSel).nth(1).click();
  await page.keyboard.press("+");
  await page.locator(widgetSel).nth(2).click();
  await page.keyboard.press("Delete");
  await page.locator(widgetSel).nth(0).click();
  await page.keyboard.press("Delete");
  await expect(page.locator(widgetSel)).toHaveCount(1);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();

  const saved = await savedBody("Line endings story");
  const width = /!\[middle\|(\d+)\]/.exec(saved)?.[1];
  expect(width).toBeTruthy();
  const expected = body
    .replace(first, "")
    .replace(middle, `![middle|${width}](${b.body.url})`)
    .replace(`end ${last}`, "end ");
  expect(saved).toBe(expected);

  const exportRes = await fetch(`${main.url}/api/backup/export`, { headers: { "X-Scrumboy": "1", Cookie: owner.cookieHeader() } });
  expect(exportRes.status).toBe(200);
  const archive = Buffer.from(await exportRes.arrayBuffer());
  const manifest = JSON.parse(zipEntry(archive, "manifest.json").toString("utf8"));
  expect(findStoryBody(manifest, "Line endings story")).toBe(expected);
});

test("u. plain text editing: typing, multiline paste, shortcuts, Unicode, IME, focus, close and save", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: main.url });
  await signedInContext(page, owner);
  await storyWithBody("Typing story", "");
  await openStory(page, main, slug, "Typing story");
  await expect(page.locator(editorSel)).toBeVisible();

  // Clicking the label focuses the editor, as it focused the textarea.
  await page.locator("#todoBodyLabel").click();
  await expect(page.locator("#todoBodyEditor .cm-content")).toBeFocused();
  await page.keyboard.type("Hello wörld 😀 — ok");
  await page.keyboard.press("Enter");
  await page.keyboard.type("  indented [link](https://example.com) **bold**");
  expect(await editorText(page)).toBe("Hello wörld 😀 — ok\n  indented [link](https://example.com) **bold**");

  // Multiline text paste keeps its lines, with no image handling.
  await page.evaluate(() => navigator.clipboard.writeText("p1\r\np2\n\n```\ncode\n```"));
  await page.keyboard.press("Enter");
  await page.keyboard.press("Control+V");
  expect(await editorText(page)).toBe("Hello wörld 😀 — ok\n  indented [link](https://example.com) **bold**\np1\np2\n\n```\ncode\n```");

  // IME composition commits once, with no leftover composition text.
  const cdp = await page.context().newCDPSession(page);
  await page.keyboard.press("Enter");
  await cdp.send("Input.imeSetComposition", { text: "に", selectionStart: 1, selectionEnd: 1 });
  await cdp.send("Input.imeSetComposition", { text: "にほん", selectionStart: 3, selectionEnd: 3 });
  await cdp.send("Input.insertText", { text: "日本" });
  await expect.poll(() => editorText(page)).toMatch(/\n日本$/);

  // Shortcuts: line start/end, word deletion, select all, undo and redo.
  await page.keyboard.press("Home");
  await page.keyboard.type("> ");
  await page.keyboard.press("End");
  await page.keyboard.press("Control+Backspace");
  const typed = await editorText(page);
  expect(typed).toMatch(/\n> $/);
  await page.keyboard.press("Control+a");
  await page.keyboard.type("x");
  expect(await editorText(page)).toBe("x");
  await page.keyboard.press("Control+z");
  expect(await editorText(page)).toBe(typed);
  await page.keyboard.press("Control+y");
  expect(await editorText(page)).toBe("x");
  await page.keyboard.press("Control+z");

  // Escape with changes asks before discarding; Cancel keeps them, Save stores them exactly.
  await page.keyboard.press("Escape");
  await expect(page.locator("#confirmDialogCancel")).toBeVisible();
  await page.locator("#confirmDialogCancel").click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  expect(await editorText(page)).toBe(typed);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Typing story")).toBe(typed);

  // Reopened unchanged, it closes without asking.
  await openStory(page, main, slug, "Typing story");
  expect(await editorText(page)).toBe(typed);
  await page.locator("#todoBodyEditor .cm-content").click();
  await page.keyboard.press("Escape");
  await expect(page.locator("#todoDialog")).toBeHidden();
  await expect(page.locator("#confirmDialogCancel")).toHaveCount(0);
});

/**
 * Selection geometry of the nth image widget: the wrapper (which carries the outline), the
 * rendered image, the resize handle's centre, and its cursor.
 */
async function selectionGeometry(page: Page, nth: number) {
  const widget = page.locator(widgetSel).nth(nth);
  const img = widget.locator("img");
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true);
  return widget.evaluate((w) => {
    const r = (el: Element) => el.getBoundingClientRect();
    const box = r(w);
    const image = r(w.querySelector("img")!);
    const centre = (sel: string) => {
      const h = r(w.querySelector(sel)!);
      return { x: h.left + h.width / 2, y: h.top + h.height / 2 };
    };
    const style = getComputedStyle(w);
    return {
      selected: w.classList.contains("is-selected"),
      outline: style.outlineStyle,
      wrapper: { left: box.left, top: box.top, width: box.width, height: box.height },
      image: { left: image.left, top: image.top, width: image.width, height: image.height, right: image.right, bottom: image.bottom },
      handle: centre(".todo-notes-image__handle"),
      handleCount: w.querySelectorAll(".todo-notes-image__handle").length,
      cursor: getComputedStyle(w.querySelector(".todo-notes-image__handle")!).cursor,
      handlesShown: getComputedStyle(w.querySelector(".todo-notes-image__handle")!).display !== "none",
      contentWidth: (document.querySelector("#todoBodyEditor .cm-content") as HTMLElement).clientWidth,
    };
  });
}

/** Clicks the image once and checks the outline and handles are the image's own bounds. */
async function expectSelectionMatchesImage(page: Page, nth: number, rtl = false) {
  await page.locator(widgetSel).nth(nth).locator("img").click();
  const g = await selectionGeometry(page, nth);
  expect(g.selected).toBe(true);
  expect(g.outline).toBe("solid");
  expect(g.handlesShown).toBe(true);
  // The outlined wrapper is exactly the rendered image: no wider, no taller.
  expect(Math.abs(g.wrapper.width - g.image.width)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(g.wrapper.height - g.image.height)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(g.wrapper.left - g.image.left)).toBeLessThanOrEqual(0.5);
  // One handle, on the image's bottom-right corner (bottom-left in RTL), with a diagonal cursor.
  expect(g.handleCount).toBe(1);
  expect(Math.abs(g.handle.x - (rtl ? g.image.left : g.image.right))).toBeLessThanOrEqual(1);
  expect(Math.abs(g.handle.y - g.image.bottom)).toBeLessThanOrEqual(1);
  expect(g.cursor).toBe(rtl ? "nesw-resize" : "nwse-resize");
  return g;
}

test("v. the selection outline and handles follow the image itself on the first click", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: main.url });
  const shapes = [
    ["landscape", makePng(1200, 900, 50)], // capped at 320x240
    ["wide", makePng(640, 200, 51)], // capped at 320 wide (wider natural size than the cap)
    ["mid", makePng(200, 150, 52)], // 200px in a much wider editor
    ["small", makePng(40, 30, 53)], // never enlarged
    ["portrait", makePng(200, 1000, 54)], // capped at 240 tall
    ["resized", makePng(1200, 600, 55)], // stored width 180
  ] as const;
  const refs: string[] = [];
  for (const [name, png] of shapes) {
    const up = await uploadViaApi(main, owner, slug, `${name}.png`, png);
    refs.push(name === "resized" ? `![${name}|180](${up.body.url})` : `![${name}](${up.body.url})`);
  }
  const body = refs.join("\n");
  await storyWithBody("Geometry story", body);
  await signedInContext(page, owner);
  await page.setViewportSize({ width: 1280, height: 900 });
  await openStory(page, main, slug, "Geometry story");
  await expect(page.locator(widgetSel)).toHaveCount(shapes.length);

  const expected: Array<[number, number]> = [[320, 240], [320, 100], [200, 150], [40, 30], [48, 240], [180, 90]];
  for (let i = 0; i < shapes.length; i += 1) {
    const g = await expectSelectionMatchesImage(page, i);
    // Border-box sizes (content plus the 1px border on each side).
    expect(Math.abs(g.image.width - expected[i][0])).toBeLessThanOrEqual(2);
    expect(Math.abs(g.image.height - expected[i][1])).toBeLessThanOrEqual(2);
    expect(g.contentWidth).toBeGreaterThan(g.wrapper.width + 100); // the editor is much wider than the box
  }

  // Switching to Preview and back, then selecting again: still the image's bounds.
  await page.locator("#todoBodyPreviewTab").click();
  await page.locator("#todoBodyWriteTab").click();
  await expectSelectionMatchesImage(page, 1);

  // Drag the portrait image's end corner: proportional, and the box follows the new size.
  const portrait = page.locator(widgetSel).nth(4);
  await portrait.locator("img").click();
  const before = await selectionGeometry(page, 4);
  await page.mouse.move(before.handle.x, before.handle.y);
  await page.mouse.down();
  await page.mouse.move(before.handle.x + 20, before.handle.y + 80, { steps: 6 });
  await page.mouse.up();
  const width = Number(/!\[portrait\|(\d+)\]/.exec(await editorText(page))?.[1]);
  expect(width).toBeGreaterThan(before.image.width + 10);
  const after = await expectSelectionMatchesImage(page, 4);
  expect(Math.round(after.image.width)).toBe(width);
  expect((after.image.width - 2) / (after.image.height - 2)).toBeCloseTo(0.2, 1);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Geometry story")).toBe(body.replace("![portrait]", `![portrait|${width}]`));

  // Reopened: the first click on each image is already exact.
  await openStory(page, main, slug, "Geometry story");
  for (let i = 0; i < shapes.length; i += 1) await expectSelectionMatchesImage(page, i);
  expect(Math.round((await selectionGeometry(page, 4)).image.width)).toBe(width);

  // A freshly pasted large image: selected with the first click after it appears.
  await placeCaret(page, 0);
  await clipboardImage(page, makePng(900, 500, 56));
  await page.keyboard.press("Control+V");
  await expect(page.locator(widgetSel)).toHaveCount(shapes.length + 1);
  const pasted = await expectSelectionMatchesImage(page, 0);
  expect(Math.abs(pasted.image.width - 320)).toBeLessThanOrEqual(2);

  // Narrow layout: boxes never exceed the image or the editor.
  await page.setViewportSize({ width: 360, height: 740 });
  for (const i of [0, 1, 6]) {
    const g = await expectSelectionMatchesImage(page, i);
    expect(g.wrapper.width).toBeLessThanOrEqual(g.contentWidth);
  }
  // Viewport grows back: the box grows with the image again.
  await page.setViewportSize({ width: 1280, height: 900 });
  expect(Math.abs((await expectSelectionMatchesImage(page, 1)).image.width - 320)).toBeLessThanOrEqual(2);

  // Right-to-left: the handle moves to the bottom-left corner, with the mirrored cursor.
  await page.evaluate(() => (document.documentElement.dir = "rtl"));
  await expectSelectionMatchesImage(page, 3, true);
  await page.evaluate(() => (document.documentElement.dir = "ltr"));
});

/** The width a corner drag should produce: the movement projected onto the image's diagonal. */
function projectedWidth(w: number, h: number, dx: number, dy: number, growRight: boolean): number {
  return w * (1 + ((growRight ? dx : -dx) * w + dy * h) / (w * w + h * h));
}

/** Drags the resize handle of the selected nth image by (dx, dy) with the mouse; returns the stored width. */
async function dragCorner(page: Page, nth: number, dx: number, dy: number, alt: string) {
  const g = await selectionGeometry(page, nth);
  const from = g.handle;
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x + dx / 2, from.y + dy / 2, { steps: 4 });
  await page.mouse.move(from.x + dx, from.y + dy, { steps: 4 });
  await page.mouse.up();
  const width = Number(new RegExp(`!\\[${alt}\\|(\\d+)\\]`).exec(await editorText(page))?.[1]);
  return { before: g, width };
}

test("w. corner drags resize proportionally along both axes, with limits, cancel, undo and save", async ({ page, browser }) => {
  const land = await uploadViaApi(main, owner, slug, "land.png", makePng(400, 200, 60)); // 320 x 160 by default
  const port = await uploadViaApi(main, owner, slug, "port.png", makePng(300, 600, 61)); // 120 x 240 by default
  const body = `![land](${land.body.url})\n![port](${port.body.url})`;
  await storyWithBody("Diagonal story", body);
  await signedInContext(page, owner);
  await page.setViewportSize({ width: 1280, height: 900 });
  await openStory(page, main, slug, "Diagonal story");
  await expect(page.locator(widgetSel)).toHaveCount(2);

  const cases = [
    // [image, alt, dx, dy]: vertical-dominant and horizontal-dominant, shrinking and growing.
    [1, "port", 4, 60],
    [1, "port", -4, -50],
    [0, "land", -70, 6],
    [0, "land", -6, -45],
    [0, "land", 50, 4],
  ] as const;
  let text = body;
  for (const [nth, alt, dx, dy] of cases) {
    await page.locator(widgetSel).nth(nth).locator("img").click();
    const { before, width } = await dragCorner(page, nth, dx, dy, alt);
    // Projected, within 48px and the image's own width plus its border.
    const want = Math.max(48, Math.min(projectedWidth(before.image.width, before.image.height, dx, dy, true), alt === "land" ? 402 : 302));
    expect(width, `${alt} ${dx},${dy}`).toBeGreaterThan(0);
    expect(Math.abs(width - want), `${alt} ${dx},${dy}: ${width} vs ${want}`).toBeLessThanOrEqual(2);
    expect(Math.abs(width - before.image.width)).toBeGreaterThan(5); // the drag did resize
    // One scale factor: the image keeps its proportions, and the outline still fits it.
    const after = await expectSelectionMatchesImage(page, nth);
    expect(Math.round(after.image.width)).toBe(width);
    expect((after.image.width - 2) / (after.image.height - 2)).toBeCloseTo(alt === "land" ? 2 : 0.5, 1);
    text = await editorText(page);
  }

  // Undo and redo the last drag, as one step each.
  const lastWidth = Number(/!\[land\|(\d+)\]/.exec(text)?.[1]);
  await page.keyboard.press("Control+z");
  const undone = await editorText(page);
  expect(undone).not.toBe(text);
  expect(undone).toBe(text.replace(`![land|${lastWidth}]`, /!\[land\|\d+\]/.exec(undone)![0]));
  await page.keyboard.press("Control+Shift+KeyZ");
  expect(await editorText(page)).toBe(text);

  // Limits: dragging far inward stops at 48px, far outward at the image's own width (plus border).
  await page.locator(widgetSel).nth(1).locator("img").click();
  expect((await dragCorner(page, 1, -400, -800, "port")).width).toBe(48);
  await page.locator(widgetSel).nth(1).locator("img").click();
  expect((await dragCorner(page, 1, 200, 600, "port")).width).toBe(302);
  text = await editorText(page);

  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(await savedBody("Diagonal story")).toBe(text);
  await openStory(page, main, slug, "Diagonal story");
  expect(await editorText(page)).toBe(text);
  expect(Math.round((await expectSelectionMatchesImage(page, 1)).image.width)).toBe(302);

  // Right-to-left: the handle is on the bottom-left corner; dragging it down and left grows the image.
  await page.evaluate(() => (document.documentElement.dir = "rtl"));
  await page.locator(widgetSel).nth(0).locator("img").click();
  const rtlBefore = Number(/!\[land\|(\d+)\]/.exec(await editorText(page))?.[1]);
  const rtl = await dragCorner(page, 0, -5, 40, "land");
  expect(rtl.width).toBeGreaterThan(rtlBefore + 5);
  expect(Math.abs(rtl.width - projectedWidth(rtl.before.image.width, rtl.before.image.height, -5, 40, false))).toBeLessThanOrEqual(2);
  await expectSelectionMatchesImage(page, 0, true);
  await page.evaluate(() => (document.documentElement.dir = "ltr"));

  // Touch: a vertical drag on a handle resizes; a cancelled touch leaves the note unchanged.
  const context = await browser.newContext({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
  await context.addCookies(toBrowserCookies(owner.cookies()));
  const touchPage = await context.newPage();
  await openStory(touchPage, main, slug, "Diagonal story");
  await expect(touchPage.locator(widgetSel)).toHaveCount(2);
  await touchPage.locator(widgetSel).nth(1).locator("img").tap();
  await touchPage.keyboard.press("0"); // back to the default size (120 x 240), as one undoable edit
  const startText = await editorText(touchPage);
  const cdp = await context.newCDPSession(touchPage);
  const touchDrag = async (dy: number, end: "touchEnd" | "touchCancel") => {
    const g = await selectionGeometry(touchPage, 1);
    const { x, y } = g.handle;
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
    for (let i = 1; i <= 6; i += 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: y + (i * dy) / 6 }] });
    }
    await cdp.send("Input.dispatchTouchEvent", { type: end, touchPoints: [] });
    return g;
  };
  await touchDrag(-60, "touchCancel");
  expect(await editorText(touchPage)).toBe(startText);
  expect(Math.abs((await selectionGeometry(touchPage, 1)).image.width - 120)).toBeLessThanOrEqual(2);
  const g = await touchDrag(-60, "touchEnd");
  const touched = Number(/!\[port\|(\d+)\]/.exec(await editorText(touchPage))?.[1]);
  expect(Math.abs(touched - projectedWidth(g.image.width, g.image.height, 0, -60, true))).toBeLessThanOrEqual(2);
  expect(touched).toBeLessThan(110);
  await context.close();
});

test("x. clicking and moving the caret beside an image work after it loads and changes the line height", async ({ page }) => {
  const up = await uploadViaApi(main, owner, slug, "late.png", makePng(300, 200, 62)); // 240 tall once loaded
  await storyWithBody("Late image story", `Before\n![late](${up.body.url})\nAfter`);
  await signedInContext(page, owner);
  let release: () => void = () => {};
  const gate = new Promise<void>((r) => (release = r));
  await page.route(`**${up.body.url}`, async (route) => {
    await gate;
    await route.continue();
  });
  await openStory(page, main, slug, "Late image story");
  await expect(page.locator(`${widgetSel}.todo-notes-image--loading`)).toHaveCount(1);
  const afterLine = page.locator("#todoBodyEditor .cm-line", { hasText: "After" });
  const yBefore = (await afterLine.boundingBox())!.y;
  release();
  await expect(page.locator(`${widgetSel}.todo-notes-image--loading`)).toHaveCount(0);
  await expect.poll(async () => (await afterLine.boundingBox())!.y).toBeGreaterThan(yBefore + 150);
  await page.unroute(`**${up.body.url}`);

  const ref = `![late](${up.body.url})`;
  const head = () =>
    page.evaluate((sel) => (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(document.querySelector(sel)).state.selection.main.head, editorSel);
  // A click at the end of the line below the image puts the caret there.
  const ab = (await afterLine.boundingBox())!;
  await page.mouse.click(ab.x + ab.width - 2, ab.y + ab.height / 2);
  expect(await head()).toBe(`Before\n${ref}\nAfter`.length);
  // A click beside the image (right of it, on its line) puts the caret right after it.
  const img = (await page.locator(`${widgetSel} img`).boundingBox())!;
  await page.mouse.click(img.x + img.width + 40, img.y + img.height / 2);
  expect(await head()).toBe(`Before\n${ref}`.length);
  await page.keyboard.type("x");
  expect(await editorText(page)).toBe(`Before\n${ref}x\nAfter`);
  await page.keyboard.press("Backspace");
  // Vertical caret movement uses the loaded height: down from "Before" reaches the image line,
  // then "After".
  await placeCaret(page, "Before".length);
  await page.keyboard.press("ArrowDown");
  const onImage = await head();
  expect(onImage === "Before\n".length || onImage === `Before\n${ref}`.length).toBe(true);
  await page.keyboard.press("ArrowDown");
  expect(await head()).toBeGreaterThanOrEqual(`Before\n${ref}\n`.length);
  await page.keyboard.type("!");
  const lines = (await editorText(page)).split("\n");
  expect(lines.slice(0, 2)).toEqual(["Before", ref]);
  expect(lines[2]).toContain("!");
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
  const sized = await uploadViaApi(main, owner, slug, "sized.png", makePng(8, 8, 30));
  const codeOnly = await uploadViaApi(main, owner, slug, "code.png", makePng(8, 8, 4));
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Public image story", body: `![pub](${embedded.body.url})\n\n![sized|64](${sized.body.url})` });
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
    await expect(img).toHaveCount(2);
    await expect.poll(() => img.first().evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth)).toBe(8);
    // A sized reference is still an embedded image for the server's public eligibility.
    expect((await fetch(`${main.url}${sized.body.url}`)).status).toBe(200);
    expect((await fetch(`${main.url}${codeOnly.body.url}`)).status).toBe(404);
  }
  setPublication(main, slug, false);
  expect((await fetch(`${main.url}${embedded.body.url}`)).status).toBe(404);
  await anon.close();
  await signedOut.close();
});

test("e. a viewer sees images in the read-only editor but cannot change or upload them", async ({ page }) => {
  const viewer = apiClient(main.url);
  await viewer.json("POST", "/api/auth/login", { email: VIEWER.email, password: VIEWER.password });
  await signedInContext(page, viewer);
  await openStory(page, main, slug, "Public image story");
  await expect(page.locator(widgetSel)).toHaveCount(2);
  await expect(page.locator("#todoBodyEditor .cm-content")).toHaveAttribute("contenteditable", "false");
  await page.locator(widgetSel).first().click();
  await expect(page.locator("#todoBodyEditor .todo-notes-image__handle").first()).toBeHidden();
  await page.keyboard.press("Delete");
  await expect(page.locator(widgetSel)).toHaveCount(2);
  await sendFiles(page, "paste", [pngFile("x.png", makePng(4, 4, 5))]);
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toHaveCount(0);
  const res = await uploadViaApi(main, viewer, slug, "x.png", makePng(4, 4, 5));
  expect([403, 404]).toContain(res.status);
});

test("g/k. export .sbz, delete the project, import a copy: images and widths restored, code text kept", async () => {
  const embedded = await uploadViaApi(main, owner, slug, "keep.png", makePng(12, 12, 10));
  await owner.json("POST", `/api/board/${slug}/todos`, {
    title: "Mixed references",
    body: "![keep|180](" + embedded.body.url + ")\n\n```\n" + embedded.body.url + "\n```",
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
  const mixed = await savedBody("Mixed references");
  const match = /^!\[keep\|180\]\(\/api\/assets\/([0-9a-f-]{36})\)/.exec(mixed);
  expect(match).toBeTruthy();
  expect(match![1]).not.toBe(embedded.body.assetId);
  const served = await fetch(`${main.url}/api/assets/${match![1]}`, { headers: { Cookie: owner.cookieHeader() } });
  expect(served.status).toBe(200);
  // The fenced mention still names the original id: only image destinations are remapped.
  expect(mixed).toContain("```\n" + embedded.body.url + "\n```");
  // The resized width survived: imported resize story keeps its suffix.
  expect(await savedBody("Resize story")).toMatch(/^Before\n!\[resize\|\d+\]\(\/api\/assets\/[0-9a-f-]{36}\)\nAfter$/);
});

test("i. closing the dialog during an upload aborts it", async ({ page }) => {
  await signedInContext(page, owner);
  await storyWithBody("Abort story", "Text");
  await page.route("**/api/board/*/assets*", () => new Promise(() => {}));
  await openStory(page, main, slug, "Abort story");
  await sendFiles(page, "paste", [pngFile("slow.png", makePng(4, 4, 6))]);
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toBeVisible();
  await page.evaluate(() => (document.getElementById("todoDialog") as HTMLDialogElement).close());
  await page.unroute("**/api/board/*/assets*");
  await openStory(page, main, slug, "Abort story");
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toHaveCount(0);
  expect(await editorText(page)).toBe("Text");
});

test("j. the storage budget rejects uploads past the limit", async ({ page }) => {
  const client = apiClient(budget.url);
  await client.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await client.json<{ slug: string }>("POST", "/api/projects", { name: "Budget Board" });
  await client.json("POST", `/api/board/${project.slug}/todos`, { title: "Budget story" });
  expect((await uploadViaApi(budget, client, project.slug, "a.png", makePng(16, 16, 7, 700_000))).status).toBe(201);
  await signedInContext(page, client);
  await openStory(page, budget, project.slug, "Budget story");
  await sendFiles(page, "paste", [pngFile("b.png", makePng(16, 16, 8, 700_000))]);
  await expect(page.locator("#todoBodyEditor .todo-notes-image--uploading")).toHaveCount(0);
  expect(await editorText(page)).toBe("");
  const res = await uploadViaApi(budget, client, project.slug, "c.png", makePng(16, 16, 9, 700_000));
  expect(res.status).toBe(507);
});

test("h. with the flag off, the notes stay a textarea and image syntax stays text", async ({ page }) => {
  const client = apiClient(off.url);
  await client.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await client.json<{ slug: string }>("POST", "/api/projects", { name: "Plain Board" });
  await client.json("POST", `/api/board/${project.slug}/todos`, { title: "Plain story", body: "![x](/api/assets/11111111-1111-4111-8111-111111111111)" });
  const status = await (await fetch(`${off.url}/api/auth/status`)).json();
  expect(status.inlineImagesEnabled).toBe(false);
  await signedInContext(page, client);
  await openStory(page, off, project.slug, "Plain story");
  await expect(page.locator("#todoBody")).toBeVisible();
  await expect(page.locator("#todoBodyEditor")).toBeHidden();
  await expect(page.locator("#todoDialog input[type=file]")).toHaveCount(0);
  await page.locator("#todoBodyPreviewTab").click();
  await expect(page.locator("#todoBodyPreview img")).toHaveCount(0);
  await expect(page.locator("#todoBodyPreview")).toContainText("![x](/api/assets/");
});

test("q. with no notes variables set, all three capabilities are on", async ({ page }) => {
  const status = await (await fetch(`${main.url}/api/auth/status`)).json();
  expect([status.markdownNotesEnabled, status.mermaidNotesEnabled, status.inlineImagesEnabled]).toEqual([true, true, true]);
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Default Board" });
  await owner.json("POST", `/api/board/${project.slug}/todos`, { title: "Default story" });
  await signedInContext(page, owner);
  await openStory(page, main, project.slug, "Default story");
  await expect(page.locator("#todoBodyPreviewTab")).toBeVisible();
  await expect(page.locator(editorSel)).toBeVisible();
  await expect(page.locator("#todoBodyInsertImage")).toHaveCount(0);
});

test("r. Markdown opt-out disables Mermaid and images despite explicit 1", async ({ page }) => {
  const client = apiClient(markdownOff.url);
  await client.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await client.json<{ slug: string }>("POST", "/api/projects", { name: "Text Board" });
  const body = "```mermaid\ngraph TD; A-->B\n```\n![x](/api/assets/11111111-1111-4111-8111-111111111111)";
  await client.json("POST", `/api/board/${project.slug}/todos`, { title: "Text story", body });
  const status = await (await fetch(`${markdownOff.url}/api/auth/status`)).json();
  expect([status.markdownNotesEnabled, status.mermaidNotesEnabled, status.inlineImagesEnabled]).toEqual([false, false, false]);
  expect((await fetch(`${markdownOff.url}/api/assets/11111111-1111-4111-8111-111111111111`)).status).toBe(404);
  await signedInContext(page, client);
  await openStory(page, markdownOff, project.slug, "Text story");
  await expect(page.locator("#todoBodyPreviewTab")).toBeHidden();
  await expect(page.locator("#todoBodyEditor")).toBeHidden();
  await expect(page.locator("#todoBody")).toHaveValue(body);
});

test("y. custom fields and inline images edit together without disturbing each other", async ({ page }) => {
  const CONTRIBUTOR = { email: "images-contributor-e2e@example.com", password: "password123", name: "Image Contributor" };
  const project = await owner.json<{ slug: string; id: number }>("POST", "/api/projects", { name: "Fields Board" });
  const other = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Plain Board" });
  for (const def of [
    { key: "ticket", name: "Ticket", type: "url" },
    { key: "points", name: "Points", type: "number" },
    { key: "note", name: "<i>Note</i>", type: "text" },
  ]) {
    await owner.json("POST", `/api/board/${project.slug}/custom-fields`, def);
  }
  const asset = (await uploadViaApi(main, owner, project.slug, "f.png", makePng(16, 12, 41))).body.assetId as string;
  const body = `Before\n![image](/api/assets/${asset})\nAfter`;
  const created = await owner.json<{ localId: number }>("POST", `/api/board/${project.slug}/todos`, {
    title: "Fields story",
    body,
    customFields: { ticket: "https://example.com/T-1", points: 5, note: "<b>keep</b>" },
  });
  await owner.json("POST", `/api/board/${other.slug}/todos`, { title: "Plain story" });
  const read = () => owner.json<any>("GET", `/api/board/${project.slug}/todos/${created.localId}`);
  const cf = (key: string) => page.locator(`#todoCustomFields input[data-cf-key="${key}"]`);

  await signedInContext(page, owner);
  // Card badges and field labels are text, never markup.
  await page.goto(`${main.url}/${project.slug}`);
  const card = page.locator(".card[data-todo-local-id]", { hasText: "Fields story" });
  await expect(card.locator(".card__field", { hasText: "<b>keep</b>" })).toHaveCount(1);
  await expect(card.locator(".card__field b")).toHaveCount(0);

  await openStory(page, main, project.slug, "Fields story");
  await expect(page.locator(widgetSel)).toHaveCount(1);
  await expect(cf("ticket")).toHaveValue("https://example.com/T-1");
  await expect(cf("points")).toHaveValue("5");
  await expect(cf("note")).toHaveValue("<b>keep</b>");
  await expect(page.locator("#todoCustomFields .field__label", { hasText: "<i>Note</i>" })).toHaveCount(1);
  await expect(page.locator("#todoCustomFields i")).toHaveCount(0);
  // Fields arrive after the dialog opened; that alone is not an unsaved change.
  await page.keyboard.press("Escape");
  await expect(page.locator("#todoDialog")).toBeHidden();

  // A custom field change alone is an unsaved change.
  await openStory(page, main, project.slug, "Fields story");
  await cf("points").fill("6");
  await page.keyboard.press("Escape");
  await expect(page.locator("#confirmDialogCancel")).toBeVisible();
  await page.locator("#confirmDialogCancel").click();
  await expect(cf("points")).toHaveValue("6");

  // Browser validation blocks a bad URL before any request.
  const patches: string[] = [];
  page.on("request", (req) => {
    if (req.method() === "PATCH") patches.push(req.postData() ?? "");
  });
  await cf("ticket").fill("not a url");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  expect(patches).toHaveLength(0);

  // Change one field, clear one, leave one, and edit the notes beside the image.
  await cf("ticket").fill("https://example.com/T-1");
  await cf("points").fill("8");
  await cf("note").fill("");
  await placeCaret(page, (await editorText(page)).length);
  await page.keyboard.type(" edited");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(patches).toHaveLength(1);
  expect(JSON.parse(patches[0]).customFields).toEqual({ points: 8, note: null });
  let saved = await read();
  expect(saved.customFields).toEqual({ ticket: "https://example.com/T-1", points: 8 });
  expect(saved.body).toBe(`${body} edited`);

  // Reopen: values and image are as saved; a definition deleted while the dialog is open does
  // not block saving other changes, since only changed fields are sent.
  await openStory(page, main, project.slug, "Fields story");
  await expect(page.locator(widgetSel)).toHaveCount(1);
  await expect(cf("points")).toHaveValue("8");
  await expect(cf("note")).toHaveValue("");
  await owner.json("DELETE", `/api/board/${project.slug}/custom-fields/note`);
  await cf("points").fill("13");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  saved = await read();
  expect(saved.customFields).toEqual({ ticket: "https://example.com/T-1", points: 13 });
  expect(saved.body).toBe(`${body} edited`);

  // Definitions that load late for a dialog that was closed never land in another board's dialog.
  // Stay in the SPA when switching boards so Board A's in-flight defs request is not aborted by
  // a full document navigation (page.goto), which would make a stale-response race impossible.
  let releaseDefs: () => void = () => {};
  const defsGate = new Promise<void>((r) => (releaseDefs = r));
  let markPending: () => void = () => {};
  const defsPending = new Promise<void>((r) => (markPending = r));
  await page.route(`**/api/board/${project.slug}/custom-fields`, async (route) => {
    markPending();
    await defsGate;
    await route.continue();
  });
  // Open without a second goto: already on Fields Board from earlier steps.
  await page.locator(".card[data-todo-local-id]", { hasText: "Fields story" }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  await defsPending; // Board A defs request intercepted and still held
  await page.keyboard.press("Escape");
  await expect(page.locator("#todoDialog")).toBeHidden();
  const boardBDefs = page.waitForResponse(
    (res) =>
      res.request().method() === "GET"
      && new URL(res.url()).pathname === `/api/board/${other.slug}/custom-fields`,
  );
  const staleDefs = page.waitForResponse(
    (res) =>
      res.request().method() === "GET"
      && new URL(res.url()).pathname === `/api/board/${project.slug}/custom-fields`,
  );
  await page.evaluate((path) => {
    history.pushState({}, "", path);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, `/${other.slug}`);
  await expect(page.locator(".card[data-todo-local-id]", { hasText: "Plain story" })).toBeVisible();
  await page.locator(".card[data-todo-local-id]", { hasText: "Plain story" }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  await boardBDefs;
  await expect(page.locator("#todoCustomFields input")).toHaveCount(0);
  releaseDefs();
  const staleResponse = await staleDefs;
  await staleResponse.finished();
  // Flush microtasks/macrotasks so Board A's loadCustomFieldDefs().then(...) has run.
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        queueMicrotask(() => {
          setTimeout(resolve, 0);
        });
      }),
  );
  await expect(page.locator("#todoCustomFields input")).toHaveCount(0);
  await page.unroute(`**/api/board/${project.slug}/custom-fields`);
  await page.keyboard.press("Escape");

  // A contributor sees the fields read-only and can still save notes around the image.
  const contributorUser = await owner.json<{ id: number }>("POST", "/api/admin/users", CONTRIBUTOR);
  await owner.json("POST", `/api/projects/${project.id}/members`, { user_id: contributorUser.id, role: "contributor" });
  await owner.json("PATCH", `/api/board/${project.slug}/todos/${created.localId}`, {
    title: "Fields story", body: saved.body, tags: [], assigneeUserId: contributorUser.id,
  });
  const contributor = apiClient(main.url);
  await contributor.json("POST", "/api/auth/login", { email: CONTRIBUTOR.email, password: CONTRIBUTOR.password });
  await page.context().clearCookies();
  await signedInContext(page, contributor);
  await openStory(page, main, project.slug, "Fields story");
  await expect(cf("points")).toBeDisabled();
  await expect(cf("ticket")).toBeDisabled();
  await placeCaret(page, (await editorText(page)).length);
  await page.keyboard.type(" by contributor");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  saved = await read();
  expect(saved.body).toBe(`${body} edited by contributor`);
  expect(saved.customFields).toEqual({ ticket: "https://example.com/T-1", points: 13 });
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
