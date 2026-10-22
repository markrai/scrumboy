// @vitest-environment jsdom
// DOMPurify does not support happy-dom. Use jsdom for sanitizer tests.
import { readFileSync } from "node:fs";
import path from "node:path";
import createDOMPurify from "dompurify";
import MarkdownIt from "markdown-it";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installAppRuntime, resetAppRuntimeForTests, type AppRuntime } from "./platform/runtime.js";
import type { AcquiredServerResource, ServerResourceOptions, ServerTransport } from "./platform/server-transport.js";

const U1 = "11111111-1111-4111-8111-111111111111";
const U2 = "22222222-2222-4222-9222-222222222222";
const img = (uuid: string, alt = "shot") => `![${alt}](/api/assets/${uuid})`;

type Pending = {
  path: string;
  signal?: AbortSignal;
  resolve(url?: string): AcquiredServerResource;
  reject(err?: unknown): void;
};

type FakeTransport = {
  pending: Pending[];
  acquired: string[];
  released: string[];
  autoResolve: boolean;
  maxInFlight: number;
};

function installFakeTransport(autoResolve = true): FakeTransport {
  const fake: FakeTransport = { pending: [], acquired: [], released: [], autoResolve, maxInFlight: 0 };
  let inFlight = 0;
  const transport: ServerTransport = {
    request: vi.fn(),
    openEventStream: vi.fn(),
    logout: vi.fn(async () => undefined),
    acquireResource: vi.fn((path: string, options?: ServerResourceOptions) => {
      fake.acquired.push(path);
      inFlight += 1;
      fake.maxInFlight = Math.max(fake.maxInFlight, inFlight);
      return new Promise<AcquiredServerResource>((resolve, reject) => {
        const make = (url = `blob:native/${path}`): AcquiredServerResource => {
          let released = false;
          return {
            url,
            release: () => {
              if (released) return;
              released = true;
              fake.released.push(path);
            },
          };
        };
        const entry: Pending = {
          path,
          signal: options?.signal,
          resolve: (url) => {
            inFlight -= 1;
            const r = make(url);
            resolve(r);
            return r;
          },
          reject: (err) => {
            inFlight -= 1;
            reject(err ?? new Error("boom"));
          },
        };
        options?.signal?.addEventListener("abort", () => entry.reject(new DOMException("Aborted", "AbortError")), { once: true });
        fake.pending.push(entry);
        if (fake.autoResolve) queueMicrotask(() => entry.resolve());
      });
    }),
  };
  const runtime: AppRuntime = {
    kind: "browser",
    capability: () => null,
    assetOrigin: () => "",
    serverOrigin: () => "",
    publicLinkOrigin: () => "",
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: vi.fn(async () => undefined),
    transport: () => transport,
  } as AppRuntime;
  installAppRuntime(runtime);
  return fake;
}

async function flush(): Promise<void> {
  for (let i = 0; i < 10; i += 1) await Promise.resolve();
}

