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
import { expect, test, type Browser, type BrowserContext, type BrowserContextOptions, type Page, type Request } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const OWNER = { email: "public-owner-e2e@example.com", password: "password123", name: "Owner Secret Name" };
const OUTSIDER = { email: "public-outsider-e2e@example.com", password: "password123", name: "Outsider" };

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let publicSlug = "";
let privateSlug = "";
/** Published board with sprints disabled and no priority tiers: the public filter toggle starts hidden. */
let bareSlug = "";
/** Published board whose tags overflow the desktop Omni rail; tag k is on (TAG_NAMES.length - k) stories. */
let tagSlug = "";
const TAG_NAMES = [
  "alpha-component-library", "beta-design-system", "gamma-performance", "delta-accessibility",
  "epsilon-localization", "zeta-observability", "eta-infrastructure", "theta-documentation",
  "iota-release-process", "kappa-customer-support", "lambda-data-pipeline", "mu-security-review",
  "nu-onboarding-flow", "xi-final-tag", "erp系统流程", "つながるひろが",
];
/** Published board for card layout: many tag-heavy cards, one maximum-length unbroken tag, and more than one lane page. */
let cardSlug = "";
const CARD_LAYOUT_STORIES = 24;
const CARD_LAYOUT_TAGS = [
  "frontend-platform", "design-review", "performance-budget", "accessibility-audit", "localization-sweep",
  "observability-gap", "infrastructure-cost", "documentation-debt", "release-train", "customer-escalation",
  "data-pipeline-fix", "security-review",
];
/** Tags allow up to 32 Unicode code points; 32 narrow ASCII characters remain a useful unbroken layout case. */
const LONGEST_TAG = "abcdefghijklmnopqrstuvwxyz012345";
// Tag names are canonicalized server-side (Unicode letters/numbers/marks plus ASCII
// hyphens), so hostile punctuation cannot reach a public board end to end; escaping
// remains unit-tested.

