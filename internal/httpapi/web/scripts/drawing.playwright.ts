import { spawn, execFileSync, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import { DatabaseSync } from "node:sqlite";
import { expect, test, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");
const OWNER = { email: "drawing-owner-e2e@example.com", password: "password123", name: "Drawing Owner" };
const VIEWER = { email: "drawing-viewer-e2e@example.com", password: "password123", name: "Drawing Viewer" };

type Cookie = { name: string; value: string; path?: string; httpOnly?: boolean };
type Instance = { proc: ChildProcessWithoutNullStreams; url: string; dataDir: string };

let binary = "";
let tmpRoot = "";
const instances: Instance[] = [];
let main: Instance;
let owner: ReturnType<typeof apiClient>;
let viewer: ReturnType<typeof apiClient>;
let slug = "";

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

function makePng(w: number, h: number, seed: number): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w * 3; x++) raw[y * (w * 3 + 1) + 1 + x] = (x * 7 + y * 13 + seed * 31) & 0xff;
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

async function startInstance(env: Record<string, string>, dataDir?: string): Promise<Instance> {
  const dir = dataDir ?? (await fs.promises.mkdtemp(path.join(tmpRoot, "data-")));
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const proc = spawn(binary, [], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1",
      SCRUMBOY_MARKDOWN_NOTES_ENABLED: "",
      SCRUMBOY_MERMAID_NOTES_ENABLED: "",
      SCRUMBOY_INLINE_IMAGES_ENABLED: "",
      SCRUMBOY_DRAWING_ENABLED: "",
      SCRUMBOY_WALL_DRAWING_ENABLED: "",
      SCRUMBOY_TRUST_PROXY: "1",
      DATA_DIR: dir,
      BIND_ADDR: `127.0.0.1:${port}`,
      SCRUMBOY_TLS_CERT: path.join(dir, "no-cert"),
      SCRUMBOY_TLS_KEY: path.join(dir, "no-key"),
      ...env,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  proc.stderr.on("data", (c) => (stderr += String(c)));
  proc.stdout.on("data", () => {});
  const inst = { proc, url, dataDir: dir };
  instances.push(inst);
  try {
    await waitForServer(url, 120_000);
  } catch (err) {
    throw new Error(`server failed to start: ${err}\n${stderr}`);
  }
  return inst;
}

async function stopInstance(inst: Instance): Promise<void> {
  inst.proc.kill("SIGTERM");
  await new Promise<void>((resolve) => {
    const t = setTimeout(() => {
      try {
        inst.proc.kill("SIGKILL");
      } catch {
        void 0;
      }
      resolve();
    }, 10_000);
    inst.proc.once("exit", () => {
      clearTimeout(t);
      resolve();
    });
  });
}

function query<T>(inst: Instance, sql: string, ...args: unknown[]): T[] {
  const db = new DatabaseSync(path.join(inst.dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    return db.prepare(sql).all(...(args as any[])) as T[];
  } finally {
    db.close();
  }
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

async function uploadImage(inst: Instance, name: string, bytes: Buffer): Promise<string> {
  const form = new FormData();
  form.append("file", new Blob([bytes], { type: "image/png" }), name);
  const res = await fetch(`${inst.url}/api/board/${slug}/assets`, {
    method: "POST",
    headers: { "X-Scrumboy": "1", Cookie: owner.cookieHeader() },
    body: form,
  });
  const body = (await res.json()) as { url: string };
  expect(res.status).toBe(201);
  return body.url;
}

async function signIn(page: Page): Promise<void> {
  await page.context().addCookies(toBrowserCookies(owner.cookies()));
}

async function openStory(page: Page, inst: Instance, title: string): Promise<void> {
  await page.goto(`${inst.url}/${slug}`);
  await page.locator(".card[data-todo-local-id]", { hasText: title }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
}

async function storyBody(title: string): Promise<string> {
  return query<{ body: string }>(main, "SELECT body FROM todos WHERE title = ?", title)[0]?.body ?? "";
}

async function drawStroke(page: Page, selector: string, from: [number, number], to: [number, number]): Promise<void> {
  const box = (await page.locator(selector).boundingBox())!;
  const x0 = box.x + box.width * from[0];
  const y0 = box.y + box.height * from[1];
  const x1 = box.x + box.width * to[0];
  const y1 = box.y + box.height * to[1];
  await page.mouse.move(x0, y0);
  await page.mouse.down();
  await page.mouse.move(x1, y1, { steps: 12 });
  await page.mouse.up();
}

async function saveStory(page: Page): Promise<void> {
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  test.setTimeout(300_000);
  tmpRoot = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-drawing-"));
  binary = path.join(tmpRoot, process.platform === "win32" ? "scrumboy.exe" : "scrumboy");
  execFileSync("go", ["build", "-o", binary, "./cmd/scrumboy"], { cwd: repoRoot, stdio: "inherit" });
  main = await startInstance({});
  owner = apiClient(main.url);
  await owner.json("POST", "/api/auth/bootstrap", OWNER);
  const project = await owner.json<{ slug: string; id: number }>("POST", "/api/projects", { name: "Drawing Board" });
  slug = project.slug;
  const viewerUser = await owner.json<{ id: number }>("POST", "/api/admin/users", VIEWER);
  await owner.json("POST", `/api/projects/${project.id}/members`, { user_id: viewerUser.id, role: "viewer" });
  viewer = apiClient(main.url);
  await viewer.json("POST", "/api/auth/login", { email: VIEWER.email, password: VIEWER.password });
});

test.afterAll(async () => {
  for (const inst of instances) await stopInstance(inst);
  if (tmpRoot) await fs.promises.rm(tmpRoot, { recursive: true, force: true }).catch(() => {});
});

const NOTES_SCROLLER = "#todoBodyEditor .cm-scroller";
const NOTES_INK = `${NOTES_SCROLLER} svg.notes-ink-layer`;
const BLUE: [number, number, number] = [37, 99, 235];

function inkPaths(page: Page) {
  return page.locator(`${NOTES_INK} g.drawing-strokes path`);
}

function decodePixel(png: Buffer): [number, number, number] {
  let off = 8;
  const idat: Buffer[] = [];
  while (off < png.length) {
    const len = png.readUInt32BE(off);
    const type = png.toString("ascii", off + 4, off + 8);
    if (type === "IDAT") idat.push(png.subarray(off + 8, off + 8 + len));
    off += 12 + len;
  }
  const raw = zlib.inflateSync(Buffer.concat(idat));
  return [raw[1], raw[2], raw[3]];
}

async function pixelAt(page: Page, x: number, y: number): Promise<[number, number, number]> {
  return decodePixel(await page.screenshot({ clip: { x: Math.round(x), y: Math.round(y), width: 1, height: 1 } }));
}

function isColor(px: [number, number, number], rgb: [number, number, number], tolerance = 30): boolean {
  return Math.abs(px[0] - rgb[0]) <= tolerance && Math.abs(px[1] - rgb[1]) <= tolerance && Math.abs(px[2] - rgb[2]) <= tolerance;
}

async function editorText(page: Page): Promise<string> {
  return page.evaluate(() => (window as any).ScrumboyCodeMirror.EditorView.findFromDOM(document.querySelector("#todoBodyEditor .cm-editor")).state.doc.toString());
}

async function enableNotesInk(page: Page): Promise<void> {
  const toggle = page.locator(".notes-ink-toggle");
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeEnabled();
  if ((await toggle.getAttribute("aria-pressed")) !== "true") await toggle.click();
  await expect(page.locator(NOTES_INK)).toHaveClass(/notes-ink-layer--drawing/);
}

async function disableNotesInk(page: Page): Promise<void> {
  const toggle = page.locator(".notes-ink-toggle");
  if ((await toggle.getAttribute("aria-pressed")) === "true") await toggle.click();
  await expect(page.locator(NOTES_INK)).not.toHaveClass(/notes-ink-layer--drawing/);
}

async function chooseBlueThick(page: Page): Promise<void> {
  await page.locator('.notes-ink-tools__swatch[data-color="#2563eb"]').click();
  await page.locator('.notes-ink-tools__size[data-size="16"]').click();
}

async function line(page: Page, x0: number, y0: number, x1: number, y1: number): Promise<void> {
  await page.mouse.move(x0, y0);
  await page.mouse.down();
  await page.mouse.move(x1, y1, { steps: 14 });
  await page.mouse.up();
}

function inkFor(title: string): { data: string } | undefined {
  return query<{ data: string }>(main, "SELECT i.data FROM todo_notes_ink i JOIN todos t ON t.id = i.todo_id WHERE t.title = ?", title)[0];
}

function inkStroke(points: number[][], id = "s1", color = "#2563eb", size = 6) {
  return { id, tool: "pen", color, size, opacity: 1, points: points.map((p) => [p[0], p[1], 0.5]), createdAt: 1 };
}

test("1. empty Story Notes accept handwriting directly, with no drawing block, insert button, or full-screen editor", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Blank sheet", body: "" });
  await openStory(page, main, "Blank sheet");
  await expect(page.locator("#todoInsertDrawingBtn")).toHaveCount(0);
  await enableNotesInk(page);
  await expect(page.locator(".notes-ink-tools")).toBeVisible();
  const scroller = (await page.locator(NOTES_SCROLLER).boundingBox())!;
  expect(scroller.height).toBeGreaterThan(250);
  await drawStroke(page, NOTES_SCROLLER, [0.2, 0.3], [0.7, 0.7]);
  await expect(inkPaths(page)).toHaveCount(1);
  expect(await editorText(page)).toBe("");
  await expect(page.locator(".drawing-modal")).toHaveCount(0);
  await expect(page.locator("#todoBodyEditor .todo-notes-drawing")).toHaveCount(0);
  await saveStory(page);
  expect(await storyBody("Blank sheet")).toBe("");
  const stored = JSON.parse(inkFor("Blank sheet")!.data);
  const first = stored.strokes[0].points[0];
  expect(Math.abs(first[0] - scroller.width * 0.2)).toBeLessThan(4);
  expect(Math.abs(first[1] - scroller.height * 0.3)).toBeLessThan(4);

  await page.reload();
  await openStory(page, main, "Blank sheet");
  await expect(page.locator(".notes-ink-toggle")).toBeEnabled();
  await expect(inkPaths(page)).toHaveCount(1);
  const reopened = (await page.locator(NOTES_SCROLLER).boundingBox())!;
  const pathBox = (await inkPaths(page).first().boundingBox())!;
  expect(Math.abs(pathBox.x - (reopened.x + first[0] - stored.strokes[0].size / 2))).toBeLessThan(3);
  expect(Math.abs(pathBox.y - (reopened.y + first[1] - stored.strokes[0].size / 2))).toBeLessThan(3);
});

test("2. text stays visible under ink, modes switch without losing ink, and typing works with drawing off", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Text and ink", body: "Alpha line\nBeta line\nGamma line" });
  await openStory(page, main, "Text and ink");
  await expect(page.locator(".notes-ink-toggle")).toBeEnabled();
  const firstLine = page.locator("#todoBodyEditor .cm-line").first();
  const blur = () => page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await enableNotesInk(page);
  await chooseBlueThick(page);
  await page.waitForTimeout(200);
  const lineBox = (await firstLine.boundingBox())!;
  await blur();
  const before = await page.screenshot({ clip: lineBox });
  await expect(page.locator(NOTES_INK)).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  const scroller = (await page.locator(NOTES_SCROLLER).boundingBox())!;
  const secondLine = (await page.locator("#todoBodyEditor .cm-line").nth(1).boundingBox())!;
  const strokeY = secondLine.y + secondLine.height / 2;
  await line(page, scroller.x + 20, strokeY, scroller.x + 220, strokeY);
  await expect(inkPaths(page)).toHaveCount(1);
  await blur();
  const after = await page.screenshot({ clip: lineBox });
  expect(Buffer.compare(before, after)).toBe(0);
  await expect(page.locator("#todoBodyEditor .cm-line").nth(1)).toBeVisible();
  expect(isColor(await pixelAt(page, scroller.x + 120, strokeY), BLUE)).toBe(true);

  await disableNotesInk(page);
  await expect(inkPaths(page)).toHaveCount(1);
  await enableNotesInk(page);
  await line(page, scroller.x + 40, scroller.y + scroller.height - 60, scroller.x + 300, scroller.y + scroller.height - 50);
  await expect(inkPaths(page)).toHaveCount(2);
  await disableNotesInk(page);
  await firstLine.click({ position: { x: lineBox.width - 2, y: lineBox.height / 2 } });
  await page.keyboard.press("End");
  await page.keyboard.type(" typed");
  expect(await editorText(page)).toBe("Alpha line typed\nBeta line\nGamma line");
  await expect(inkPaths(page)).toHaveCount(2);
  await saveStory(page);
  expect(await storyBody("Text and ink")).toBe("Alpha line typed\nBeta line\nGamma line");
  expect(JSON.parse(inkFor("Text and ink")!.data).strokes).toHaveLength(2);
});

test("3. long Notes scroll with ink attached to document coordinates", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  const body = Array.from({ length: 90 }, (_, i) => `Line ${i + 1}`).join("\n");
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Long scroll", body });
  await openStory(page, main, "Long scroll");
  await enableNotesInk(page);
  const scroller = page.locator(NOTES_SCROLLER);
  const box = (await scroller.boundingBox())!;
  await line(page, box.x + 40, box.y + 60, box.x + 260, box.y + 70);
  const top = (await inkPaths(page).first().boundingBox())!;
  await scroller.evaluate((el) => {
    el.scrollTop = 300;
  });
  await page.waitForTimeout(100);
  const scrolled = (await inkPaths(page).first().boundingBox())!;
  expect(Math.abs(top.y - scrolled.y - 300)).toBeLessThan(2);
  await line(page, box.x + 40, box.y + 100, box.x + 260, box.y + 110);
  await saveStory(page);
  const stored = JSON.parse(inkFor("Long scroll")!.data);
  expect(stored.strokes).toHaveLength(2);
  expect(stored.strokes[1].points[0][1]).toBeGreaterThan(380);
  expect(stored.strokes[0].points[0][1]).toBeLessThan(80);
  expect(stored.height).toBeGreaterThan(400);
});

test("4. handwriting draws over embedded images", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  const asset = await uploadImage(main, "sketch.png", makePng(320, 200, 3));
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Image sketch", body: `Before\n\n![shot](${asset})\n\nAfter` });
  await openStory(page, main, "Image sketch");
  const img = page.locator("#todoBodyEditor .todo-notes-image img").first();
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true);
  await enableNotesInk(page);
  await chooseBlueThick(page);
  await page.waitForTimeout(200);
  const imgBox = (await img.boundingBox())!;
  const y = imgBox.y + imgBox.height / 2;
  await line(page, imgBox.x - 6, y, imgBox.x + imgBox.width + 20, y);
  await expect(inkPaths(page)).toHaveCount(1);
  expect(isColor(await pixelAt(page, imgBox.x + imgBox.width / 2, y), BLUE)).toBe(true);
  await saveStory(page);
  expect(JSON.parse(inkFor("Image sketch")!.data).strokes).toHaveLength(1);
  expect(await storyBody("Image sketch")).toBe(`Before\n\n![shot](${asset})\n\nAfter`);
});

