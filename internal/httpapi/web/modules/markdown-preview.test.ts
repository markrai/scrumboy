// @vitest-environment jsdom
// DOMPurify does not support happy-dom. Use jsdom for sanitizer tests.
import createDOMPurify from "dompurify";
import MarkdownIt from "markdown-it";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  DEFAULT_MERMAID_SEMANTIC_EDGE_CONFIG,
  MERMAID_SEMANTIC_EDGES_URL,
  resetMermaidSemanticEdgeConfigCacheForTests,
} from "./mermaid-semantic-edges.js";
import deCatalog from "./i18n/locales/de.json";

function installMarkdownVendors(): void {
  (window as any).markdownit = (preset?: string, options?: Record<string, unknown>) =>
    new MarkdownIt(preset, options);
  (window as any).DOMPurify = createDOMPurify(window);
}

function installMermaidStub(
  onRun?: (node: HTMLElement, runIndex: number) => Promise<void> | void,
): { initialize: ReturnType<typeof vi.fn>; run: ReturnType<typeof vi.fn> } {
  const initialize = vi.fn();
  let runIndex = 0;
  const run = vi.fn(async (options?: MermaidRunOptions) => {
    const nodes = Array.from(options?.nodes ?? []) as HTMLElement[];
    for (const node of nodes) {
      runIndex += 1;
      await onRun?.(node, runIndex);
    }
  });
  (window as any).mermaid = { initialize, run };
  return { initialize, run };
}

