/**
 * Focused regression coverage for Wall story pins (PR #360 Phase 4).
 *
 * Wall pins reuse the shared story-card footer markup at a fixed 280px pin
 * width, independent of viewport width. One small project carries four pins:
 * tags+badges, tags-only, badges-only, and bare. Each viewport width audits
 * pin geometry (bounding rects + computed styles) in a single DOM pass and
 * drags the rich pin to prove positioning and drag stay functional.
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

const OWNER = { email: "wall-pin-footer-e2e@example.com", password: "password123", name: "Pin Footer Owner" };

/** 32 narrow ASCII code points: the maximum-length unbroken tag layout case. */
const LONGEST_TAG = "abcdefghijklmnopqrstuvwxyz012345";
const RICH_TAGS = [LONGEST_TAG, "frontend-platform", "design-review", "accessibility-audit"];
const RICH = 1;
const TAGS_ONLY = 2;
const BADGES_ONLY = 3;
const BARE = 4;
/** Canvas placement per story local id; pins never overlap each other. */
const PIN_POS: Record<number, { x: number; y: number }> = {
  [RICH]: { x: 60, y: 60 },
  [TAGS_ONLY]: { x: 60, y: 700 },
  [BADGES_ONLY]: { x: 60, y: 1340 },
  [BARE]: { x: 60, y: 1980 },
};
/** Fixed pin width contract from wall-story-geometry.ts. */
const PIN_WIDTH = 280;
/** Rich pin canvas position, advanced by each width's drag (drops commit server-side). */
let richPos = { x: PIN_POS[RICH].x, y: PIN_POS[RICH].y };