test("5. cancelling a Story edit persists no ink", async ({ page }) => {
  await signIn(page);
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Cancel me", body: "draft" });
  await openStory(page, main, "Cancel me");
  await enableNotesInk(page);
  await drawStroke(page, NOTES_SCROLLER, [0.2, 0.5], [0.6, 0.6]);
  await expect(inkPaths(page)).toHaveCount(1);
  await page.locator("#closeTodoBtn").click();
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(inkFor("Cancel me")).toBeUndefined();
  await openStory(page, main, "Cancel me");
  await expect(page.locator(".notes-ink-toggle")).toBeEnabled();
  await expect(inkPaths(page)).toHaveCount(0);
});

test("6. a failed Save leaves both the notes text and the ink unchanged", async ({ page }) => {
  await signIn(page);
  await owner.json("POST", `/api/board/${slug}/todos`, {
    title: "Failing save",
    body: "original",
    notesInk: { version: 1, width: 600, height: 200, strokes: [inkStroke([[20, 20], [200, 40]])] },
  });
  const beforeInk = inkFor("Failing save")!.data;
  await openStory(page, main, "Failing save");
  await expect(inkPaths(page)).toHaveCount(1);
  await enableNotesInk(page);
  await drawStroke(page, NOTES_SCROLLER, [0.2, 0.6], [0.7, 0.7]);
  await disableNotesInk(page);
  await page.locator("#todoBodyEditor .cm-line").first().click();
  await page.keyboard.press("End");
  await page.keyboard.type(" changed");
  await page.route(`**/api/board/${slug}/todos/*`, async (route) => {
    if (route.request().method() !== "PATCH") return route.continue();
    const payload = JSON.parse(route.request().postData() ?? "{}");
    payload.notesInk.strokes[0].color = "url(#x)";
    await route.continue({ postData: JSON.stringify(payload) });
  });
  const failed = page.waitForResponse((r) => r.request().method() === "PATCH" && r.url().includes("/todos/"));
  await page.locator("#saveTodoBtn").click();
  expect((await failed).status()).toBe(400);
  await expect(page.locator("#todoDialog")).toBeVisible();
  expect(await storyBody("Failing save")).toBe("original");
  expect(inkFor("Failing save")!.data).toBe(beforeInk);
  await page.unroute(`**/api/board/${slug}/todos/*`);
  await saveStory(page);
  expect(await storyBody("Failing save")).toBe("original changed");
  expect(JSON.parse(inkFor("Failing save")!.data).strokes).toHaveLength(2);
});

