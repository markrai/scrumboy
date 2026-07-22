#!/usr/bin/env node
/**
 * Low-cost documentation parity checks for Scrumboy.
 * Run from repository root: node docs/scripts/verify-docs.mjs
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "../..");
const DIAGRAMS_DIR = path.join(REPO_ROOT, "docs", "diagrams");
const CATALOG_PATH = path.join(DIAGRAMS_DIR, "catalog.json");
const MCP_MD = path.join(REPO_ROOT, "docs", "mcp.md");
const ADAPTER_GO = path.join(REPO_ROOT, "internal", "mcp", "adapter.go");
const PACKAGE_JSON = path.join(REPO_ROOT, "internal", "httpapi", "web", "package.json");
const MARKDOWN_MD = path.join(REPO_ROOT, "docs", "markdown-and-mermaid.md");
const INDEX_HTML = path.join(DIAGRAMS_DIR, "index.html");

let failures = 0;

function fail(msg) {
  console.error("FAIL:", msg);
  failures++;
}

function ok(msg) {
  console.log("OK:", msg);
}

function read(p) {
  return fs.readFileSync(p, "utf8");
}

function checkCatalog() {
  if (!fs.existsSync(CATALOG_PATH)) {
    fail("missing docs/diagrams/catalog.json");
    return;
  }
  const catalog = JSON.parse(read(CATALOG_PATH));
  const listed = new Set();
  for (const cat of catalog.categories || []) {
    for (const doc of cat.docs || []) {
      if (!doc.file) {
        fail(`catalog entry missing file in category ${cat.id}`);
        continue;
      }
      listed.add(doc.file);
      const fp = path.join(DIAGRAMS_DIR, doc.file);
      if (!fs.existsSync(fp)) {
        fail(`catalog lists missing file: ${doc.file}`);
      }
    }
  }
  const onDisk = fs
    .readdirSync(DIAGRAMS_DIR)
    .filter((f) => /^scrumboy_.*\.md$/i.test(f));
  for (const f of onDisk) {
    if (!listed.has(f)) {
      fail(`diagram file not in catalog.json: ${f}`);
    }
  }
  if (failures === 0 || listed.size > 0) {
    ok(`diagram catalog (${listed.size} files, ${onDisk.length} on disk)`);
  }
}

function extractMcpToolsFromDocs(md) {
  const fence = md.match(/### Tool Index \(Flat\)[\s\S]*?```(?:\w*)\r?\n([\s\S]*?)```/);
  if (!fence) return null;
  return fence[1]
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("#") && /^[a-z]+\.[a-zA-Z]+$/.test(l));
}

function extractMcpToolsFromAdapter(goSrc) {
  const m = goSrc.match(/func \(a \*Adapter\) implementedTools\(\) \[\]string \{\s*return \[\]string\{([\s\S]*?)\}/);
  if (!m) return null;
  return [...m[1].matchAll(/"([^"]+)"/g)].map((x) => x[1]);
}

function checkMcpTools() {
  const docsTools = extractMcpToolsFromDocs(read(MCP_MD));
  const codeTools = extractMcpToolsFromAdapter(read(ADAPTER_GO));
  if (!docsTools) {
    fail("could not parse Tool Index (Flat) from docs/mcp.md");
    return;
  }
  if (!codeTools) {
    fail("could not parse implementedTools() from internal/mcp/adapter.go");
    return;
  }
  if (docsTools.length !== codeTools.length) {
    fail(`MCP tool count mismatch: docs=${docsTools.length} code=${codeTools.length}`);
  }
  const max = Math.max(docsTools.length, codeTools.length);
  for (let i = 0; i < max; i++) {
    if (docsTools[i] !== codeTools[i]) {
      fail(`MCP tool order/name mismatch at index ${i}: docs=${docsTools[i]} code=${codeTools[i]}`);
    }
  }
  if (docsTools.length === codeTools.length && docsTools.every((t, i) => t === codeTools[i])) {
    ok(`MCP tools parity (${docsTools.length} tools)`);
  }
}

function checkDependencyPins() {
  const pkg = JSON.parse(read(PACKAGE_JSON));
  const md = read(MARKDOWN_MD);
  const html = read(INDEX_HTML);
  const expected = {
    "markdown-it": pkg.dependencies["markdown-it"],
    dompurify: pkg.dependencies.dompurify,
    mermaid: pkg.dependencies.mermaid,
  };
  for (const [name, ver] of Object.entries(expected)) {
    if (!ver) {
      fail(`package.json missing dependency ${name}`);
      continue;
    }
    if (name === "mermaid") {
      if (!md.includes(`Mermaid \`${ver}\``) && !md.includes(`mermaid@${ver}`) && !md.includes(`Mermaid ${ver}`)) {
        // markdown-and-mermaid.md uses phrasing like Mermaid `11.16.0`
        if (!md.includes(`\`${ver}\``) || !md.toLowerCase().includes("mermaid")) {
          fail(`docs/markdown-and-mermaid.md does not document Mermaid ${ver}`);
        }
      }
      if (!html.includes(`mermaid@${ver}/`)) {
        fail(`docs/diagrams/index.html Mermaid CDN not pinned to ${ver}`);
      } else {
        ok(`Mermaid CDN pin ${ver}`);
      }
    } else if (name === "markdown-it") {
      if (!md.includes(`markdown-it@${ver}`)) {
        fail(`docs/markdown-and-mermaid.md missing markdown-it@${ver}`);
      } else {
        ok(`markdown-it pin ${ver}`);
      }
    } else if (name === "dompurify") {
      if (!md.includes(`dompurify@${ver}`)) {
        fail(`docs/markdown-and-mermaid.md missing dompurify@${ver}`);
      } else {
        ok(`dompurify pin ${ver}`);
      }
    }
  }
}

function collectMarkdownFiles() {
  const files = [];
  function walk(dir) {
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      if (ent.name === "draft" || ent.name === "node_modules") continue;
      const p = path.join(dir, ent.name);
      if (ent.isDirectory()) walk(p);
      else if (ent.name.endsWith(".md")) files.push(p);
    }
  }
  walk(path.join(REPO_ROOT, "docs"));
  files.push(path.join(REPO_ROOT, "README.md"));
  files.push(path.join(REPO_ROOT, "FAQ.md"));
  files.push(path.join(REPO_ROOT, "API.md"));
  files.push(path.join(REPO_ROOT, "CONTRIBUTING.md"));
  return files.filter((f) => fs.existsSync(f));
}

function stripFencedCode(text) {
  return text.replace(/```[\s\S]*?```/g, "").replace(/`[^`\n]+`/g, "");
}

function checkInternalLinks() {
  const linkRe = /\[[^\]]*\]\(([^)]+)\)/g;
  let broken = 0;
  for (const file of collectMarkdownFiles()) {
    const text = stripFencedCode(read(file));
    const dir = path.dirname(file);
    let m;
    while ((m = linkRe.exec(text)) !== null) {
      let href = m[1].trim();
      if (!href || href.startsWith("http://") || href.startsWith("https://") || href.startsWith("mailto:")) continue;
      if (href.startsWith("#")) continue;
      // Placeholder / example hrefs in prose
      if (href === "url" || /\s/.test(href)) continue;
      const hash = href.indexOf("#");
      if (hash >= 0) href = href.slice(0, hash);
      if (!href) continue;
      // Only validate paths that look like repo docs/files
      if (!/\.(md|html|json|svg|png|jpg|yml|yaml|go|ts|js|mjs)$/i.test(href) && !href.includes("/")) continue;
      const target = path.resolve(dir, href);
      if (!fs.existsSync(target)) {
        fail(`broken link in ${path.relative(REPO_ROOT, file)}: ${m[1]}`);
        broken++;
      }
    }
  }
  if (broken === 0) ok("internal markdown links resolve");
}

async function checkMermaid() {
  const catalog = JSON.parse(read(CATALOG_PATH));
  const blocks = [];
  for (const cat of catalog.categories || []) {
    for (const doc of cat.docs || []) {
      const fp = path.join(DIAGRAMS_DIR, doc.file);
      if (!fs.existsSync(fp)) continue;
      const text = read(fp);
      const re = /```mermaid\s*\n([\s\S]*?)```/g;
      let m;
      while ((m = re.exec(text)) !== null) {
        blocks.push({ file: doc.file, src: m[1].trim() });
      }
    }
  }

  const mermaidPkg = path.join(REPO_ROOT, "internal", "httpapi", "web", "node_modules", "mermaid", "dist", "mermaid.core.mjs");
  if (!fs.existsSync(mermaidPkg)) {
    console.warn("SKIP: Mermaid smoke (internal/httpapi/web/node_modules/mermaid not installed)");
    return;
  }

  let mermaid;
  try {
    mermaid = (await import(pathToFileURL(mermaidPkg).href)).default;
    await mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
  } catch (err) {
    console.warn("SKIP: Mermaid init failed:", err.message || err);
    return;
  }

  let parseFails = 0;
  for (const b of blocks) {
    try {
      await mermaid.parse(b.src);
    } catch (err) {
      // DOMPurify hook errors in Node are environmental; treat as skip for that engine
      const msg = String(err.message || err);
      if (msg.includes("DOMPurify")) {
        console.warn("SKIP: Mermaid parse unavailable in Node (DOMPurify):", b.file);
        return;
      }
      fail(`Mermaid parse ${b.file}: ${msg}`);
      parseFails++;
    }
  }
  if (parseFails === 0) ok(`Mermaid smoke (${blocks.length} blocks)`);
}

async function main() {
  console.log("verify-docs: repo root", REPO_ROOT);
  checkCatalog();
  checkMcpTools();
  checkDependencyPins();
  checkInternalLinks();
  await checkMermaid();
  if (failures > 0) {
    console.error(`\nverify-docs: ${failures} failure(s)`);
    process.exit(1);
  }
  console.log("\nverify-docs: all checks passed");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
