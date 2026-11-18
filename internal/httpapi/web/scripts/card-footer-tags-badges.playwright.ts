/**
 * Focused regression coverage for story-card footer layout (PR #360):
 * tags grid vs. badge column on desktop, separate full-width badge rows on
 * mobile, and no phantom rows for empty tag/badge containers.
 *
 * One small published project carries five backlog cards: tags+badges,
 * many-tags, tags-only, badges-only, and bare. Each width/audience pair
 * audits geometry (bounding rects + computed styles) in a single DOM pass.
 *
 * Spawns a temporary full-mode Scrumboy server (go run) against a temp DATA_DIR.
 */
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");

const OWNER = { email: "card-footer-e2e@example.com", password: "password123", name: "Footer Owner" };

/** 32 narrow ASCII code points: the maximum-length unbroken tag layout case. */
const LONGEST_TAG = "abcdefghijklmnopqrstuvwxyz012345";
const MANY_TAGS = [
  "frontend-platform", "design-review", "performance-budget", "accessibility-audit",
  "localization-sweep", "observability-gap", "infrastructure-cost", "documentation-debt",
  "release-train", "customer-escalation", "data-pipeline-fix", "security-review",
];
const RICH_TAGS = [LONGEST_TAG, "frontend-platform", "design-review", "accessibility-audit"];
/** Local ids follow creation order. */
const RICH = 1;
const MANY = 2;
const TAGS_ONLY = 3;
const BADGES_ONLY = 4;
const BARE = 5;

const WIDTHS = [320, 375, 620, 767, 768, 1280];
const AUDIENCES = ["public", "member"] as const;

let server: ChildProcessWithoutNullStreams | null = null;
let dataDir = "";
let baseUrl = "";
let slug = "";