test("7. read-only members and public visitors see the ink without drawing controls", async ({ page, browser }) => {
  await owner.json("POST", `/api/board/${slug}/todos`, {
    title: "Shared ink",
    body: "Public notes line",
    notesInk: { version: 1, width: 600, height: 200, strokes: [inkStroke([[20, 30], [260, 60]])] },
  });
  const viewerContext = await browser.newContext();
  await viewerContext.addCookies(toBrowserCookies(viewer.cookies()));
  const viewerPage = await viewerContext.newPage();
  await openStory(viewerPage, main, "Shared ink");
  await expect(viewerPage.locator(`${NOTES_INK} g.drawing-strokes path`)).toHaveCount(1);
  await expect(viewerPage.locator(".notes-ink-toggle")).toBeHidden();
  await expect(viewerPage.locator(NOTES_INK)).not.toHaveClass(/notes-ink-layer--drawing/);
  await viewerContext.close();

  setPublication(main, slug, true);
  const anon = await browser.newContext();
  const pub = await anon.newPage();
  await pub.goto(`${main.url}/${slug}`);
  await pub.locator("[data-public-local-id]", { hasText: "Shared ink" }).click();
  const surface = pub.locator("#publicTodoDialog .public-todo__notes--surface");
  await expect(surface.locator("svg.notes-ink-layer g.drawing-strokes path")).toHaveCount(1);
  await expect(surface.locator(".cm-content")).toContainText("Public notes line");
  await expect(pub.locator("#publicTodoDialog .notes-ink-toggle")).toHaveCount(0);
  await expect(surface.locator("svg.notes-ink-layer")).not.toHaveClass(/notes-ink-layer--drawing/);
  await expect(surface.locator(".cm-content")).toHaveAttribute("contenteditable", "false");
  await anon.close();
  setPublication(main, slug, false);
});

