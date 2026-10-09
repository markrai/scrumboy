import { createServer, type Server } from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const staticFiles = new Set([
  "/mermaid-semantic-edges.json",
  "/vendor/markdown-it.min.js",
  "/vendor/mermaid.min.js",
  "/vendor/purify.min.js",
]);

let server: Server;
let baseUrl: string;

function contentType(filePath: string): string {
  if (filePath.endsWith(".json")) return "application/json; charset=utf-8";
  return "text/javascript; charset=utf-8";
}

async function installCspListener(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as any).__scrumboyCspViolations = [];
    document.addEventListener("securitypolicyviolation", (event) => {
      (window as any).__scrumboyCspViolations.push({
        blockedUri: event.blockedURI,
        directive: event.effectiveDirective,
      });
    });
  });
}

test.beforeAll(async () => {
  server = createServer(async (request, response) => {
    response.setHeader(
      "Content-Security-Policy",
      "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src data:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-src 'none'",
    );
    const requestPath = new URL(request.url ?? "/", "http://localhost").pathname;
    if (requestPath === "/mermaid-only.html") {
      response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      response.end('<!doctype html><html><head><script src="/vendor/mermaid.min.js"></script></head><body></body></html>');
      return;
    }
    if (requestPath === "/preview.html") {
      response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      response.end('<!doctype html><html><head><script src="/vendor/markdown-it.min.js"></script><script src="/vendor/purify.min.js"></script></head><body></body></html>');
      return;
    }

    if (staticFiles.has(requestPath) || requestPath.startsWith("/dist/")) {
      const filePath = path.resolve(webDir, requestPath.slice(1));
      if (!filePath.startsWith(`${webDir}${path.sep}`)) {
        response.writeHead(404);
        response.end();
        return;
      }
      try {
        response.writeHead(200, { "Content-Type": contentType(filePath) });
        response.end(await fs.readFile(filePath));
        return;
      } catch {
        response.writeHead(404);
        response.end();
        return;
      }
    }

    response.writeHead(404);
    response.end();
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("Could not determine Mermaid browser test server address");
  }
  baseUrl = `http://127.0.0.1:${address.port}`;
});

test.afterAll(async () => {
  await new Promise<void>((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
  });
});

test("self-contained Mermaid runtime preserves the global API, CSP, flowcharts, and patched math", async ({ page }) => {
  const requests: string[] = [];
  page.on("request", (request) => requests.push(request.url()));
  await installCspListener(page);
  await page.goto(`${baseUrl}/mermaid-only.html`);

  const result = await page.evaluate(async () => {
    const mermaid = (window as any).mermaid;
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      maxTextSize: 50_000,
      maxEdges: 500,
      suppressErrorRendering: true,
    });

    const flowchart = document.createElement("div");
    flowchart.textContent = "flowchart TD\nA[Start] --> B[Done]";
    document.body.append(flowchart);
    await mermaid.run({ nodes: [flowchart] });

    const math = document.createElement("div");
    math.textContent = 'flowchart TD\nA["$$x^2 + y^2$$"] --> B[Result]';
    document.body.append(math);
    await mermaid.run({ nodes: [math] });

    return {
      api: ["initialize", "run", "render", "parse"].map((name) => typeof mermaid[name]),
      securityLevel: mermaid.mermaidAPI.getConfig().securityLevel,
      flowchartRendered: !!flowchart.querySelector("svg .node"),
      mathRendered: !!math.querySelector("svg .katex"),
      violations: (window as any).__scrumboyCspViolations,
    };
  });

  expect(result).toEqual({
    api: ["function", "function", "function", "function"],
    securityLevel: "strict",
    flowchartRendered: true,
    mathRendered: true,
    violations: [],
  });
  expect(requests.map((request) => new URL(request).pathname)).toEqual([
    "/mermaid-only.html",
    "/vendor/mermaid.min.js",
  ]);
});

test("Markdown preview integration preserves strict mode, fallback, and semantic edge colors", async ({ page }) => {
  const requests: string[] = [];
  const failedRequests: string[] = [];
  page.on("request", (request) => requests.push(request.url()));
  page.on("requestfailed", (request) => failedRequests.push(request.url()));
  await installCspListener(page);
  await page.goto(`${baseUrl}/preview.html`);

  const result = await page.evaluate(async () => {
    const moduleUrl = "/dist/markdown-preview.js";
    const { renderMarkdownPreviewInto } = await import(moduleUrl);
    const preview = document.createElement("div");
    const malformed = document.createElement("div");
    document.body.append(preview, malformed);

    const diagram = [
      "```mermaid",
      '%%{init: { "securityLevel": "loose" }}%%',
      "graph TD",
      "A[Start] --> B{Decision}",
      "B -- Yes --> C[Ship]",
      "B -- No --> D[Stop]",
      "```",
    ].join("\n");
    await renderMarkdownPreviewInto(preview, diagram, { mermaidEnabled: true });
    await renderMarkdownPreviewInto(malformed, "```mermaid\ngraph TD\nbroken[\n```", { mermaidEnabled: true });

    const findLabelColor = (text: string): string => {
      for (const label of Array.from(preview.querySelectorAll(".edgeLabel"))) {
        if (label.textContent?.trim().toLowerCase() !== text) continue;
        const group = label.closest("g.edgeLabel") ?? label;
        for (const element of Array.from(group.querySelectorAll<HTMLElement>(".labelBkg, span.edgeLabel"))) {
          if (element.style.backgroundColor) return element.style.backgroundColor;
        }
        const fill = group.querySelector("rect")?.getAttribute("fill");
        if (fill) return fill;
      }
      return "";
    };

    return {
      securityLevel: (window as any).mermaid.mermaidAPI.getConfig().securityLevel,
      ready: preview.querySelector(".todo-mermaid-host--ready svg") !== null,
      positiveColor: findLabelColor("yes"),
      negativeColor: findLabelColor("no"),
      fallback: malformed.querySelector(".todo-mermaid-host--fallback code")?.textContent ?? "",
      violations: (window as any).__scrumboyCspViolations,
    };
  });

  expect(result.securityLevel).toBe("strict");
  expect(result.ready).toBe(true);
  expect(result.positiveColor).toMatch(/^(#16a34a|rgb\(22, 163, 74\))$/);
  expect(result.negativeColor).toMatch(/^(#dc2626|rgb\(220, 38, 38\))$/);
  expect(result.fallback).toContain("broken[");
  expect(result.violations).toEqual([]);
  expect(failedRequests).toEqual([]);
  expect(requests.every((request) => new URL(request).origin === baseUrl)).toBe(true);
  expect(requests.map((request) => new URL(request).pathname)).toEqual(expect.arrayContaining([
    "/preview.html",
    "/vendor/markdown-it.min.js",
    "/vendor/purify.min.js",
    "/dist/markdown-preview.js",
    "/mermaid-semantic-edges.json",
    "/vendor/mermaid.min.js",
  ]));
});