const WIDTHS = [375, 768, 1280];

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let slug = "";

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-wall-pin-footer-"));
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
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Wall Pin Footer" });
  slug = project.slug;
  await owner.json("POST", `/api/board/${slug}/custom-fields`, { name: "Ticket", type: "url", key: "ticket" });
  await owner.json("POST", `/api/board/${slug}/custom-fields`, { name: "Note", type: "text", key: "note" });
  const create = (body: Record<string, unknown>) => owner.json("POST", `/api/board/${slug}/todos`, body);
  await create({
    title: "Rich pin story",
    tags: RICH_TAGS,
    priorityKey: "high",
    estimationPoints: 8,
    dueAt: "2099-01-01T12:00:00Z",
    customFields: { ticket: "https://example.com/T-1", note: "needs-review" },
  });
  await create({ title: "Tags only pin", tags: ["frontend-platform", "design-review"] });
  await create({ title: "Badges only pin", priorityKey: "high", estimationPoints: 5 });
  await create({ title: "Bare pin" });
  for (const localId of [RICH, TAGS_ONLY, BADGES_ONLY, BARE]) {
    const pos = PIN_POS[localId];
    await owner.json("POST", `/api/board/${slug}/wall/stories`, { localId, x: pos.x, y: pos.y });
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

type PinFacts = {
  id: number;
  pinOverflow: string | null;
  outsideParts: string[];
  layoutWidth: number;
  styleLeft: number | null;
  styleTop: number | null;
  tagsPresent: boolean;
  tagsChildren: number;
  tagsDisplay: string;
  badgesPresent: boolean;
  badgesChildren: number;
  badgesDisplay: string;
  badgesGridColumn: string;
  badgesGridRow: string;
  tagsGridRow: string;
  badgesRightGap: number | null;
  badgesBelowTags: boolean | null;
  rowsShareLeft: boolean | null;
  longestTagLines: number | null;
  tagOverflow: string | null;
};

type PinReport = {
  pins: number;
  problems: string[];
  facts: PinFacts[];
  pageOverflow: number;
};

/** Single DOM pass over every story pin: containment, overflow, footer rows, tag lines. */
async function auditPins(page: Page): Promise<PinReport> {
  return page.locator("#wallDialog").evaluate((dialog) => {
    const eps = 0.5;
    const problems: string[] = [];
    const facts: PinFacts[] = [];
    const pins = Array.from(dialog.querySelectorAll<HTMLElement>(".wall-story"));
    const style = (el: Element, prop: string) => getComputedStyle(el).getPropertyValue(prop);
    pins.forEach((pin, i) => {
      const id = Number(pin.dataset.storyLocalId);
      const name = pin.querySelector(".card__title")?.textContent ?? `pin ${i}`;
      const box = pin.getBoundingClientRect();
      let pinOverflow: string | null = null;
      if (pin.scrollWidth > pin.clientWidth + 1) pinOverflow = `content ${pin.scrollWidth}px wider than pin ${pin.clientWidth}px`;
      if (pin.scrollHeight > pin.clientHeight + 1) pinOverflow = `${pin.scrollHeight}px taller than pin ${pin.clientHeight}px`;
      if (pinOverflow) problems.push(`${name}: ${pinOverflow}`);
      const outsideParts: string[] = [];
      const tagsEl = pin.querySelector<HTMLElement>(".card__tags .card__tags-list");
      const badgesEl = pin.querySelector<HTMLElement>(".card__tags .card__badges");
      const parts: HTMLElement[] = [
        ...Array.from(pin.querySelectorAll<HTMLElement>(".card__title-row")),
        ...Array.from(pin.querySelectorAll<HTMLElement>(".card__tags .tag")),
      ];
      if (badgesEl) parts.push(...Array.from(badgesEl.children).filter((el): el is HTMLElement => el instanceof HTMLElement));
      for (const part of parts) {
        const r = part.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        if (r.left < box.left - eps || r.right > box.right + eps || r.top < box.top - eps || r.bottom > box.bottom + eps) {
          outsideParts.push(`${part.className || part.tagName}`);
        }
      }
      if (outsideParts.length > 0) problems.push(`${name}: outside pin: ${outsideParts.join(", ")}`);
      let longestTagLines: number | null = null;
      let tagOverflow: string | null = null;
      for (const tag of Array.from(pin.querySelectorAll<HTMLElement>(".card__tags .tag"))) {
        // Tags are inline spans: scrollWidth is meaningless once wrapped, so
        // check every rendered line box against the pin instead.
        for (const line of Array.from(tag.getClientRects())) {
          if (line.width > 0 && (line.left < box.left - eps || line.right > box.right + eps)) {
            tagOverflow = `"${tag.textContent}" clips horizontally`;
          }
        }
        // Pills use `line-height: normal`, which has no pixel value: fall back to font size.
        let lineHeight = parseFloat(style(tag, "line-height"));
        if (!Number.isFinite(lineHeight) || lineHeight <= 0) {
          lineHeight = parseFloat(style(tag, "font-size")) * 1.2;
        }
        const r = tag.getBoundingClientRect();
        if (Number.isFinite(lineHeight) && lineHeight > 0 && r.height > 0) {
          const lines = Math.round(r.height / lineHeight);
          if (tag.textContent === "abcdefghijklmnopqrstuvwxyz012345") longestTagLines = lines;
        }
      }
      if (tagOverflow) problems.push(`${name}: tag ${tagOverflow}`);
      let badgesRightGap: number | null = null;
      let badgesBelowTags: boolean | null = null;
      let rowsShareLeft: boolean | null = null;
      if (tagsEl && badgesEl && style(badgesEl, "display") !== "none" && style(tagsEl, "display") !== "none") {
        const t = tagsEl.getBoundingClientRect();
        const b = badgesEl.getBoundingClientRect();
        badgesRightGap = box.right - b.right;
        badgesBelowTags = b.top >= t.bottom - 1;
        rowsShareLeft = Math.abs(b.left - t.left) <= 2;
      }
      facts.push({
        id,
        pinOverflow,
        outsideParts,
        layoutWidth: pin.offsetWidth,
        styleLeft: pin.style.left ? parseInt(pin.style.left, 10) : null,
        styleTop: pin.style.top ? parseInt(pin.style.top, 10) : null,
        tagsPresent: !!tagsEl,
        tagsChildren: tagsEl ? tagsEl.childElementCount : 0,
        tagsDisplay: tagsEl ? style(tagsEl, "display") : "absent",
        badgesPresent: !!badgesEl,
        badgesChildren: badgesEl ? badgesEl.childElementCount : 0,
        badgesDisplay: badgesEl ? style(badgesEl, "display") : "absent",
        badgesGridColumn: badgesEl ? style(badgesEl, "grid-column-start") : "absent",
        badgesGridRow: badgesEl ? style(badgesEl, "grid-row-start") : "absent",
        tagsGridRow: tagsEl ? style(tagsEl, "grid-row-start") : "absent",
        badgesRightGap,
        badgesBelowTags,
        rowsShareLeft,
        longestTagLines,
        tagOverflow,
      });
    });
    return {
      pins: pins.length,
      problems,
      facts,
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
}

let ownerCookies: Cookie[] | null = null;

async function openWall(page: Page): Promise<void> {
  await page.goto(`${baseUrl}/${slug}`);
  await page.locator("#wallBtn").click();
  await expect(page.locator("#wallDialog")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".wall-story")).toHaveCount(4, { timeout: 30_000 });
}

for (const width of WIDTHS) {
  test(`wall story pins at ${width}px keep footers inside readable pins and drag to new positions`, async ({ browser }) => {
    const mobile = width < 768;
    const context = await browser.newContext({
      viewport: { width, height: 800 },
      isMobile: mobile,
      hasTouch: mobile,
    });
    await context.addCookies(toBrowserCookies((await ownerClient()).cookies()));
    const page = await context.newPage();
    await openWall(page);

    const report = await auditPins(page);
    expect(report.problems).toEqual([]);
    expect(report.pageOverflow).toBeLessThanOrEqual(0);
    expect(report.facts.map((f) => f.id).sort()).toEqual([RICH, TAGS_ONLY, BADGES_ONLY, BARE]);

    const byId = new Map(report.facts.map((f) => [f.id, f]));
    const rich = byId.get(RICH)!;
    const tagsOnly = byId.get(TAGS_ONLY)!;
    const badgesOnly = byId.get(BADGES_ONLY)!;
    const bare = byId.get(BARE)!;

    // Fixed pin width and canvas positions hold at every viewport width.
    // Other pins keep their seeds; the rich pin accumulates this file's drags.
    for (const fact of report.facts) {
      expect(fact.layoutWidth).toBe(PIN_WIDTH);
      const want = fact.id === RICH ? richPos : PIN_POS[fact.id];
      expect(fact.styleLeft).toBe(want.x);
      expect(fact.styleTop).toBe(want.y);
    }

    // Footer composition per pin kind. The wall story projection carries tags,
    // priority, and points, but no due date or custom fields (store wall.go),
    // so the rich pin shows priority + points badges.
    expect(rich.tagsChildren).toBe(RICH_TAGS.length);
    expect(rich.badgesChildren).toBe(2);
    expect(rich.tagsDisplay).not.toBe("none");
    expect(rich.badgesDisplay).not.toBe("none");
    expect(tagsOnly.tagsChildren).toBe(2);
    expect(tagsOnly.badgesChildren).toBe(0);
    expect(badgesOnly.tagsChildren).toBe(0);
    expect(badgesOnly.badgesChildren).toBeGreaterThan(0);
    expect(bare.tagsPresent).toBe(false);
    expect(bare.badgesPresent).toBe(false);

    // Empty containers reserve no row: hidden on mobile, zero-size on desktop.
    if (mobile) {
      expect(tagsOnly.badgesDisplay).toBe("none");
      expect(badgesOnly.tagsDisplay).toBe("none");
      // Mobile: tags and badges occupy separate full-width rows, badges below tags.
      expect(rich.tagsGridRow).toBe("2");
      expect(rich.badgesGridRow).toBe("3");
      expect(rich.badgesBelowTags).toBe(true);
      expect(rich.rowsShareLeft).toBe(true);
    } else {
      // Desktop: two-column footer grid, badges in the right-hand column.
      const tagsBoxes = await page.locator(".wall-story > .card__content > .card__tags").evaluateAll(
        (els) => els.map((el) => ({ display: getComputedStyle(el).display, tracks: getComputedStyle(el).gridTemplateColumns.split(" ").length })),
      );
      expect(tagsBoxes.map((b) => b.display)).toEqual(["grid", "grid", "grid"]);
      expect(tagsBoxes.map((b) => b.tracks)).toEqual([2, 2, 2]);
      for (const pin of [rich, badgesOnly]) {
        expect(pin.badgesGridColumn).toBe("2");
        expect(pin.badgesRightGap).not.toBeNull();
        expect(pin.badgesRightGap!).toBeLessThanOrEqual(24);
      }
      const emptyTagsBox = await page.locator(`.wall-story[data-story-local-id="${BADGES_ONLY}"] .card__tags-list`).boundingBox();
      expect(emptyTagsBox?.height ?? 0).toBeLessThanOrEqual(1);
      const emptyBadgesBox = await page.locator(`.wall-story[data-story-local-id="${TAGS_ONLY}"] .card__badges`).boundingBox();
      expect(emptyBadgesBox?.width ?? 0).toBeLessThanOrEqual(1);
    }

    // The 32-character unbroken tag never collapses to one character per line,
    // even though pins are far narrower than the viewport.
    expect(rich.longestTagLines).not.toBeNull();
    expect(rich.longestTagLines!).toBeLessThanOrEqual(4);

    // Drag the rich pin: canvas position follows the pointer and the footer
    // stays inside the pin afterwards.
    const pin = page.locator(`.wall-story[data-story-local-id="${RICH}"]`);
    const dialogBox = (await page.locator("#wallDialog").boundingBox())!;
    const startBox = (await pin.boundingBox())!;
    expect(startBox.x).toBeGreaterThanOrEqual(dialogBox.x);
    expect(startBox.x + startBox.width).toBeLessThanOrEqual(dialogBox.x + dialogBox.width);
    const zoom = startBox.width / PIN_WIDTH;
    const startLeft = richPos.x;
    const startTop = richPos.y;
    const dx = 80;
    const dy = 60;
    await page.mouse.move(startBox.x + startBox.width / 2, startBox.y + 40);
    await page.mouse.down();
    await page.mouse.move(startBox.x + startBox.width / 2 + dx, startBox.y + 40 + dy, { steps: 12 });
    const committed = page.waitForResponse(
      (response) => response.url().endsWith(`/api/board/${slug}/wall/stories/${RICH}`) &&
        response.request().method() === "PATCH",
      { timeout: 10_000 },
    );
    await page.mouse.up();
    expect((await committed).ok()).toBe(true);
    await expect.poll(async () => pin.evaluate((el) => (el as HTMLElement).style.left), { timeout: 10_000 }).toBe(`${Math.round(startLeft + dx / zoom)}px`);
    await expect.poll(async () => pin.evaluate((el) => (el as HTMLElement).style.top), { timeout: 10_000 }).toBe(`${Math.round(startTop + dy / zoom)}px`);
    richPos = { x: Math.round(startLeft + dx / zoom), y: Math.round(startTop + dy / zoom) };

    await expect.poll(async () => {
      const wall = await (await ownerClient()).json<{ stories: Array<{ localId: number; x: number; y: number }> }>(
        "GET", `/api/board/${slug}/wall`,
      );
      const saved = wall.stories.find((story) => story.localId === RICH);
      return saved ? { x: saved.x, y: saved.y } : null;
    }, { timeout: 10_000 }).toEqual(richPos);

    const after = await auditPins(page);
    expect(after.problems).toEqual([]);
    expect(after.facts.find((f) => f.id === RICH)!.styleLeft).toBe(richPos.x);
    expect(after.facts.find((f) => f.id === RICH)!.styleTop).toBe(richPos.y);
    await context.close();
  });
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