test("8. archive export and import restore Notes ink on the imported stories", async ({ page }) => {
  await signIn(page);
  const exported = await fetch(`${main.url}/api/backup/export`, { headers: { Cookie: owner.cookieHeader() } });
  expect(exported.status).toBe(200);
  expect(exported.headers.get("content-type")).toBe("application/zip");
  const archive = Buffer.from(await exported.arrayBuffer());
  const form = new FormData();
  form.append("importMode", "copy");
  form.append("archive", new Blob([archive], { type: "application/zip" }), "backup.sbz");
  const imported = await fetch(`${main.url}/api/backup/import`, { method: "POST", headers: { "X-Scrumboy": "1", Cookie: owner.cookieHeader() }, body: form });
  expect(imported.status).toBe(200);
  const rows = query<{ slug: string; data: string }>(main, "SELECT p.slug, i.data FROM todo_notes_ink i JOIN todos t ON t.id = i.todo_id JOIN projects p ON p.id = t.project_id WHERE t.title = 'Text and ink'");
  expect(rows.length).toBe(2);
  const copy = rows.find((r) => r.slug !== slug)!;
  expect(JSON.parse(copy.data).strokes).toHaveLength(2);
  await page.goto(`${main.url}/${copy.slug}`);
  await page.locator(".card[data-todo-local-id]", { hasText: "Text and ink" }).first().click();
  await expect(page.locator(`${NOTES_INK} g.drawing-strokes path`)).toHaveCount(2);
});