describe("markdown preview rendering", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = "";
    document.documentElement.removeAttribute("data-theme");
    resetMermaidSemanticEdgeConfigCacheForTests();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url === MERMAID_SEMANTIC_EDGES_URL || url.endsWith(MERMAID_SEMANTIC_EDGES_URL)) {
          return { ok: true, json: async () => DEFAULT_MERMAID_SEMANTIC_EDGE_CONFIG };
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );
    installMarkdownVendors();
    delete (window as any).mermaid;
  });

  afterEach(async () => {
    const i18n = await import("./i18n/index.js");
    i18n.resetI18nForTests();
    vi.unstubAllGlobals();
  });

  it("renders the supported markdown set for todo notes", async () => {
    const { renderMarkdownToSafeHtml } = await import("./markdown-preview.js");

    const html = renderMarkdownToSafeHtml(
      [
        "# Heading One",
        "## Heading Two",
        "### Heading Three",
        "",
        "**bold** and *italic*",
        "",
        "- one",
        "- two",
        "",
        "1. first",
        "2. second",
        "",
        "> quoted",
        "",
        "inline `code`",
        "",
        "```ts",
        "const answer = 42;",
        "```",
        "",
        "---",
        "",
        "[safe link](https://example.com/path)",
        "",
        "line one",
        "line two",
      ].join("\n"),
    );

    expect(html).toContain("<h1>Heading One</h1>");
    expect(html).toContain("<h2>Heading Two</h2>");
    expect(html).toContain("<h3>Heading Three</h3>");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<em>italic</em>");
    expect(html).toContain("<ul>");
    expect(html).toContain("<ol>");
    expect(html).toContain("<blockquote>");
    expect(html).toContain("<code>code</code>");
    expect(html).toContain("<pre><code>const answer = 42;");
    expect(html).toContain("<hr>");
    expect(html).toContain('href="https://example.com/path"');
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain("<br>");
  });

  it("allows safe root-relative links but rejects protocol-relative and non-web schemes", async () => {
    const { renderMarkdownToSafeHtml } = await import("./markdown-preview.js");

    const html = renderMarkdownToSafeHtml(
      [
        "[root](/board/alpha)",
        "[protocol-relative](//example.com/path)",
        "[mailto](mailto:test@example.com)",
        "[tel](tel:+15555550123)",
      ].join("\n\n"),
    );

    expect(html).toContain('<a href="/board/alpha">root</a>');
    expect(html).not.toContain('href="//example.com/path"');
    expect(html).not.toContain('href="mailto:test@example.com"');
    expect(html).not.toContain('href="tel:+15555550123"');
    expect(html).toContain("<p>protocol-relative</p>");
    expect(html).toContain("<p>mailto</p>");
    expect(html).toContain("<p>tel</p>");
  });

  it("neutralizes raw html, dangerous links, and image syntax", async () => {
    const { renderMarkdownToSafeHtml } = await import("./markdown-preview.js");

    const html = renderMarkdownToSafeHtml(
      [
        "<script>alert(1)</script>",
        '<img src=x onerror=alert(1)>',
        "[x](javascript:alert(1))",
        "[x](data:text/html,<script>alert(1)</script>)",
        "[x](vbscript:msgbox(1))",
        "[x](//example.com/path)",
        "<iframe src=\"https://example.com\"></iframe>",
        "<object data=\"https://example.com/file.swf\"></object>",
        "<embed src=\"https://example.com/file.swf\">",
        "![alt](https://example.com/x.png)",
      ].join("\n\n"),
    );

    expect(html).not.toContain("<script");
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<iframe");
    expect(html).not.toContain("<object");
    expect(html).not.toContain("<embed");
    expect(html).not.toContain('href="javascript:');
    expect(html).not.toContain('href="data:text/html');
    expect(html).not.toContain('href="vbscript:');
    expect(html).not.toContain('href="//example.com/path"');
    expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
    expect(html).toContain("![alt](https://example.com/x.png)");

    const template = document.createElement("template");
    template.innerHTML = html;
    expect(template.content.querySelector("[onerror]")).toBeNull();

    const anchorCount = (html.match(/<a /g) || []).length;
    expect(anchorCount).toBe(0);
  });

  it("renders mermaid fences as ordinary code when mermaid support is off", async () => {
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      ["```mermaid", "graph TD", "A-->B", "```"].join("\n"),
      { mermaidEnabled: false },
    );

    expect(container.querySelector(".todo-mermaid-host")).toBeNull();
    expect(container.innerHTML).toContain("<pre><code>graph TD");
    expect(container.innerHTML).toContain("A--&gt;B");
  });

  it("renders mermaid fences with the lazy mermaid runtime and strips all mermaid directive blocks", async () => {
    const seenSources: string[] = [];
    const mermaid = installMermaidStub((node) => {
      seenSources.push(node.textContent || "");
      node.innerHTML = '<svg data-rendered="yes"></svg>';
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      [
        "```mermaid",
        '%%{init: { "theme": "dark" }}%%',
        '%%{initialize: { "securityLevel": "loose" }}%%',
        '%%{config: { "theme": "forest" }}%%',
        "graph TD",
        "A-->B",
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        startOnLoad: false,
        securityLevel: "strict",
        maxTextSize: 50_000,
        maxEdges: 500,
        theme: "base",
        themeVariables: expect.objectContaining({
          darkMode: true,
          background: "#0f172a",
        }),
      }),
    );
    expect(seenSources).toEqual(["graph TD\nA-->B"]);
    expect(container.innerHTML).toContain('data-rendered="yes"');
    expect(container.textContent).not.toContain("%%{init:");
    expect(container.textContent).not.toContain("%%{initialize:");
    expect(container.textContent).not.toContain("%%{config:");
  });

  it("shows a local warning for directive-only mermaid blocks without invoking mermaid", async () => {
    const mermaid = installMermaidStub();
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      [
        "```mermaid",
        '%%{init: { "theme": "dark" }}%%',
        '%%{config: { "theme": "forest" }}%%',
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(mermaid.initialize).not.toHaveBeenCalled();
    expect(mermaid.run).not.toHaveBeenCalled();
    expect(container.textContent).toContain("This Mermaid block contains only ignored Mermaid directives. Showing source instead.");
    expect(container.textContent).toContain('%%{init: { "theme": "dark" }}%%');
    expect(container.textContent).toContain('%%{config: { "theme": "forest" }}%%');
  });

  it("renders multiple mermaid blocks while leaving other fenced code untouched", async () => {
    const rendered: string[] = [];
    installMermaidStub((node, index) => {
      rendered.push(node.textContent || "");
      node.innerHTML = `<svg data-order="${index}"></svg>`;
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      [
        "```mermaid",
        "graph TD",
        "A-->B",
        "```",
        "",
        "```ts",
        "const value = 1;",
        "```",
        "",
        "```mermaid",
        "graph TD",
        "B-->C",
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(rendered).toEqual(["graph TD\nA-->B", "graph TD\nB-->C"]);
    expect(container.innerHTML).toContain('data-order="1"');
    expect(container.innerHTML).toContain('data-order="2"');
    expect(container.innerHTML).toContain("<pre><code>const value = 1;");
  });

  it("does not upgrade ordinary fenced code that looks like a mermaid placeholder", async () => {
    const rendered: string[] = [];
    installMermaidStub((node) => {
      rendered.push(node.textContent || "");
      node.innerHTML = '<svg data-rendered="real-mermaid"></svg>';
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      [
        "```text",
        "__SCRUMBOY_MERMAID_BLOCK_0__",
        "```",
        "",
        "```mermaid",
        "graph TD",
        "A-->B",
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(rendered).toEqual(["graph TD\nA-->B"]);
    expect(container.innerHTML).toContain('data-rendered="real-mermaid"');
    expect(container.innerHTML).toContain("<pre><code>__SCRUMBOY_MERMAID_BLOCK_0__");
  });

  it("preserves the original directive-bearing source in error fallback output", async () => {
    installMermaidStub(() => {
      throw new Error("bad diagram");
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      [
        "```mermaid",
        '%%{config: { "theme": "forest" }}%%',
        "graph TD",
        "broken",
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(container.textContent).toContain('%%{config: { "theme": "forest" }}%%');
    expect(container.textContent).toContain("Could not render Mermaid diagram. Showing source instead.");
  });

  it("keeps preview open and shows source fallback when mermaid rendering fails", async () => {
    installMermaidStub(() => {
      throw new Error("bad diagram");
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      ["```mermaid", "graph TD", "broken", "```"].join("\n"),
      { mermaidEnabled: true },
    );

    expect(container.textContent).toContain("Could not render Mermaid diagram. Showing source instead.");
    expect(container.textContent).toContain("graph TD");
    expect(container.textContent).toContain("broken");
  });

  it("localizes mermaid loading, warning, and fallback messages after i18n initialization", async () => {
    const i18n = await import("./i18n/index.js");
    await i18n.initI18n({
      locale: "de",
      loadLocale: vi.fn(async () => deCatalog),
    });
    const pending: Array<() => void> = [];
    installMermaidStub(() => new Promise<void>((resolve) => pending.push(resolve)));
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const loadingContainer = document.createElement("div");
    document.body.appendChild(loadingContainer);

    const loadingRender = renderMarkdownPreviewInto(
      loadingContainer,
      ["```mermaid", "graph TD", "A-->B", "```"].join("\n"),
      { mermaidEnabled: true },
    );
    for (let i = 0; i < 10 && pending.length === 0; i += 1) {
      await Promise.resolve();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }

    expect(loadingContainer.querySelector(".todo-mermaid-status")?.textContent).toBe(
      deCatalog["todo.markdownPreview.rendering"],
    );
    expect(pending).toHaveLength(1);
    pending[0]?.();
    await loadingRender;

    const warningContainer = document.createElement("div");
    document.body.appendChild(warningContainer);
    await renderMarkdownPreviewInto(
      warningContainer,
      [
        "```mermaid",
        '%%{init: { "theme": "dark" }}%%',
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );
    expect(warningContainer.textContent).toContain(deCatalog["todo.markdownPreview.directivesOnly"]);

    installMermaidStub(() => {
      throw new Error("bad diagram");
    });
    const errorContainer = document.createElement("div");
    document.body.appendChild(errorContainer);
    await renderMarkdownPreviewInto(
      errorContainer,
      ["```mermaid", "graph TD", "broken", "```"].join("\n"),
      { mermaidEnabled: true },
    );

    expect(errorContainer.textContent).toContain(deCatalog["todo.markdownPreview.renderFailed"]);
  });

  it("shows a local warning for oversized mermaid diagrams without loading the runtime", async () => {
    const mermaid = installMermaidStub();
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);
    const oversizedBlock = `graph TD\nA[${"x".repeat(4_200)}]-->B`;

    await renderMarkdownPreviewInto(container, ["```mermaid", oversizedBlock, "```"].join("\n"), {
      mermaidEnabled: true,
    });

    expect(mermaid.initialize).not.toHaveBeenCalled();
    expect(mermaid.run).not.toHaveBeenCalled();
    expect(container.textContent).toContain("Showing source instead.");
    expect(container.textContent).toContain(oversizedBlock);
  });

  it("renders only mermaid blocks that fit within the total preview budget", async () => {
    const mermaid = installMermaidStub((node, index) => {
      node.innerHTML = `<svg data-order="${index}"></svg>`;
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);
    const nearLimitBlock = `graph TD\nA[${"x".repeat(2_900)}]-->B`;

    await renderMarkdownPreviewInto(
      container,
      [
        "```mermaid",
        nearLimitBlock,
        "```",
        "",
        "```mermaid",
        nearLimitBlock,
        "```",
        "",
        "```mermaid",
        nearLimitBlock,
        "```",
      ].join("\n"),
      { mermaidEnabled: true },
    );

    expect(mermaid.initialize).toHaveBeenCalledTimes(1);
    expect(mermaid.run).toHaveBeenCalledTimes(2);
    expect(container.innerHTML).toContain('data-order="1"');
    expect(container.innerHTML).toContain('data-order="2"');
    expect(container.textContent).toContain("This note exceeds the 8000-character Mermaid preview budget. Showing source instead.");
  });

  it("configures mermaid with light preview colors when data-theme is light", async () => {
    document.documentElement.setAttribute("data-theme", "light");
    const mermaid = installMermaidStub((node) => {
      node.innerHTML = '<svg data-rendered="yes"></svg>';
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    await renderMarkdownPreviewInto(
      container,
      ["```mermaid", "graph TD", "A-->B", "```"].join("\n"),
      { mermaidEnabled: true },
    );

    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        theme: "base",
        themeVariables: expect.objectContaining({
          darkMode: false,
          background: "#e5dfd5",
          mainBkg: "#e5dfd5",
          primaryTextColor: "#111827",
        }),
      }),
    );
  });

  it("reconfigures mermaid when preview theme changes between renders", async () => {
    const initConfigs: MermaidConfig[] = [];
    const mermaid = installMermaidStub((node) => {
      node.innerHTML = '<svg data-rendered="yes"></svg>';
    });
    mermaid.initialize.mockImplementation((config: MermaidConfig) => {
      initConfigs.push(config);
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);
    const diagram = ["```mermaid", "graph TD", "A-->B", "```"].join("\n");

    await renderMarkdownPreviewInto(container, diagram, { mermaidEnabled: true });
    document.documentElement.setAttribute("data-theme", "light");
    await renderMarkdownPreviewInto(container, diagram, { mermaidEnabled: true });

    expect(initConfigs).toHaveLength(2);
    expect(initConfigs[0].themeVariables).toEqual(
      expect.objectContaining({
        darkMode: true,
        background: "#0f172a",
      }),
    );
    expect(initConfigs[1].themeVariables).toEqual(
      expect.objectContaining({
        darkMode: false,
        background: "#e5dfd5",
      }),
    );
  });

  it("initializes mermaid only for the active render when previews overlap", async () => {
    const pending: Array<() => void> = [];
    const mermaid = installMermaidStub(() => {
      return new Promise<void>((resolve) => {
        pending.push(() => resolve());
      });
    });
    document.documentElement.setAttribute("data-theme", "light");
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);
    const diagram = ["```mermaid", "graph TD", "A-->B", "```"].join("\n");

    void renderMarkdownPreviewInto(container, diagram, { mermaidEnabled: true });
    void renderMarkdownPreviewInto(container, diagram, { mermaidEnabled: true });

    await Promise.resolve();
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(mermaid.initialize).toHaveBeenCalledTimes(1);
    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        themeVariables: expect.objectContaining({
          background: "#e5dfd5",
        }),
      }),
    );
    expect(pending).toHaveLength(1);

    pending[0]();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(mermaid.run).toHaveBeenCalledTimes(1);
  });

  it("keeps only the latest async mermaid render result when preview rerenders while typing", async () => {
    const pending: Array<() => void> = [];
    const queuedSources: string[] = [];
    installMermaidStub((node, index) => {
      return new Promise<void>((resolve) => {
        queuedSources.push(node.textContent || "");
        pending.push(() => {
          node.innerHTML = `<svg data-order="${index}"></svg>`;
          resolve();
        });
      });
    });
    const { renderMarkdownPreviewInto } = await import("./markdown-preview.js");
    const container = document.createElement("div");
    document.body.appendChild(container);

    const firstRender = renderMarkdownPreviewInto(
      container,
      ["```mermaid", "graph TD", "A-->B", "```"].join("\n"),
      { mermaidEnabled: true },
    );
    const secondRender = renderMarkdownPreviewInto(
      container,
      ["```mermaid", "graph TD", "B-->C", "```"].join("\n"),
      { mermaidEnabled: true },
    );

    await Promise.resolve();
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(pending).toHaveLength(1);
    expect(queuedSources).toEqual(["graph TD\nB-->C"]);

    pending[0]();
    await Promise.all([firstRender, secondRender]);

    expect(container.innerHTML).toContain('data-order="1"');
    expect(container.textContent).not.toContain("A-->B");
  });
});