function setPublication(projectSlug: string, enabled: boolean): void {
  const db = new DatabaseSync(path.join(dataDir, "app.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.prepare("UPDATE projects SET public_view_enabled = ? WHERE slug = ?").run(enabled ? 1 : 0, projectSlug);
  } finally {
    db.close();
  }
}

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  dataDir = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-card-footer-"));
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;
  server = spawn("go", ["run", "./cmd/scrumboy"], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1",
      // Distinct client addresses per context (X-Forwarded-For) stay under the
      // public per-IP read rate limit shared across widths.
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
  const project = await owner.json<{ slug: string }>("POST", "/api/projects", { name: "Card Footer Board" });
  slug = project.slug;
  await owner.json("POST", `/api/board/${slug}/custom-fields`, { name: "Ticket", type: "url", key: "ticket" });
  await owner.json("POST", `/api/board/${slug}/custom-fields`, { name: "Note", type: "text", key: "note" });
  const create = (body: Record<string, unknown>) => owner.json("POST", `/api/board/${slug}/todos`, body);
  await create({
    title: "Rich footer card",
    tags: RICH_TAGS,
    priorityKey: "high",
    estimationPoints: 8,
    dueAt: "2099-01-01T12:00:00Z",
    customFields: { ticket: "https://example.com/T-1", note: "needs-review" },
  });
  await create({ title: "Many tags card", tags: MANY_TAGS });
  await create({ title: "Tags only card", tags: ["frontend-platform", "design-review"] });
  await create({ title: "Badges only card", priorityKey: "high", estimationPoints: 5 });
  await create({ title: "Bare card" });
  setPublication(slug, true);
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

type CardFacts = {
  id: number;
  cardOverflow: string | null;
  overlapsNext: string | null;
  outsideParts: string[];
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
  handleMissing: boolean;
  handleMisplaced: boolean;
};

type FooterReport = {
  cards: number;
  problems: string[];
  facts: CardFacts[];
  publicHandles: number;
  pageOverflow: number;
};

/**
 * Single DOM pass per page: containment, overflow, overlap, empty-container
 * visibility, longest-tag line count, badge placement, and drag handles.
 */
async function auditFooter(page: Page, listSelector: string, cardAttr: string): Promise<FooterReport> {
  return page.locator(listSelector).evaluate((list, attr) => {
    const eps = 0.5;
    const problems: string[] = [];
    const facts: CardFacts[] = [];
    const cards = Array.from(list.querySelectorAll<HTMLElement>(":scope > .card"));
    const style = (el: Element, prop: string) => getComputedStyle(el).getPropertyValue(prop);
    cards.forEach((card, i) => {
      const id = Number(card.getAttribute(attr));
      const name = card.querySelector(".card__title")?.textContent ?? `card ${i}`;
      const box = card.getBoundingClientRect();
      let cardOverflow: CardFacts["cardOverflow"] = null;
      if (card.scrollWidth > card.clientWidth + 1) cardOverflow = `content ${card.scrollWidth}px wider than card ${card.clientWidth}px`;
      if (card.scrollHeight > card.clientHeight + 1) cardOverflow = `${card.scrollHeight}px taller than card ${card.clientHeight}px`;
      if (cardOverflow) problems.push(`${name}: ${cardOverflow}`);
      const next = cards[i + 1]?.getBoundingClientRect();
      const overlapsNext = next && next.top < box.bottom - eps ? `overlaps the next card by ${Math.round(box.bottom - next.top)}px` : null;
      if (overlapsNext) problems.push(`${name}: ${overlapsNext}`);
      const outsideParts: string[] = [];
      const tagsEl = card.querySelector<HTMLElement>(".card__tags .card__tags-list");
      const badgesEl = card.querySelector<HTMLElement>(".card__tags .card__badges");
      const parts: HTMLElement[] = [
        ...Array.from(card.querySelectorAll<HTMLElement>(".card__title-row")),
        ...Array.from(card.querySelectorAll<HTMLElement>(".card__tags .tag")),
      ];
      if (badgesEl) parts.push(...Array.from(badgesEl.children).filter((el): el is HTMLElement => el instanceof HTMLElement));
      for (const part of parts) {
        const r = part.getBoundingClientRect();
        if (r.width === 0 && r.height === 0) continue;
        if (r.left < box.left - eps || r.right > box.right + eps || r.top < box.top - eps || r.bottom > box.bottom + eps) {
          outsideParts.push(`${part.className || part.tagName}`);
        }
      }
      if (outsideParts.length > 0) problems.push(`${name}: outside card: ${outsideParts.join(", ")}`);
      let longestTagLines: number | null = null;
      let tagOverflow: string | null = null;
      for (const tag of Array.from(card.querySelectorAll<HTMLElement>(".card__tags .tag"))) {
        const r = tag.getBoundingClientRect();
        // Tags are inline spans: scrollWidth is meaningless once wrapped, so
        // check every rendered line box against the card instead.
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
        if (Number.isFinite(lineHeight) && lineHeight > 0 && r.height > 0) {
          const lines = Math.round(r.height / lineHeight);
          if (tag.textContent === "abcdefghijklmnopqrstuvwxyz012345") longestTagLines = lines;
        }
      }
      if (tagOverflow) problems.push(`${name}: tag ${tagOverflow}`);
      const handle = card.querySelector<HTMLElement>(".card__drag-handle");
      const handleMissing = !handle;
      let handleMisplaced = false;
      if (handle) {
        const h = handle.getBoundingClientRect();
        if (!(h.right <= box.right + eps && h.top >= box.top - eps && h.left >= box.left + box.width / 2 && h.bottom <= box.top + 48) || h.width === 0) {
          handleMisplaced = true;
          problems.push(`${name}: drag handle not in the card's top-right corner`);
        }
      }
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
        cardOverflow,
        overlapsNext,
        outsideParts,
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
        handleMissing,
        handleMisplaced,
      });
    });
    return {
      cards: cards.length,
      problems,
      facts,
      publicHandles: document.querySelectorAll(".card__drag-handle").length,
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  }, cardAttr);
}

let isolatedClient = 0;

async function isolatedContext(browser: Browser, width: number): Promise<BrowserContext> {
  isolatedClient += 1;
  return browser.newContext({
    viewport: { width, height: 800 },
    isMobile: width <= 620,
    hasTouch: width < 768,
    extraHTTPHeaders: { "X-Forwarded-For": `198.51.100.${isolatedClient}` },
  });
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

let memberCookies: Cookie[] | null = null;

for (const width of WIDTHS) {
  for (const audience of AUDIENCES) {
    test(`${audience} card footer at ${width}px: tags, badges, and rows stay inside the card`, async ({ browser }) => {
      const mobile = width < 768;
      const context = await isolatedContext(browser, width);
      if (audience === "member") {
        memberCookies ??= (await ownerClient()).cookies();
        await context.addCookies(toBrowserCookies(memberCookies));
      }
      const page = await context.newPage();
      await page.goto(`${baseUrl}/${slug}`);
      const listSelector = audience === "public" ? '[data-public-list="backlog"]' : "#list_backlog";
      const cardSelector = audience === "public" ? "[data-public-local-id]" : "[data-todo-id]";
      const list = page.locator(listSelector);
      await expect(list.locator(cardSelector)).toHaveCount(5, { timeout: 30_000 });
      if (width <= 620) await expect(page.locator(`.col--mobile-active ${listSelector}`)).toHaveCount(1);

      const report = await auditFooter(page, listSelector, audience === "public" ? "data-public-local-id" : "data-todo-local-id");
      expect(report.problems).toEqual([]);
      expect(report.pageOverflow).toBeLessThanOrEqual(0);
      expect(report.facts.map((f) => f.id).sort()).toEqual([RICH, MANY, TAGS_ONLY, BADGES_ONLY, BARE]);

      const byId = new Map(report.facts.map((f) => [f.id, f]));
      const rich = byId.get(RICH)!;
      const many = byId.get(MANY)!;
      const tagsOnly = byId.get(TAGS_ONLY)!;
      const badgesOnly = byId.get(BADGES_ONLY)!;
      const bare = byId.get(BARE)!;

      // Footer composition per card kind.
      expect(rich.tagsChildren).toBeGreaterThan(0);
      expect(rich.badgesChildren).toBe(audience === "member" ? 5 : 2);
      expect(rich.tagsDisplay).not.toBe("none");
      expect(rich.badgesDisplay).not.toBe("none");
      expect(many.tagsChildren).toBe(MANY_TAGS.length);
      expect(tagsOnly.tagsChildren).toBe(2);
      expect(badgesOnly.badgesChildren).toBeGreaterThan(0);
      expect(bare.tagsPresent).toBe(false);
      expect(bare.badgesPresent).toBe(false);

      // Empty containers reserve no row: hidden on mobile, zero-size on desktop.
      for (const empty of [many, tagsOnly]) {
        expect(empty.badgesChildren).toBe(0);
        if (mobile) expect(empty.badgesDisplay).toBe("none");
        else expect(empty.badgesDisplay).not.toBe("none");
      }
      if (mobile) {
        expect(badgesOnly.tagsDisplay).toBe("none");
      } else {
        const attr = audience === "public" ? "data-public-local-id" : "data-todo-local-id";
        const tagsListBox = await list.locator(`[${attr}="${BADGES_ONLY}"] .card__tags-list`).boundingBox();
        expect(tagsListBox?.height ?? 0).toBeLessThanOrEqual(1);
      }

      // The 32-character unbroken tag never collapses to one character per line.
      expect(rich.longestTagLines).not.toBeNull();
      expect(rich.longestTagLines!).toBeLessThanOrEqual(4);

      if (!mobile) {
        // Desktop: two-column footer grid, badges in the right-hand column.
        const displays = await list.locator(".card > .card__content > .card__tags").evaluateAll((els) => els.map((el) => getComputedStyle(el).display));
        expect(displays).toEqual(["grid", "grid", "grid", "grid"]);
        const columns = await list.locator(".card > .card__content > .card__tags").evaluateAll((els) => els.map((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length));
        expect(columns).toEqual([2, 2, 2, 2]);
        for (const card of [rich, badgesOnly]) {
          expect(card.badgesGridColumn).toBe("2");
          expect(card.badgesRightGap).not.toBeNull();
          expect(card.badgesRightGap!).toBeLessThanOrEqual(24);
        }
      } else {
        // Mobile: tags and badges occupy separate full-width rows, badges below tags.
        expect(rich.tagsGridRow).toBe("2");
        expect(rich.badgesGridRow).toBe("3");
        expect(rich.badgesBelowTags).toBe(true);
        expect(rich.rowsShareLeft).toBe(true);
      }

      // Drag handles stay pinned top-right on member cards and are absent publicly.
      if (audience === "member") {
        for (const fact of report.facts) {
          expect(fact.handleMissing).toBe(false);
          expect(fact.handleMisplaced).toBe(false);
        }
      } else {
        expect(report.publicHandles).toBe(0);
      }
      await context.close();
    });
  }
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