test("9. stored image annotations and standalone drawings still render, with no editing entry points", async ({ page }) => {
  await signIn(page);
  const asset = await uploadImage(main, "legacy.png", makePng(300, 200, 4));
  const a = "drw_legacyaaaa01";
  const b = "drw_legacybbbb02";
  const doc = (color: string) => ({ version: 1, width: 300, height: 200, strokes: [inkStroke([[10, 10], [200, 150]], "x1", color, 4)] });
  await owner.json("POST", `/api/board/${slug}/todos`, {
    title: "Annotated",
    body: `![shot|draw:${a}|300](${asset})\n\n![Sketch|draw:${b}](/api/board/${slug}/drawings/${b})`,
    drawings: { [a]: doc("#e11d48"), [b]: doc("#16a34a") },
    notesInk: { version: 1, width: 600, height: 300, strokes: [inkStroke([[30, 30], [300, 60]])] },
  });
  await openStory(page, main, "Annotated");
  await expect(page.locator("#todoBodyEditor svg.todo-notes-image__annotation-layer path")).toHaveCount(1);
  await expect(page.locator("#todoBodyEditor .todo-notes-drawing svg path")).toHaveCount(1);
  await expect(inkPaths(page)).toHaveCount(1);
  await expect(page.locator("#todoBodyEditor .todo-notes-image__btn--draw")).toHaveCount(0);
  await expect(page.locator("#todoInsertDrawingBtn")).toHaveCount(0);
  await page.locator("#todoBodyEditor .todo-notes-drawing").dblclick();
  await expect(page.locator(".drawing-modal")).toHaveCount(0);
});

test("10. mobile viewport (simulated touch): Notes handwriting works with touch-sized controls", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
  await context.addCookies(toBrowserCookies(owner.cookies()));
  const page = await context.newPage();
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Phone notes", body: "Short" });
  await page.goto(`${main.url}/${slug}`);
  await page.locator(".card[data-todo-local-id]", { hasText: "Phone notes" }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  const toggle = page.locator(".notes-ink-toggle");
  await expect(toggle).toBeEnabled();
  const toggleBox = (await toggle.boundingBox())!;
  expect(toggleBox.width).toBeGreaterThanOrEqual(40);
  await toggle.tap();
  await expect(page.locator(NOTES_INK)).toHaveClass(/notes-ink-layer--drawing/);
  await expect(page.locator(NOTES_INK)).toHaveCSS("touch-action", "none");
  await page.locator(NOTES_INK).evaluate((el) => {
    const r = el.getBoundingClientRect();
    const fire = (type: string, x: number, y: number) =>
      el.dispatchEvent(new PointerEvent(type, { pointerId: 21, pointerType: "touch", isPrimary: true, clientX: r.left + x, clientY: r.top + y, bubbles: true, cancelable: true }));
    fire("pointerdown", 30, 60);
    for (let i = 1; i <= 10; i += 1) fire("pointermove", 30 + i * 15, 60 + i * 6);
    fire("pointerup", 180, 120);
  });
  await expect(page.locator(`${NOTES_INK} g.drawing-strokes path`)).toHaveCount(1);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect(JSON.parse(inkFor("Phone notes")!.data).strokes).toHaveLength(1);
  await context.close();
});