function setPublication(slug: string, enabled: boolean): void {
  const db = new DatabaseSync(path.join(dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.prepare("UPDATE projects SET public_view_enabled = ? WHERE slug = ?").run(enabled ? 1 : 0, slug);
  } finally {
    db.close();
  }
}

function stripSprintsAndPriorities(slug: string): void {
  const db = new DatabaseSync(path.join(dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.prepare("UPDATE projects SET sprints_enabled = 0 WHERE slug = ?").run(slug);
    db.prepare("DELETE FROM project_priorities WHERE project_id = (SELECT id FROM projects WHERE slug = ?)").run(slug);
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
      // Lets the Omni tests below present distinct client addresses (X-Forwarded-For) so their
      // traffic does not exhaust the per-IP public read limit (120/min) shared by the other tests.
      SCRUMBOY_TRUST_PROXY: "1",
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
  const bare = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Bare Public" });
  bareSlug = bare.slug;
  await owner.json("POST", `/api/board/${bareSlug}/todos`, { title: "Bare story" });
  stripSprintsAndPriorities(bareSlug);
  setPublication(bareSlug, true);
  const tagged = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Tagged Public" });
  tagSlug = tagged.slug;
  for (let j = 0; j < TAG_NAMES.length; j++) {
    await owner.json("POST", `/api/board/${tagSlug}/todos`, { title: `Tagged story ${j}`, tags: TAG_NAMES.slice(0, TAG_NAMES.length - j) });
  }
  setPublication(tagSlug, true);
  const cards = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Card Layout Public" });
  cardSlug = cards.slug;
  for (let j = 0; j < CARD_LAYOUT_STORIES; j++) {
    const body: Record<string, unknown> = j === 2
      ? { title: "Longest tag story", tags: [LONGEST_TAG] }
      : j < 10
        ? { title: `Many tags story ${j}`, tags: CARD_LAYOUT_TAGS.slice(0, CARD_LAYOUT_TAGS.length - (j % 6)), priorityKey: "high", estimationPoints: 8 }
        : { title: `Filler story ${j}`, tags: ["ui"] };
    await owner.json("POST", `/api/board/${cardSlug}/todos`, body);
  }
  setPublication(cardSlug, true);
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

test("a sprint created by a maintainer reloads the public vocabulary but never adds a Sprint filter section", async ({ page }) => {
  const api = trackApi(page);
  await page.goto(`${baseUrl}/${publicSlug}`);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#publicFilterPanel [data-public-sprint-section]")).toHaveCount(0);

  const owner = apiClient(baseUrl);
  await owner.json("POST", "/api/auth/login", { email: OWNER.email, password: OWNER.password });
  const vocabulary = waitForPublicSprints(page, publicSlug, "Live Sprint");
  const start = Date.now();
  await owner.json("POST", `/api/board/${publicSlug}/sprints`, {
    name: "Live Sprint",
    plannedStartAt: start,
    plannedEndAt: start + 7 * 24 * 60 * 60 * 1000,
  });
  await vocabulary;
  await page.locator("#publicFilterToggle").click();
  await expect(page.locator("#publicFilterPanel [data-public-priority-option]").first()).toBeVisible();
  await expect(page.locator("#publicFilterPanel [data-public-sprint-section], #publicFilterPanel [data-public-sprint-option]")).toHaveCount(0);
  await expect(page.locator("#publicFilterPanel")).not.toContainText("Live Sprint");
  expect(api.filter((p) => !p.startsWith(`/api/public/board/${publicSlug}`) && p !== "/api/auth/status")).toEqual([`/api/board/${publicSlug}`]);
});

test("a public sprint-number deep link stays applied and is removable outside the popover", async ({ page }) => {
  const sprints = await apiClient(baseUrl).json<{ sprints: { number: number; name: string }[] }>("GET", `/api/public/board/${publicSlug}/sprints`);
  const live = sprints.sprints.find((sprint) => sprint.name === "Live Sprint")!;
  const urls = trackApiUrls(page);
  await page.goto(`${baseUrl}/${publicSlug}?sprintNumber=${live.number}&priority=high`);
  const pill = page.locator("#publicOmniPinnedTags .omni-tag-pill--applied", { hasText: "Live Sprint" });
  await expect(pill).toBeVisible({ timeout: 30_000 });
  const snapshotUrls = () => urls.filter((u) => u.startsWith(`/api/public/board/${publicSlug}?`)).map((u) => new URL(u, baseUrl));
  expect(snapshotUrls().at(-1)!.searchParams.get("sprintNumber")).toBe(String(live.number));
  await page.locator("#publicFilterToggle").click();
  await expect(page.locator("#publicFilterPanel [data-public-sprint-option], #publicFilterPanel [data-public-clear-sprint]")).toHaveCount(0);
  await page.keyboard.press("Escape");

  await pill.locator("[data-public-clear-sprint]").click();
  await expect(pill).toHaveCount(0);
  await expect(page).not.toHaveURL(/sprintNumber=/);
  await expect(page).toHaveURL(/[?&]priority=high/);
  await expect.poll(() => snapshotUrls().at(-1)!.searchParams.has("sprintNumber")).toBe(false);
  expect(snapshotUrls().at(-1)!.searchParams.get("priority")).toBe("high");
});

type Box = { x: number; y: number; width: number; height: number };

function overlaps(a: Box, b: Box): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

function contains(outer: Box, inner: Box): boolean {
  return inner.x >= outer.x && inner.y >= outer.y && inner.x + inner.width <= outer.x + outer.width && inner.y + inner.height <= outer.y + outer.height;
}

async function focusedId(page: Page): Promise<string> {
  return page.evaluate(() => (document.activeElement as HTMLElement | null)?.id ?? "");
}

async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
}

/** Full API URLs (path + query) so filter parameters can be inspected. */
function trackApiUrls(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (req: Request) => {
    const u = new URL(req.url());
    if (u.pathname.startsWith("/api/")) seen.push(u.pathname + u.search);
  });
  return seen;
}

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

/** Resolves once the public sprint vocabulary for `slug` has been fetched and includes `name`. */
function waitForPublicSprints(page: Page, slug: string, name: string): Promise<unknown> {
  return page.waitForResponse(async (res) => {
    if (new URL(res.url()).pathname !== `/api/public/board/${slug}/sprints` || !res.ok()) return false;
    const body = (await res.json()) as { sprints?: { name: string }[] };
    return !!body.sprints?.some((sprint) => sprint.name === name);
  }, { timeout: 20_000 });
}

async function createSprint(client: ReturnType<typeof apiClient>, slug: string, name: string): Promise<void> {
  const start = Date.now();
  await client.json("POST", `/api/board/${slug}/sprints`, { name, plannedStartAt: start, plannedEndAt: start + 7 * 24 * 60 * 60 * 1000 });
}

test("hidden public filter toggle and clear button are invisible, unclickable, and skipped by Tab", async ({ page }) => {
  await page.goto(`${baseUrl}/${bareSlug}`);
  const input = page.locator("#publicSearchInput");
  const toggle = page.locator("#publicFilterToggle");
  const clear = page.locator("#publicSearchClear");
  await expect(input).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("[data-public-local-id]")).toHaveCount(1);

  const expectGone = async () => {
    for (const hidden of [toggle, clear]) {
      await expect(hidden).toHaveAttribute("hidden", "");
      await expect(hidden).toBeHidden();
      expect(await hidden.evaluate((el) => getComputedStyle(el).display)).toBe("none");
      expect(await hidden.boundingBox()).toBeNull();
    }
    // Where the clear button and chevron would sit, the input receives the click.
    const box = (await input.boundingBox())!;
    for (const offset of [10, 30]) {
      const hit = await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.id ?? "", [box.x + box.width - offset, box.y + box.height / 2]);
      expect(hit).toBe("publicSearchInput");
    }
    await input.focus();
    await page.keyboard.press("Tab");
    expect(["publicFilterToggle", "publicSearchClear"]).not.toContain(await focusedId(page));
  };

  // Initially hidden: no sprints, no priorities, no search text.
  await expectGone();

  // Clear button: hidden -> visible (reachable by Tab) -> hidden.
  await input.fill("Bare");
  await expect(clear).toBeVisible();
  await input.focus();
  await page.keyboard.press("Tab");
  expect(await focusedId(page)).toBe("publicSearchClear");
  await clear.click();
  await expect(clear).toBeHidden();
  expect(await focusedId(page)).toBe("publicSearchInput");

  // Toggle: sprints alone never reveal it, since the popover offers only Priority.
  const owner = await ownerClient();
  await owner.json("PATCH", `/api/board/${bareSlug}/settings`, { sprintsEnabled: true });
  const vocabulary = waitForPublicSprints(page, bareSlug, "Bare Sprint");
  await createSprint(owner, bareSlug, "Bare Sprint");
  await vocabulary;
  await expectGone();

  await owner.json("PATCH", `/api/board/${bareSlug}/settings`, { sprintsEnabled: false });
  await expectGone();
});