describe("markdown preview inline images", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) => new MarkdownIt(preset, options);
    (window as any).DOMPurify = createDOMPurify(window);
  });

  afterEach(() => {
    resetAppRuntimeForTests();
  });

  async function load() {
    return import("./markdown-preview.js");
  }

  it("keeps image syntax as escaped text when inline images are disabled", async () => {
    installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const on = document.createElement("div");
    const off = document.createElement("div");
    await renderMarkdownPreviewInto(off, img(U1));
    await renderMarkdownPreviewInto(on, img(U1), { inlineImagesEnabled: false });
    expect(off.innerHTML).toBe(on.innerHTML);
    expect(off.querySelector("img")).toBeNull();
    expect(off.textContent).toContain(`![shot](/api/assets/${U1})`);
  });

  it("renders a canonical asset image through the transport", async () => {
    const fake = installFakeTransport(false);
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    const done = renderMarkdownPreviewInto(container, `Before ${img(U1, "My shot")} after`, { inlineImagesEnabled: true });
    const el = container.querySelector("img") as HTMLImageElement;
    expect(el).not.toBeNull();
    expect(el.getAttribute("src")).toBeNull();
    expect(el.alt).toBe("My shot");
    await flush();
    fake.pending[0].resolve("blob:native/one");
    await done;
    expect(el.getAttribute("src")).toBe("blob:native/one");
    expect(fake.acquired).toEqual([`/api/assets/${U1}`]);
  });

  it("applies a stored display width and keeps it out of the alt text", async () => {
    installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, `${img(U1, "wide|480")}\n\n${img(U2, "plain")}\n\n${img(U2, "year|2024")}`, { inlineImagesEnabled: true });
    const [wide, plain, year] = Array.from(container.querySelectorAll("img"));
    expect(wide.alt).toBe("wide");
    expect(wide.style.width).toBe("480px");
    expect(wide.parentElement?.classList.contains("todo-notes-image--sized")).toBe(true);
    expect(plain.alt).toBe("plain");
    expect(plain.style.width).toBe("");
    expect(plain.parentElement?.classList.contains("todo-notes-image--sized")).toBe(false);
    // Out of range: ordinary alt text, default size.
    expect(year.alt).toBe("year|2024");
    expect(year.style.width).toBe("");
  });

  it("keeps non-canonical image destinations as escaped text", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    for (const src of [
      "https://evil.example/x.png",
      "/api/assets/AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
      `/api/assets/${U1}?x=1`,
      `/api/assets/${U1}#f`,
      `//evil.example/api/assets/${U1}`,
      "data:image/png;base64,AAAA",
    ]) {
      const container = document.createElement("div");
      await renderMarkdownPreviewInto(container, `![x](${src})`, { inlineImagesEnabled: true });
      expect({ src, img: container.querySelector("img")?.outerHTML ?? null }).toEqual({ src, img: null });
      expect(container.textContent).toContain("![x](");
    }
    expect(fake.acquired).toEqual([]);
  });

  it("does not let user text spoof an image placeholder", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, "`__SCRUMBOY_IMAGE__abc_0__` and ``__SCRUMBOY_IMAGE__`` " + img(U1), { inlineImagesEnabled: true });
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(Array.from(container.querySelectorAll("code")).map((c) => c.textContent)).toEqual([
      "__SCRUMBOY_IMAGE__abc_0__",
      "__SCRUMBOY_IMAGE__",
    ]);
    expect(fake.acquired).toHaveLength(1);
  });

  it("leaves image syntax inside code as code", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, "```\n" + img(U1) + "\n```\n\n`" + img(U2) + "`", { inlineImagesEnabled: true });
    expect(container.querySelector("img")).toBeNull();
    expect(fake.acquired).toEqual([]);
  });

  it("sets alt and title as properties, never as markup", async () => {
    installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, `![x"><img src=x onerror=alert(1)>](/api/assets/${U1} "t\\"><b>")`, { inlineImagesEnabled: true });
    const images = container.querySelectorAll("img");
    expect(images).toHaveLength(1);
    expect(container.querySelector("b")).toBeNull();
    expect(images[0].getAttribute("onerror")).toBeNull();
  });

  it("does not change the sanitizer configuration", async () => {
    installFakeTransport();
    const purify = (window as any).DOMPurify;
    const spy = vi.spyOn(purify, "sanitize");
    const { renderMarkdownPreviewInto } = await load();
    await renderMarkdownPreviewInto(document.createElement("div"), img(U1), { inlineImagesEnabled: true });
    const options = spy.mock.calls[0][1] as Record<string, unknown>;
    expect(options.ALLOWED_TAGS).not.toContain("img");
    expect(options.ALLOWED_ATTR).toEqual(["href", "rel", "target"]);
    expect(options.ALLOW_DATA_ATTR).toBe(false);
  });

  it("releases every acquisition across re-renders and explicit release", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto, releaseMarkdownPreviewResources } = await load();
    const container = document.createElement("div");
    for (let i = 0; i < 4; i += 1) {
      await renderMarkdownPreviewInto(container, img(U1) + img(U2), { inlineImagesEnabled: true });
    }
    releaseMarkdownPreviewResources(container);
    releaseMarkdownPreviewResources(container);
    expect(fake.acquired).toHaveLength(8);
    expect(fake.released).toHaveLength(8);
  });

  it("releases a stale acquisition instead of attaching it", async () => {
    const fake = installFakeTransport(false);
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    const first = renderMarkdownPreviewInto(container, img(U1), { inlineImagesEnabled: true });
    await flush();
    const second = renderMarkdownPreviewInto(container, img(U2), { inlineImagesEnabled: true });
    await flush();
    // The first acquisition was aborted by the re-render; resolving late must not attach it.
    expect(fake.pending[0].signal?.aborted).toBe(true);
    fake.pending[1].resolve("blob:native/two");
    await Promise.all([first, second]);
    const images = container.querySelectorAll("img");
    expect(images).toHaveLength(1);
    expect(images[0].getAttribute("src")).toBe("blob:native/two");
  });

  it("aborts pending acquisitions and clears src on release", async () => {
    const fake = installFakeTransport(false);
    const { renderMarkdownPreviewInto, releaseMarkdownPreviewResources } = await load();
    const container = document.createElement("div");
    const done = renderMarkdownPreviewInto(container, img(U1) + img(U2), { inlineImagesEnabled: true });
    await flush();
    fake.pending[0].resolve();
    await flush();
    releaseMarkdownPreviewResources(container);
    await done;
    expect(fake.pending[1].signal?.aborted).toBe(true);
    expect(fake.released).toEqual([`/api/assets/${U1}`]);
    for (const el of Array.from(container.querySelectorAll("img"))) {
      expect(el.getAttribute("src")).toBeNull();
    }
  });

  it("shows a fallback when acquisition fails or the image errors", async () => {
    const fake = installFakeTransport(false);
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    const done = renderMarkdownPreviewInto(container, img(U1, "first") + " " + img(U2, "second"), { inlineImagesEnabled: true });
    await flush();
    fake.pending[0].reject(new Error("404"));
    fake.pending[1].resolve();
    await done;
    const unavailable = container.querySelectorAll(".todo-notes-image--unavailable");
    expect(unavailable).toHaveLength(1);
    expect(unavailable[0].textContent).toContain("first");
    const loaded = container.querySelector("img") as HTMLImageElement;
    loaded.dispatchEvent(new Event("error"));
    expect(container.querySelectorAll(".todo-notes-image--unavailable")).toHaveLength(2);
  });

  it("acquires a repeated image once", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, img(U1) + "\n\n" + img(U1, "again"), { inlineImagesEnabled: true });
    expect(fake.acquired).toHaveLength(1);
    const srcs = Array.from(container.querySelectorAll("img")).map((el) => el.getAttribute("src"));
    expect(srcs).toHaveLength(2);
    expect(srcs[0]).toBe(srcs[1]);
  });

  it("bounds concurrent acquisitions and images per preview", async () => {
    const fake = installFakeTransport();
    const { renderMarkdownPreviewInto } = await load();
    const ids = Array.from({ length: 60 }, (_, i) => `${String(i).padStart(8, "0")}-1111-4111-8111-111111111111`);
    const container = document.createElement("div");
    await renderMarkdownPreviewInto(container, ids.map((id) => img(id)).join("\n\n"), { inlineImagesEnabled: true });
    expect(fake.maxInFlight).toBeLessThanOrEqual(4);
    expect(fake.acquired).toHaveLength(50);
    expect(container.querySelectorAll("img")).toHaveLength(50);
    expect(container.textContent).toContain(`![shot](/api/assets/${ids[59]})`);
  });

  it("renders images alongside mermaid with strict security", async () => {
    installFakeTransport();
    const initialize = vi.fn();
    const run = vi.fn(async () => undefined);
    (window as any).mermaid = { initialize, run };
    const { DEFAULT_MERMAID_SEMANTIC_EDGE_CONFIG, resetMermaidSemanticEdgeConfigCacheForTests } = await import("./mermaid-semantic-edges.js");
    resetMermaidSemanticEdgeConfigCacheForTests();
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => DEFAULT_MERMAID_SEMANTIC_EDGE_CONFIG })));
    const { renderMarkdownPreviewInto } = await load();
    const container = document.createElement("div");
    document.body.append(container);
    await renderMarkdownPreviewInto(container, img(U1) + "\n\n```mermaid\ngraph TD\nA-->B\n```", {
      inlineImagesEnabled: true,
      mermaidEnabled: true,
    });
    expect(container.querySelector("img")).not.toBeNull();
    expect(run).toHaveBeenCalled();
    expect(initialize.mock.calls[0][0].securityLevel).toBe("strict");
    vi.unstubAllGlobals();
    delete (window as any).mermaid;
  });

  it("matches the server extraction fixtures (assetrefs parity)", async () => {
    // Tests run from internal/httpapi/web; the corpus is shared with internal/assetrefs (Go).
    const fixturesPath = path.resolve(process.cwd(), "../../assetrefs/testdata/fixtures.json");
    const fixtures = JSON.parse(readFileSync(fixturesPath, "utf8")) as { name: string; markdown: string; expected: string[] }[];
    expect(fixtures.length).toBeGreaterThanOrEqual(35);
    const { renderMarkdownPreviewInto } = await load();
    for (const fixture of fixtures) {
      const fake = installFakeTransport();
      const container = document.createElement("div");
      await renderMarkdownPreviewInto(container, fixture.markdown, { inlineImagesEnabled: true });
      const got = fake.acquired.map((p) => p.slice("/api/assets/".length));
      expect({ name: fixture.name, got }).toEqual({ name: fixture.name, got: fixture.expected });
    }
  });
});