test("11. Wall ink stays above every kind of card and connector", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await signIn(page);
  const board = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Layer Board" });
  const story = await owner.json<{ localId: number }>("POST", `/api/board/${board.slug}/todos`, { title: "Pinned card" });
  const note = (x: number, text: string) => owner.json<{ id: string }>("POST", `/api/board/${board.slug}/wall/notes`, { x, y: 120, width: 180, height: 140, color: "#ffd966", text });
  const plain = await note(60, "plain");
  const selected = await note(320, "selected");
  const dragged = await note(900, "dragged");
  await owner.json("POST", `/api/board/${board.slug}/wall/stories`, { localId: story.localId, x: 580, y: 120 });
  await owner.json("POST", `/api/board/${board.slug}/wall/edges`, { from: plain.id, to: selected.id });
  await page.goto(`${main.url}/${board.slug}`);
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDialog")).toBeVisible();
  const noteEl = (id: string) => page.locator(`.wall-note[data-note-id="${id}"]`);
  await expect(noteEl(plain.id)).toBeVisible();
  await noteEl(selected.id).click({ modifiers: ["Control"] });
  await expect(noteEl(selected.id)).toHaveClass(/wall-note--selected/);
  await page.locator(".wall-story").first().focus();
  await noteEl(dragged.id).evaluate((el) => el.classList.add("wall-note--dragging"));
  await page.locator(".wall-draw-toggle-btn").click();
  await page.locator('.wall-draw-palette__swatch[data-color="#2563eb"]').click();
  await page.locator('.wall-draw-palette__size[data-size="16"]').click();
  const plainBox = (await noteEl(plain.id).boundingBox())!;
  const y = plainBox.y + plainBox.height / 2;
  const draggedBox = (await noteEl(dragged.id).boundingBox())!;
  const commit = page.waitForResponse((r) => r.url().endsWith(`/api/board/${board.slug}/wall/drawings`) && r.request().method() === "POST");
  await line(page, plainBox.x - 30, y, draggedBox.x + draggedBox.width + 30, y);
  expect((await commit).status()).toBe(200);
  const selectedBox = (await noteEl(selected.id).boundingBox())!;
  const storyBox = (await page.locator(".wall-story").first().boundingBox())!;
  const samples: Array<[string, number]> = [
    ["plain note", plainBox.x + plainBox.width / 2],
    ["connector between notes", (plainBox.x + plainBox.width + selectedBox.x) / 2],
    ["selected note", selectedBox.x + selectedBox.width / 2],
    ["focused story card", storyBox.x + storyBox.width / 2],
    ["dragged note", draggedBox.x + draggedBox.width / 2],
  ];
  for (const [name, x] of samples) {
    const px = await pixelAt(page, x, y);
    expect(isColor(px, BLUE), `${name} pixel ${px.join(",")}`).toBe(true);
  }
  const before = await pixelAt(page, storyBox.x + storyBox.width / 2, y);
  await page.locator(".wall-draw-toggle-btn").click();
  await page.mouse.move(storyBox.x + storyBox.width / 2, y);
  expect(await pixelAt(page, storyBox.x + storyBox.width / 2, y)).toEqual(before);
  const closeBox = (await page.locator("#closeWallBtn").boundingBox())!;
  const topAtClose = await page.evaluate(([cx, cy]) => document.elementFromPoint(cx, cy)?.id ?? "", [closeBox.x + closeBox.width / 2, closeBox.y + closeBox.height / 2]);
  expect(topAtClose).toBe("closeWallBtn");
});