for (const width of [320, 390, 1280]) {
  test(`public filter popover at ${width}px: layout, selection, keyboard, and dismissal`, async ({ browser }) => {
    const mobile = width < 768;
    const height = mobile ? 700 : 800;
    const context = await browser.newContext({ viewport: { width, height }, isMobile: mobile, hasTouch: mobile });
    const page = await context.newPage();
    const urls = trackApiUrls(page);
    await page.goto(`${baseUrl}/${publicSlug}`);
    const input = page.locator("#publicSearchInput");
    const toggle = page.locator("#publicFilterToggle");
    const clear = page.locator("#publicSearchClear");
    const panel = page.locator("#publicFilterPanel");
    const cards = page.locator("[data-public-local-id]");
    await expect(cards).toHaveCount(2, { timeout: 30_000 });
    await expect(input).toBeVisible();
    await expect(toggle).toBeVisible();
    await expect(clear).toBeHidden();
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);

    // Typing filters while the input keeps focus; clear and chevron sit inside the input without overlapping.
    await input.click();
    await input.pressSequentially("Readable");
    await expect(cards).toHaveCount(1);
    await expect(clear).toBeVisible();
    expect(await focusedId(page)).toBe("publicSearchInput");
    await expect(input).toHaveValue("Readable");
    const [inputBox, clearBox, toggleBox] = [(await input.boundingBox())!, (await clear.boundingBox())!, (await toggle.boundingBox())!];
    expect(contains(inputBox, clearBox)).toBe(true);
    expect(contains(inputBox, toggleBox)).toBe(true);
    expect(overlaps(clearBox, toggleBox)).toBe(false);
    expect(inputBox.x).toBeGreaterThanOrEqual(0);
    expect(inputBox.x + inputBox.width).toBeLessThanOrEqual(width);
    for (const other of await page.locator(".topbar > :visible").all()) {
      const box = await other.boundingBox();
      if (!box || (await other.evaluate((el) => el.classList.contains("search-input-wrapper") || el.classList.contains("spacer")))) continue;
      expect(overlaps(box, inputBox), await other.evaluate((el) => el.outerHTML.slice(0, 80))).toBe(false);
    }
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    await clear.click();
    await expect(cards).toHaveCount(2);

    // The popover opens fully inside the viewport, below the chevron.
    await toggle.click();
    await expect(panel).toBeVisible();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    const panelBox = (await panel.boundingBox())!;
    const openToggleBox = (await toggle.boundingBox())!;
    expect(contains({ x: 0, y: 0, width, height }, panelBox)).toBe(true);
    expect(panelBox.y).toBeGreaterThanOrEqual(openToggleBox.y + openToggleBox.height);
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    await expect(panel.locator("[data-public-priority-option]")).toHaveCount(5);
    await expect(panel.locator(".search-filter-panel__section")).toHaveCount(1);
    await expect(panel.locator('[data-public-sprint-section], [data-public-sprint-option], [data-public-priority-option="**none**"], [data-assignee-option], [data-sort-option]')).toHaveCount(0);
    await expect(panel).not.toContainText("Live Sprint");

    // Outside click closes.
    await page.locator(".public-board__project-name").click();
    await expect(panel).toBeHidden();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");

    // Keyboard: Enter opens, Tab moves into the options, Escape closes and restores focus.
    await toggle.focus();
    await page.keyboard.press("Enter");
    await expect(panel).toBeVisible();
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => !!document.activeElement?.closest("#publicFilterPanel"))).toBe(true);
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
    expect(await focusedId(page)).toBe("publicFilterToggle");

    // Select a priority by keyboard: closes, focuses the chevron, marks it active.
    await page.keyboard.press("Enter");
    const high = panel.locator('[data-public-priority-option="high"]');
    await high.focus();
    await page.keyboard.press("Enter");
    await expect(panel).toBeHidden();
    expect(await focusedId(page)).toBe("publicFilterToggle");
    await expect(page).toHaveURL(/[?&]priority=high/);
    await expect(toggle).toHaveClass(/search-filter-toggle--active/);
    await expect(cards).toHaveCount(0);
    await toggle.click();
    await expect(high).toHaveClass(/is-active/);
    await expect(high).toHaveAttribute("aria-pressed", "true");

    // Clearing restores the unfiltered board and the idle chevron.
    await panel.locator('[data-public-priority-option=""]').click();
    await expect(cards).toHaveCount(2);
    await expect(page).not.toHaveURL(/priority=|sprintNumber=/);
    await expect(toggle).not.toHaveClass(/search-filter-toggle--active/);
    await toggle.click();
    await expect(panel.locator('[data-public-priority-option=""]')).toHaveAttribute("aria-pressed", "true");
    await page.keyboard.press("Escape");

    const nonPublic = urls.filter((u) => !u.startsWith(`/api/public/board/${publicSlug}`) && u !== "/api/auth/status");
    expect(nonPublic.map((u) => new URL(u, baseUrl).pathname)).toEqual([`/api/board/${publicSlug}`]);
    await context.close();
  });
}

test("public search keeps focus and text across filtering and a live refresh", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${publicSlug}`);
  const input = page.locator("#publicSearchInput");
  const cards = page.locator("[data-public-local-id]");
  await expect(cards).toHaveCount(2, { timeout: 30_000 });
  await input.click();
  await input.pressSequentially("Second");
  await expect(cards).toHaveCount(1);
  expect(await focusedId(page)).toBe("publicSearchInput");

  // A sprint change arrives over the stream: refresh + vocabulary reload, input untouched.
  const inputHandle = await input.elementHandle();
  const vocabulary = waitForPublicSprints(page, publicSlug, "Refresh Sprint");
  await createSprint(await ownerClient(), publicSlug, "Refresh Sprint");
  await vocabulary;
  expect(await focusedId(page)).toBe("publicSearchInput");
  expect(await inputHandle!.evaluate((el) => el.isConnected)).toBe(true);
  await expect(input).toHaveValue("Second");
  await expect(cards).toHaveCount(1);
  await context.close();
});

test("member board filtering is unchanged and its handlers ignore public controls after navigation", async ({ browser }) => {
  const outsider = apiClient(baseUrl);
  await outsider.json("POST", "/api/auth/login", { email: OUTSIDER.email, password: OUTSIDER.password });
  const own = await outsider.json<{ slug: string }>("POST", "/api/projects", { name: "Outsider Own Board" });
  await outsider.json("POST", `/api/board/${own.slug}/todos`, { title: "Own story" });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await context.addCookies(toBrowserCookies(outsider.cookies()));
  const page = await context.newPage();
  const urls = trackApiUrls(page);

  // Member board: the regular panel with assignee, sort, No Priority, and its toast.
  await page.goto(`${baseUrl}/${own.slug}`);
  const memberToggle = page.locator("#searchFilterToggle");
  const memberPanel = page.locator("#searchFilterPanel");
  await expect(memberToggle).toBeVisible({ timeout: 30_000 });
  await memberToggle.click();
  await expect(memberPanel).toBeVisible();
  await expect(memberPanel.locator("[data-assignee-option]").first()).toBeVisible();
  await expect(memberPanel.locator("[data-sort-option]")).toHaveCount(4);
  await expect(memberPanel.locator('[data-priority-option="**none**"]')).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(memberPanel).toBeHidden();
  await memberToggle.click();
  await memberPanel.locator('[data-priority-option="high"]').click();
  await expect(memberPanel).toBeHidden();
  await expect(page).toHaveURL(/[?&]priority=high/);
  await expect(page.locator("#toast")).toContainText("High");
  await expect.poll(() => urls.some((u) => u.startsWith(`/api/board/${own.slug}?`) && u.includes("priority=high"))).toBe(true);
  await expect(memberToggle).toHaveClass(/search-filter-toggle--active/);
  await expect(page.locator("#searchClear")).toHaveCount(0);

  // Client-side navigation to the public board (member listeners stay installed).
  await page.evaluate((slug) => {
    history.pushState({}, "", `/${slug}`);
    dispatchEvent(new PopStateEvent("popstate"));
  }, publicSlug);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#searchFilterToggle, #searchFilterPanel, #searchInput")).toHaveCount(0);
  const afterNav = urls.length;

  const toggle = page.locator("#publicFilterToggle");
  const panel = page.locator("#publicFilterPanel");
  await toggle.click();
  await expect(panel).toBeVisible();
  await panel.locator('[data-public-priority-option="high"]').click();
  await expect(panel).toBeHidden();
  await toggle.click();
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();
  await toggle.click();
  await page.locator(".public-board__project-name").click();
  await expect(panel).toBeHidden();
  await expect.poll(() => urls.slice(afterNav).some((u) => u.startsWith(`/api/public/board/${publicSlug}?`) && u.includes("priority=high"))).toBe(true);

  const url = new URL(page.url());
  expect(url.searchParams.get("priority")).toBe("high");
  for (const memberParam of ["sprintId", "assignee", "sort"]) expect(url.searchParams.has(memberParam)).toBe(false);
  // No member reload or member board request was triggered by public controls.
  expect(urls.slice(afterNav).filter((u) => !u.startsWith("/api/public/") && u !== "/api/auth/status")).toEqual([]);
  await context.close();
});

test("member Omni Sprint section follows the sprint collection and offers only Scheduled/Unscheduled", async ({ browser }) => {
  const owner = await ownerClient();
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Member Sprint Filter" });
  const slug = project.slug;
  await owner.json("POST", `/api/board/${slug}/todos`, { title: "Unscheduled story" });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await context.addCookies(toBrowserCookies(owner.cookies()));
  const page = await context.newPage();
  const urls = trackApiUrls(page);
  const toggle = page.locator("#searchFilterToggle");
  const panel = page.locator("#searchFilterPanel");
  const section = panel.locator("[data-sprint-filter-section]");
  const sprintsLoaded = () => page.waitForResponse((res) => new URL(res.url()).pathname === `/api/board/${slug}/sprints`, { timeout: 30_000 });

  // Sprints enabled, none created: no section.
  let loaded = sprintsLoaded();
  await page.goto(`${baseUrl}/${slug}?tag=none-such&priority=high`);
  await loaded;
  await expect(toggle).toBeVisible({ timeout: 30_000 });
  await toggle.click();
  await expect(panel.locator("[data-sort-option]")).toHaveCount(4);
  await expect(section).toHaveCount(0);
  await page.keyboard.press("Escape");

  // Many sprints: still exactly Scheduled and Unscheduled, inserted after the async load.
  const sprintIds: number[] = [];
  for (let i = 1; i <= 21; i++) {
    const start = Date.now();
    const sprint = await owner.json<{ id: number }>("POST", `/api/board/${slug}/sprints`, { name: `Sprint ${i}`, plannedStartAt: start, plannedEndAt: start + 86_400_000 });
    sprintIds.push(sprint.id);
  }
  loaded = sprintsLoaded();
  await page.reload();
  await loaded;
  await toggle.click();
  await expect(section.locator("[data-sprint-option]")).toHaveCount(2);
  expect(await section.locator("[data-sprint-option]").evaluateAll((els) => els.map((el) => el.getAttribute("data-sprint-option")))).toEqual(["scheduled", "unscheduled"]);

  // Select, then deselect by clicking the active option; other params survive.
  await section.locator('[data-sprint-option="unscheduled"]').click();
  await expect(page).toHaveURL(/[?&]sprintId=unscheduled/);
  await expect.poll(() => urls.some((u) => u.startsWith(`/api/board/${slug}?`) && u.includes("sprintId=unscheduled"))).toBe(true);
  loaded = sprintsLoaded();
  await page.reload();
  await loaded;
  await toggle.click();
  await expect(section.locator('[data-sprint-option="unscheduled"]')).toHaveClass(/is-active/);
  const before = urls.filter((u) => u.startsWith(`/api/board/${slug}?`)).length;
  await section.locator('[data-sprint-option="unscheduled"]').click();
  await expect(page).not.toHaveURL(/sprintId=/);
  const url = new URL(page.url());
  expect(url.searchParams.getAll("tag")).toEqual(["none-such"]);
  expect(url.searchParams.get("priority")).toBe("high");
  await expect.poll(() => urls.filter((u) => u.startsWith(`/api/board/${slug}?`)).length).toBe(before + 1);
  await toggle.click();
  await expect(section.locator(".is-active")).toHaveCount(0);
  await page.keyboard.press("Escape");

  // Typed discovery uses the loaded vocabulary even on an empty filtered board.
  loaded = sprintsLoaded();
  await page.goto(`${baseUrl}/${slug}?tag=none-such&priority=high&assignee=me&sort=newest&search=Sprint%203`);
  await loaded;
  const input = page.locator("#searchInput");
  await expect(input).toHaveValue("Sprint 3");
  const thirdSuggestion = page.locator('#omniCandidateViewport [data-omni-sprint]').filter({ hasText: "Sprint 3" }).first();
  await expect(thirdSuggestion).toBeVisible();
  const thirdNumber = await thirdSuggestion.getAttribute("data-omni-sprint");
  expect(thirdNumber).toBeTruthy();
  const beforeDiscoveryPick = urls.filter((u) => u.startsWith(`/api/board/${slug}?`)).length;
  await thirdSuggestion.press("Enter");
  await expect(input).toHaveValue("");
  await expect(page).toHaveURL(new RegExp(`[?&]sprintId=${thirdNumber}(?:&|$)`));
  await expect(page.locator(`#omniPinnedTags [data-omni-clear-sprint="${thirdNumber}"]`)).toBeVisible();
  await page.waitForTimeout(500);
  expect(urls.filter((u) => u.startsWith(`/api/board/${slug}?`)).length).toBe(beforeDiscoveryPick + 1);
  let discoveredUrl = new URL(page.url());
  expect(discoveredUrl.searchParams.has("search")).toBe(false);
  expect(discoveredUrl.searchParams.getAll("tag")).toEqual(["none-such"]);
  expect(discoveredUrl.searchParams.get("priority")).toBe("high");
  expect(discoveredUrl.searchParams.get("assignee")).toBe("me");
  expect(discoveredUrl.searchParams.get("sort")).toBe("newest");

  await input.fill("Sprint 4");
  const fourthSuggestion = page.locator('#omniCandidateViewport [data-omni-sprint]').filter({ hasText: "Sprint 4" }).first();
  await expect(fourthSuggestion).toBeVisible();
  const fourthNumber = await fourthSuggestion.getAttribute("data-omni-sprint");
  await fourthSuggestion.click();
  await expect(page).toHaveURL(new RegExp(`[?&]sprintId=${fourthNumber}(?:&|$)`));
  await expect(page.locator(`#omniPinnedTags [data-omni-clear-sprint="${thirdNumber}"]`)).toHaveCount(0);
  const clearSprint = page.locator(`#omniPinnedTags [data-omni-clear-sprint="${fourthNumber}"]`);
  await clearSprint.press(" ");
  await expect(page).not.toHaveURL(/sprintId=/);
  discoveredUrl = new URL(page.url());
  expect(discoveredUrl.searchParams.getAll("tag")).toEqual(["none-such"]);
  expect(discoveredUrl.searchParams.get("priority")).toBe("high");

  // A numeric deep link stays visible as the selected option and can be cleared.
  const sprints = await owner.json<{ sprints: { id: number; number: number; name: string }[] }>("GET", `/api/board/${slug}/sprints`);
  const third = sprints.sprints.find((s) => s.name === "Sprint 3")!;
  loaded = sprintsLoaded();
  await page.goto(`${baseUrl}/${slug}?sprintId=${third.number}`);
  await loaded;
  await toggle.click();
  const numeric = section.locator(`[data-sprint-option="${third.number}"]`);
  await expect(numeric).toHaveText("Sprint 3");
  await expect(numeric).toHaveClass(/is-active/);
  await expect(section.locator("[data-sprint-option]")).toHaveCount(3);
  await numeric.click();
  await expect(page).not.toHaveURL(/sprintId=/);
  await toggle.click();
  await expect(section.locator("[data-sprint-option]")).toHaveCount(2);
  await page.keyboard.press("Escape");

  // Last sprint deleted: the section disappears.
  for (const id of sprintIds) await owner.json("DELETE", `/api/board/${slug}/sprints/${id}`);
  loaded = sprintsLoaded();
  await page.reload();
  await loaded;
  await toggle.click();
  await expect(panel.locator("[data-sort-option]")).toHaveCount(4);
  await expect(section).toHaveCount(0);
  await context.close();
});