test("12. Wall: draw, keep alignment through zoom, erase, and keep note interactions", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  await page.goto(`${main.url}/${slug}`);
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDialog")).toBeVisible();
  await page.locator(".wall-draw-toggle-btn").click();
  await expect(page.locator(".wall-draw-capture")).toBeVisible();
  const commit = page.waitForResponse((r) => r.url().endsWith("/wall/drawings") && r.request().method() === "POST");
  await drawStroke(page, ".wall-draw-capture", [0.3, 0.4], [0.5, 0.45]);
  expect((await commit).status()).toBe(200);
  const path = page.locator("#wallDrawingOverlay .wall-drawing-layer path");
  await expect(path).toHaveCount(1);
  const surface = (await page.locator("#wallSurface").boundingBox())!;
  const before = (await path.boundingBox())!;
  await page.mouse.move(surface.x + surface.width / 2, surface.y + surface.height / 2);
  await page.mouse.wheel(0, 120);
  await page.waitForTimeout(200);
  const panned = (await path.boundingBox())!;
  expect(Math.abs(panned.y - (before.y - 120))).toBeLessThan(1);
  expect(Math.abs(panned.width - before.width)).toBeLessThan(0.5);
  await page.keyboard.down("Control");
  for (let i = 0; i < 4; i += 1) await page.mouse.wheel(0, -100);
  await page.keyboard.up("Control");
  await page.waitForTimeout(200);
  const after = (await path.boundingBox())!;
  expect(after.width).toBeGreaterThan(before.width * 1.2);
  const matrix = await page.locator(".wall-content").evaluate((el) => new DOMMatrixReadOnly(getComputedStyle(el).transform).a);
  const stored = query<{ drawings: string }>(main, "SELECT pw.drawings FROM project_walls pw JOIN projects p ON p.id = pw.project_id WHERE p.slug = ?", slug)[0].drawings;
  const stroke = JSON.parse(stored).find((s: any) => !s.deleted);
  const xs = stroke.points.map((p: number[]) => p[0]);
  const worldSpan = Math.max(...xs) - Math.min(...xs) + stroke.size;
  expect(Math.abs(after.width - worldSpan * matrix)).toBeLessThan(4);

  await page.reload();
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-layer path")).toHaveCount(1);
  await page.locator(".wall-draw-toggle-btn").click();
  await page.locator(".wall-draw-palette__tool--eraser").click();
  const box = (await page.locator("#wallDrawingOverlay .wall-drawing-layer path").boundingBox())!;
  const erase = page.waitForResponse((r) => r.url().endsWith("/wall/drawings") && r.request().method() === "POST");
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  expect((await erase).status()).toBe(200);
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-layer path")).toHaveCount(0);

  await page.locator(".wall-draw-toggle-btn").click();
  await expect(page.locator(".wall-draw-capture")).toBeHidden();
  const created = page.waitForResponse((r) => r.url().endsWith("/wall/notes") && r.request().method() === "POST");
  await page.mouse.click(surface.x + surface.width * 0.6, surface.y + surface.height * 0.6, { button: "right" });
  expect((await created).status()).toBe(201);
});