let isolatedClient = 0;

/** A browser context with its own client address for the public per-IP rate limits. */
async function isolatedContext(browser: Browser, options: BrowserContextOptions = {}): Promise<BrowserContext> {
  isolatedClient += 1;
  return browser.newContext({ ...options, extraHTTPHeaders: { "X-Forwarded-For": `198.51.100.${isolatedClient}` } });
}

function publicSnapshotUrls(urls: string[], slug: string): URL[] {
  return urls.filter((u) => u.startsWith(`/api/public/board/${slug}?`)).map((u) => new URL(u, baseUrl));
}

for (const width of [320, 390, 767, 768, 1280]) {
  test(`public Omni tags at ${width}px: browse, suggestions, pins, rails, and keyboard`, async ({ browser }) => {
    const desktop = width >= 768;
    const context = await isolatedContext(browser, { viewport: { width, height: 800 }, isMobile: width <= 620, hasTouch: !desktop });
    const page = await context.newPage();
    const urls = trackApiUrls(page);
    await page.goto(`${baseUrl}/${tagSlug}`);
    const input = page.locator("#publicSearchInput");
    const viewport = page.locator("#publicOmniCandidateViewport");
    const pinnedRail = page.locator("#publicOmniPinnedTags");
    const mobileRail = page.locator("#publicOmniMobileTagPills");
    const next = page.locator("#publicOmniCandidateNext");
    const previous = page.locator("#publicOmniCandidatePrev");
    await expect(input).toBeVisible({ timeout: 30_000 });
    // Like the member board, the mobile row collapses while it has nothing to show.
    await expect(page.locator("[data-public-omni]")).toHaveCount(1);
    await expect(page.locator(".chip, [data-public-tag], #publicTagChips, [id^='omni'], [data-omni-tag]")).toHaveCount(0);
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    const expectRowBelowTopbar = async () => {
      const rowBox = (await page.locator("[data-public-omni]").boundingBox())!;
      const topbarBox = (await page.locator(".topbar").boundingBox())!;
      expect(rowBox.height).toBeGreaterThan(0);
      expect(rowBox.y).toBeGreaterThanOrEqual(topbarBox.y + topbarBox.height - 1);
    };
    if (desktop) await expectRowBelowTopbar();

    if (desktop) {
      // Browse: count order, subdued, overflowing with working chevrons; no mobile rail.
      await expect(viewport.locator("[data-public-omni-tag]").first()).toHaveAttribute("data-public-omni-tag", TAG_NAMES[0]);
      const browseNames = await viewport.locator("[data-public-omni-tag]").evaluateAll((els) => els.map((el) => el.getAttribute("data-public-omni-tag")));
      expect(browseNames).toEqual(TAG_NAMES);
      await expect(viewport.locator(".omni-tag-pill--browse")).toHaveCount(TAG_NAMES.length);
      await expect(mobileRail).toBeHidden();
      await expect(previous).toBeDisabled();
      await expect(next).toBeEnabled();
      await next.click();
      await expect.poll(() => viewport.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);
      await expect(previous).toBeEnabled();
      await previous.click();
      await expect.poll(() => viewport.evaluate((el) => el.scrollLeft)).toBe(0);
      await expect(previous).toBeDisabled();
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    } else {
      // Mobile: no desktop rails and no empty-search Browse pills.
      await expect(page.locator("#publicOmniCandidateRegion")).toBeHidden();
      await expect(pinnedRail).toBeHidden();
      await expect(mobileRail).toBeHidden();
    }

    // Typing shows suggestions immediately; picking one consumes the search with a single reload.
    await input.click();
    await input.pressSequentially("gam");
    const suggestion = (desktop ? viewport : mobileRail).locator('[data-public-omni-tag="gamma-performance"]');
    await expect(suggestion).toBeVisible();
    await expect(suggestion).toHaveClass(/omni-tag-pill--suggestion/);
    const beforePick = urls.length;
    await suggestion.click();
    await expect(input).toHaveValue("");
    await expect(page.locator("#publicSearchClear")).toBeHidden();
    expect(await focusedId(page)).toBe("publicSearchInput");
    const pinRail = desktop ? pinnedRail : mobileRail;
    await expect(pinRail.locator('[data-public-omni-clear-tag="gamma-performance"]')).toBeVisible();
    await expect(page).toHaveURL(/[?&]tag=gamma-performance/);
    await expectRowBelowTopbar();
    await page.waitForTimeout(800);
    const picked = publicSnapshotUrls(urls.slice(beforePick), tagSlug);
    expect(picked).toHaveLength(1);
    expect(picked[0].searchParams.getAll("tag")).toEqual(["gamma-performance"]);
    expect(picked[0].searchParams.has("search")).toBe(false);
    expect(publicSnapshotUrls(urls, tagSlug).some((u) => u.searchParams.get("search") === "gam")).toBe(false);
    if (!desktop) {
      // Mobile rail: pinned pill plus typed suggestions.
      await input.pressSequentially("eps");
      await expect(mobileRail.locator('[data-public-omni-tag="epsilon-localization"]')).toBeVisible();
      await expect(mobileRail.locator('[data-public-omni-clear-tag="gamma-performance"]')).toBeVisible();
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
      await page.locator("#publicSearchClear").click();
      await expect(mobileRail.locator("[data-public-omni-tag]")).toHaveCount(0);
    }

    // Keyboard: Enter on a pill pins it (desktop Browse); Enter on × removes it and returns focus to search.
    if (desktop) {
      const browsePill = viewport.locator('[data-public-omni-tag="beta-design-system"]');
      await browsePill.focus();
      await page.keyboard.press("Enter");
      await expect(pinnedRail.locator('[data-public-omni-clear-tag="beta-design-system"]')).toBeVisible();
      expect(await focusedId(page)).toBe("publicSearchInput");
      await expect(viewport.locator('[data-public-omni-tag="beta-design-system"]')).toHaveCount(0);
    }
    const clear = pinRail.locator('[data-public-omni-clear-tag="gamma-performance"]');
    await clear.focus();
    await page.keyboard.press("Enter");
    await expect(pinRail.locator('[data-public-omni-clear-tag="gamma-performance"]')).toHaveCount(0);
    expect(await focusedId(page)).toBe("publicSearchInput");
    await expect(page).not.toHaveURL(/tag=gamma-performance/);

    // Slice 1 filter panel still coexists with the Omni row.
    await page.locator("#publicFilterToggle").click();
    const panelBox = (await page.locator("#publicFilterPanel").boundingBox())!;
    expect(contains({ x: 0, y: 0, width, height: 800 }, panelBox)).toBe(true);
    await page.keyboard.press("Escape");
    await expect(page.locator("#publicFilterPanel")).toBeHidden();
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
    expect(urls.filter((u) => !u.startsWith(`/api/public/board/${tagSlug}`) && u !== "/api/auth/status").map((u) => new URL(u, baseUrl).pathname)).toEqual([`/api/board/${tagSlug}`]);
    await context.close();
  });
}

test("removing one public Omni tag keeps the other tags and filters", async ({ browser }) => {
  const context = await isolatedContext(browser);
  const page = await context.newPage();
  const urls = trackApiUrls(page);
  await page.goto(`${baseUrl}/${tagSlug}?tag=alpha-component-library&tag=beta-design-system&priority=high&search=story`);
  const pinned = page.locator("#publicOmniPinnedTags");
  await expect(pinned.locator("[data-public-omni-clear-tag]")).toHaveCount(2, { timeout: 30_000 });
  await expect(page.locator("#publicSearchInput")).toHaveValue("story");
  const before = urls.length;
  await pinned.locator('[data-public-omni-clear-tag="alpha-component-library"]').click();
  await expect(pinned.locator("[data-public-omni-clear-tag]")).toHaveCount(1);
  const url = new URL(page.url());
  expect(url.searchParams.getAll("tag")).toEqual(["beta-design-system"]);
  expect(url.searchParams.get("priority")).toBe("high");
  expect(url.searchParams.get("search")).toBe("story");
  await expect.poll(() => publicSnapshotUrls(urls.slice(before), tagSlug).length).toBe(1);
  const req = publicSnapshotUrls(urls.slice(before), tagSlug)[0];
  expect(req.searchParams.getAll("tag")).toEqual(["beta-design-system"]);
  expect(req.searchParams.get("priority")).toBe("high");
  expect(req.searchParams.get("search")).toBe("story");
  await context.close();
});