test("13. Wall durability: failed saves keep strokes, close is guarded, and a late Board A response never touches Board B", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await signIn(page);
  const other = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Second Board" });
  const drawingsUrl = (s: string) => `**/api/board/${s}/wall/drawings`;
  let releaseFirst: () => void = () => undefined;
  const firstHeld = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  let intercepted = 0;
  await page.route(drawingsUrl(slug), async (route) => {
    intercepted += 1;
    if (intercepted === 1) await firstHeld;
    await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { message: "boom" } }) });
  });
  await page.goto(`${main.url}/${slug}`);
  await page.locator("#wallBtn").click();
  await page.locator(".wall-draw-toggle-btn").click();
  await expect(page.locator(".wall-draw-toggle-btn svg.lucide-pencil")).toBeVisible();
  await drawStroke(page, ".wall-draw-capture", [0.3, 0.6], [0.45, 0.62]);
  await expect.poll(() => intercepted).toBe(1);
  await page.locator("#closeWallBtn").click();
  await expect(page.locator("#confirmDialogConfirm")).toBeVisible();
  await page.locator("#confirmDialogCancel").click();
  await expect(page.locator("#wallDialog")).toBeVisible();
  releaseFirst();
  await expect(page.locator(".wall-draw-status")).toBeVisible({ timeout: 15_000 });
  expect(intercepted).toBe(3);
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-active path[data-stroke-id]")).toHaveCount(1);
  await page.unroute(drawingsUrl(slug));
  const saved = page.waitForResponse((r) => r.url().endsWith(`/api/board/${slug}/wall/drawings`) && r.request().method() === "POST");
  await page.locator(".wall-draw-status__retry").click();
  expect((await saved).status()).toBe(200);
  await expect(page.locator(".wall-draw-status")).toBeHidden();
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-active path[data-stroke-id]")).toHaveCount(0);
  const savedCount = await page.locator("#wallDrawingOverlay .wall-drawing-layer path").count();
  expect(savedCount).toBeGreaterThan(0);

  let releaseLate: () => void = () => undefined;
  const lateHeld = new Promise<void>((resolve) => {
    releaseLate = resolve;
  });
  let lateSeen = false;
  let lateDone = false;
  await page.route(drawingsUrl(slug), async (route) => {
    lateSeen = true;
    await lateHeld;
    await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: { message: "stale" } }) });
    lateDone = true;
  });
  const boardBWrites: string[] = [];
  const boardAWallReads: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes(`/api/board/${other.slug}/wall/drawings`)) boardBWrites.push(req.method());
    if (req.method() === "GET" && req.url().endsWith(`/api/board/${slug}/wall`)) boardAWallReads.push(req.url());
  });
  await drawStroke(page, ".wall-draw-capture", [0.6, 0.3], [0.7, 0.35]);
  await expect.poll(() => lateSeen).toBe(true);
  await page.locator("#closeWallBtn").click();
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator("#wallDialog")).toBeHidden();
  await page.evaluate((s) => {
    window.history.pushState({}, "", `/${s}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, other.slug);
  await expect(page.locator("#wallBtn")).toBeVisible();
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDialog")).toBeVisible();
  const readsBeforeRelease = boardAWallReads.length;
  releaseLate();
  await expect.poll(() => lateDone).toBe(true);
  await page.waitForTimeout(1500);
  expect(boardBWrites).toEqual([]);
  expect(boardAWallReads.length).toBe(readsBeforeRelease);
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-layer path")).toHaveCount(0);
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-active path")).toHaveCount(0);
  await expect(page.locator(".wall-draw-status")).toBeHidden();
  const otherWall = query<{ drawings: string }>(main, "SELECT pw.drawings FROM project_walls pw JOIN projects p ON p.id = pw.project_id WHERE p.slug = ?", other.slug);
  expect(otherWall.length === 0 || JSON.parse(otherWall[0].drawings).length === 0).toBe(true);
  await page.unroute(drawingsUrl(slug));
});

test("14. mobile viewport (simulated touch): the Wall is reachable and touch strokes persist", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
  await context.addCookies(toBrowserCookies(owner.cookies()));
  const page = await context.newPage();
  await page.goto(`${main.url}/${slug}`);
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDialog")).toBeVisible();
  await page.locator(".wall-draw-toggle-btn").click();
  await page.waitForTimeout(300);
  const before = await page.locator("#wallDrawingOverlay .wall-drawing-layer path").count();
  const commit = page.waitForResponse((r) => r.url().endsWith("/wall/drawings") && r.request().method() === "POST");
  await page.locator(".wall-draw-capture").evaluate((el) => {
    const r = el.getBoundingClientRect();
    const fire = (type: string, x: number, y: number) =>
      el.dispatchEvent(new PointerEvent(type, { pointerId: 11, pointerType: "touch", isPrimary: true, clientX: r.left + x, clientY: r.top + y, bubbles: true, cancelable: true }));
    fire("pointerdown", 80, 200);
    for (let i = 1; i <= 10; i += 1) fire("pointermove", 80 + i * 12, 200 + i * 4);
    fire("pointerup", 200, 240);
  });
  expect((await commit).status()).toBe(200);
  await expect(page.locator("#wallDrawingOverlay .wall-drawing-layer path")).toHaveCount(before + 1);
  await context.close();
});

test("15. disabling drawing hides the UI but preserves every stored drawing and Notes ink", async ({ page }) => {
  const storyRows = query<{ n: number }>(main, "SELECT COUNT(*) AS n FROM todo_drawings")[0].n;
  const inkBefore = query<{ todo_id: number; data: string }>(main, "SELECT todo_id, data FROM todo_notes_ink ORDER BY todo_id");
  expect(inkBefore.length).toBeGreaterThan(0);
  const wallBefore = query<{ drawings: string }>(main, "SELECT drawings FROM project_walls")[0].drawings;
  expect(storyRows).toBeGreaterThan(0);
  const dataDir = main.dataDir;
  await stopInstance(main);
  instances.splice(instances.indexOf(main), 1);
  const off = await startInstance({ SCRUMBOY_DRAWING_ENABLED: "0", SCRUMBOY_WALL_DRAWING_ENABLED: "0" }, dataDir);
  main = off;
  owner = apiClient(off.url);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  await signIn(page);
  await openStory(page, off, "Annotated");
  const widget = page.locator("#todoBodyEditor .todo-notes-image[data-path]").first();
  await expect(widget.locator("img")).toBeVisible();
  await expect(widget.locator("svg")).toHaveCount(0);
  await expect(page.locator("#todoBodyEditor svg.notes-ink-layer g.drawing-strokes path")).toHaveCount(0);
  await expect(page.locator(".notes-ink-toggle:visible")).toHaveCount(0);
  await expect(page.locator("#todoInsertDrawingBtn")).toHaveCount(0);
  await page.locator("#todoTitle").fill("Annotated renamed");
  await saveStory(page);
  expect(query<{ n: number }>(off, "SELECT COUNT(*) AS n FROM todo_drawings")[0].n).toBe(storyRows);
  expect(query<{ todo_id: number; data: string }>(off, "SELECT todo_id, data FROM todo_notes_ink ORDER BY todo_id")).toEqual(inkBefore);
  expect(query<{ drawings: string }>(off, "SELECT drawings FROM project_walls")[0].drawings).toBe(wallBefore);
  const wallPost = await fetch(`${off.url}/api/board/${slug}/wall/drawings`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Scrumboy": "1", Cookie: owner.cookieHeader() },
    body: JSON.stringify({ baseVersion: 0, erase: ["x"] }),
  });
  expect(wallPost.status).toBe(404);
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
      void 0;
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