test("public Omni keeps focus and unchanged rail scroll across live refreshes", async ({ browser }) => {
  const context = await isolatedContext(browser, { viewport: { width: 1280, height: 800 } });
  const page = await context.newPage();
  await page.goto(`${baseUrl}/${tagSlug}`);
  const viewport = page.locator("#publicOmniCandidateViewport");
  await expect(viewport.locator("[data-public-omni-tag]")).toHaveCount(TAG_NAMES.length, { timeout: 30_000 });
  const owner = await ownerClient();

  // Counts change but the candidate order (and markup) does not: scroll and the focused pill survive.
  await viewport.evaluate((el) => { el.scrollLeft = 120; });
  const pill = viewport.locator('[data-public-omni-tag="delta-accessibility"]');
  await pill.focus();
  const marker = await pill.evaluate((el) => { (el as HTMLElement & { __mark?: number }).__mark = 1; return true; });
  expect(marker).toBe(true);
  await owner.json("POST", `/api/board/${tagSlug}/todos`, { title: "Refresh alpha", tags: ["alpha-component-library"] });
  await expect(page.locator(".card__title", { hasText: "Refresh alpha" })).toHaveCount(1, { timeout: 20_000 });
  expect(await pill.evaluate((el) => (el as HTMLElement & { __mark?: number }).__mark)).toBe(1);
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-public-omni-tag"))).toBe("delta-accessibility");
  expect(await viewport.evaluate((el) => el.scrollLeft)).toBe(120);

  // A vocabulary change re-renders candidates but keeps focus on the same tag.
  await owner.json("POST", `/api/board/${tagSlug}/todos`, { title: "Refresh omega", tags: ["omega-new"] });
  await expect(viewport.locator('[data-public-omni-tag="omega-new"]')).toHaveCount(1, { timeout: 20_000 });
  expect(await page.evaluate(() => document.activeElement?.getAttribute("data-public-omni-tag"))).toBe("delta-accessibility");
  await context.close();
});

test("after a member Omni board, public Omni controls never reach member handlers", async ({ browser }) => {
  const outsider = apiClient(baseUrl);
  await outsider.json("POST", "/api/auth/login", { email: OUTSIDER.email, password: OUTSIDER.password });
  const own = await outsider.json<{ slug: string }>("POST", "/api/projects", { name: "Outsider Omni Board" });
  await outsider.json("POST", `/api/board/${own.slug}/todos`, { title: "Own tagged", tags: ["member-only-tag"] });
  const context = await isolatedContext(browser, { viewport: { width: 1280, height: 800 } });
  await context.addCookies(toBrowserCookies(outsider.cookies()));
  const page = await context.newPage();
  const urls = trackApiUrls(page);

  // Member Omni works as before (member IDs, data-omni-* hooks, member reload).
  await page.goto(`${baseUrl}/${own.slug}`);
  const memberPill = page.locator('#omniCandidateViewport [data-omni-tag="member-only-tag"]');
  await expect(memberPill).toBeVisible({ timeout: 30_000 });
  await memberPill.click();
  await expect(page.locator('#omniPinnedTags [data-omni-clear-tag="member-only-tag"]')).toBeVisible();
  await expect.poll(() => urls.some((u) => u.startsWith(`/api/board/${own.slug}?`) && u.includes("tag=member-only-tag"))).toBe(true);

  await page.evaluate((slug) => {
    history.pushState({}, "", `/${slug}`);
    dispatchEvent(new PopStateEvent("popstate"));
  }, tagSlug);
  await expect(page.locator(".public-board-badge")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("[id^='omni'], [data-omni-tag], [data-omni-clear-tag]")).toHaveCount(0);
  const afterNav = urls.length;

  const viewport = page.locator("#publicOmniCandidateViewport");
  await viewport.locator('[data-public-omni-tag="alpha-component-library"]').click();
  await expect(page.locator('#publicOmniPinnedTags [data-public-omni-clear-tag="alpha-component-library"]')).toBeVisible();
  await page.locator("#publicSearchInput").pressSequentially("bet");
  await viewport.locator('[data-public-omni-tag="beta-design-system"]').click();
  await page.locator("#publicOmniCandidateNext").click();
  await page.setViewportSize({ width: 1000, height: 800 });
  await page.locator('#publicOmniPinnedTags [data-public-omni-clear-tag="alpha-component-library"]').click();
  await expect.poll(() => publicSnapshotUrls(urls.slice(afterNav), tagSlug).some((u) => u.searchParams.getAll("tag").join() === "beta-design-system")).toBe(true);

  const url = new URL(page.url());
  expect(url.searchParams.getAll("tag")).toEqual(["beta-design-system"]);
  for (const memberParam of ["sprintId", "assignee", "sort"]) expect(url.searchParams.has(memberParam)).toBe(false);
  expect(urls.slice(afterNav).filter((u) => !u.startsWith("/api/public/") && u !== "/api/auth/status")).toEqual([]);
  await context.close();
});

type CardLayoutReport = {
  cards: number;
  problems: string[];
  longestTagShown: boolean;
  dragHandles: number;
};

/** Geometry of every loaded card in one lane: tags inside, no content overflow, no neighbor overlap, handle placement. */
async function measureCardLayout(page: Page, listSelector: string): Promise<CardLayoutReport> {
  return page.locator(listSelector).evaluate((list, longest) => {
    const problems: string[] = [];
    const cards = Array.from(list.querySelectorAll<HTMLElement>(":scope > .card"));
    const eps = 0.5;
    let dragHandles = 0;
    cards.forEach((card, i) => {
      const name = card.querySelector(".card__title")?.textContent ?? `card ${i}`;
      const box = card.getBoundingClientRect();
      if (card.scrollHeight > card.clientHeight + 1) problems.push(`${name}: content ${card.scrollHeight}px > card ${card.clientHeight}px`);
      if (card.scrollWidth > card.clientWidth + 1) problems.push(`${name}: content wider than card`);
      for (const part of Array.from(card.querySelectorAll<HTMLElement>(".card__title-row, .card__tags .tag, .card__badges > *"))) {
        const r = part.getBoundingClientRect();
        if (r.left < box.left - eps || r.right > box.right + eps || r.top < box.top - eps || r.bottom > box.bottom + eps) {
          problems.push(`${name}: ${part.className || part.tagName} "${part.textContent}" outside card`);
        }
      }
      const next = cards[i + 1]?.getBoundingClientRect();
      if (next && next.top < box.bottom - eps) problems.push(`${name}: overlaps the next card by ${Math.round(box.bottom - next.top)}px`);
      const handle = card.querySelector<HTMLElement>(".card__drag-handle");
      if (handle) {
        dragHandles += 1;
        const h = handle.getBoundingClientRect();
        const inTopRight = h.right <= box.right + eps && h.top >= box.top - eps && h.left >= box.left + box.width / 2 && h.bottom <= box.top + 48;
        if (!inTopRight || h.width === 0) problems.push(`${name}: drag handle not in the card's top-right corner`);
      }
    });
    const longestTagShown = Array.from(list.querySelectorAll(".card__tags .tag")).some((tag) => tag.textContent === longest);
    return { cards: cards.length, problems, longestTagShown, dragHandles };
  }, LONGEST_TAG);
}

let cardOwnerCookies: Cookie[] | null = null;

for (const width of [320, 390, 620, 621, 767, 1280]) {
  for (const audience of ["public", "member"] as const) {
    test(`${audience} story cards at ${width}px keep tags inside non-overlapping cards and still load more`, async ({ browser }) => {
      const mobileLane = width <= 620;
      const context = await isolatedContext(browser, { viewport: { width, height: 740 }, isMobile: mobileLane, hasTouch: width < 768 });
      if (audience === "member") {
        cardOwnerCookies ??= (await ownerClient()).cookies();
        await context.addCookies(toBrowserCookies(cardOwnerCookies));
      }
      const page = await context.newPage();
      await page.goto(`${baseUrl}/${cardSlug}`);
      const listSelector = audience === "public" ? '[data-public-list="backlog"]' : "#list_backlog";
      const cardSelector = audience === "public" ? "[data-public-local-id]" : "[data-todo-id]";
      const list = page.locator(listSelector);
      await expect(list.locator(cardSelector)).toHaveCount(20, { timeout: 30_000 });
      if (mobileLane) await expect(page.locator(`.col--mobile-active ${listSelector}`)).toHaveCount(1);

      const first = await measureCardLayout(page, listSelector);
      expect(first.problems).toEqual([]);
      expect(first.longestTagShown).toBe(true);
      expect(first.dragHandles).toBe(audience === "member" ? 20 : 0);
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);

      // Scroll the lane to its end, then load the next page with the control this width shows.
      await list.evaluate((el) => { el.scrollTop = el.scrollHeight; el.dispatchEvent(new Event("scroll")); });
      if (mobileLane) {
        expect(await list.evaluate((el) => el.scrollHeight > el.clientHeight && el.scrollTop > 0)).toBe(true);
        const loadMore = audience === "public"
          ? page.locator('.col--mobile-active .col__load-more--mobile[data-public-load-more="backlog"]')
          : page.locator('.col--mobile-active [data-load-more="backlog"].visible .col__load-more--mobile');
        await expect(loadMore).toBeVisible();
        await loadMore.click();
      } else {
        const loadMore = audience === "public"
          ? page.locator('.col__load-more--desktop[data-public-load-more="backlog"]')
          : page.locator('[data-load-more="backlog"] .col__load-more--desktop');
        await loadMore.scrollIntoViewIfNeeded();
        await loadMore.click();
      }
      await expect(list.locator(cardSelector)).toHaveCount(CARD_LAYOUT_STORIES, { timeout: 20_000 });

      const all = await measureCardLayout(page, listSelector);
      expect(all.problems).toEqual([]);
      expect(all.dragHandles).toBe(audience === "member" ? CARD_LAYOUT_STORIES : 0);
      expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
      await context.close();
    });
  }
}

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
