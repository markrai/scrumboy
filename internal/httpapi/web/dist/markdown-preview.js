import { buildMermaidSemanticLabelColors, loadMermaidSemanticEdgeConfig, paintMermaidSemanticLabelBackgrounds, } from "./mermaid-semantic-edges.js";
import { hasI18nKey, t } from "./i18n/index.js";
import { getAppRuntime } from "./platform/runtime.js";
import { CANONICAL_ASSET_PATH_RE, MAX_NOTE_IMAGES, parseImageAlt } from "./note-images.js";
import { escapeHTML } from "./utils.js";
const SAFE_TAGS = [
    "a",
    "blockquote",
    "br",
    "code",
    "em",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "hr",
    "li",
    "ol",
    "p",
    "pre",
    "strong",
    "ul",
];
const SAFE_ATTRS = ["href", "rel", "target"];
const MERMAID_PLACEHOLDER_PREFIX = "__SCRUMBOY_MERMAID_BLOCK__";
const MERMAID_SCRIPT_SRC = "/vendor/mermaid.min.js";
const MERMAID_MAX_TEXT_SIZE = 50000;
const MERMAID_MAX_EDGES = 500;
const MERMAID_MAX_BLOCKS_PER_PREVIEW = 4;
const MERMAID_MAX_CHARS_PER_BLOCK = 4000;
const MERMAID_MAX_TOTAL_CHARS_PER_PREVIEW = 8000;
const MERMAID_DIRECTIVE_BLOCK_RE = /%%\{[\s\S]*?\}%%/g;
const IMAGE_PLACEHOLDER_PREFIX = "__SCRUMBOY_IMAGE__";
// The only image destination that renders: a Scrumboy-managed asset (see note-images.ts).
const CANONICAL_ASSET_SRC_RE = CANONICAL_ASSET_PATH_RE;
const MAX_IMAGES_PER_PREVIEW = MAX_NOTE_IMAGES;
const IMAGE_ACQUIRE_CONCURRENCY = 4;
function previewText(key, fallback, values = {}) {
    return hasI18nKey(key) ? t(key, values) : fallback;
}
let markdownRenderer = null;
let mermaidConfiguredThemeKey = null;
let mermaidLoadPromise = null;
const renderEpochByContainer = new WeakMap();
const previewResourcesByContainer = new WeakMap();
function getMarkdownFactory() {
    if (typeof window === "undefined" || typeof window.markdownit !== "function") {
        throw new Error("Markdown preview is unavailable: missing /vendor/markdown-it.min.js");
    }
    return window.markdownit;
}
function getDOMPurify() {
    if (typeof window === "undefined" || !window.DOMPurify || typeof window.DOMPurify.sanitize !== "function") {
        throw new Error("Markdown preview is unavailable: missing /vendor/purify.min.js");
    }
    return window.DOMPurify;
}
function getMermaidGlobal() {
    if (typeof window === "undefined" || !window.mermaid) {
        throw new Error("Markdown preview is unavailable: missing /vendor/mermaid.min.js");
    }
    return window.mermaid;
}
function renderImageTokenAsText(token) {
    const alt = token?.content ?? "";
    const src = token?.attrGet?.("src") ?? "";
    const title = token?.attrGet?.("title") ?? "";
    const titlePart = title ? ` "${title}"` : "";
    return escapeHTML(`![${alt}](${src}${titlePart})`);
}
function getMarkdownRenderer() {
    if (markdownRenderer) {
        return markdownRenderer;
    }
    const factory = getMarkdownFactory();
    const renderer = factory("default", {
        html: false,
        breaks: true,
        linkify: false,
    });
    renderer.renderer.rules.image = (tokens, idx) => renderImageTokenAsText(tokens[idx]);
    markdownRenderer = renderer;
    return renderer;
}
function getFenceLanguage(info) {
    return (info || "").trim().split(/\s+/, 1)[0]?.toLowerCase() || "";
}
function renderFenceToken(token) {
    const language = getFenceLanguage(token?.info ?? "");
    const classAttr = language ? ` class="language-${escapeHTML(language)}"` : "";
    return `<pre><code${classAttr}>${escapeHTML(token?.content ?? "")}</code></pre>\n`;
}
function createMermaidPlaceholderNonce() {
    if (typeof crypto !== "undefined") {
        if (typeof crypto.randomUUID === "function") {
            return crypto.randomUUID().replace(/-/g, "");
        }
        if (typeof crypto.getRandomValues === "function") {
            const bytes = crypto.getRandomValues(new Uint8Array(16));
            return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
        }
    }
    return `${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`;
}
function buildMermaidPlaceholder(nonce, index) {
    return `${MERMAID_PLACEHOLDER_PREFIX}${nonce}_${index}__`;
}
function stripUnsupportedMermaidDirectives(source) {
    return source.replace(MERMAID_DIRECTIVE_BLOCK_RE, "").trim();
}
function getMermaidWarningMessage(blockIndex, displaySource, renderSource, totalCharsBeforeBlock) {
    if (renderSource.trim() === "") {
        return previewText("todo.markdownPreview.directivesOnly", "This Mermaid block contains only ignored Mermaid directives. Showing source instead.");
    }
    if (blockIndex >= MERMAID_MAX_BLOCKS_PER_PREVIEW) {
        return previewText("todo.markdownPreview.tooManyDiagrams", `Only the first ${MERMAID_MAX_BLOCKS_PER_PREVIEW} Mermaid diagrams are rendered in note preview. Showing source instead.`, { count: MERMAID_MAX_BLOCKS_PER_PREVIEW });
    }
    if (displaySource.length > MERMAID_MAX_CHARS_PER_BLOCK) {
        return previewText("todo.markdownPreview.diagramTooLarge", `This Mermaid diagram exceeds the ${MERMAID_MAX_CHARS_PER_BLOCK}-character preview limit. Showing source instead.`, { count: MERMAID_MAX_CHARS_PER_BLOCK });
    }
    if (totalCharsBeforeBlock + displaySource.length > MERMAID_MAX_TOTAL_CHARS_PER_PREVIEW) {
        return previewText("todo.markdownPreview.budgetExceeded", `This note exceeds the ${MERMAID_MAX_TOTAL_CHARS_PER_PREVIEW}-character Mermaid preview budget. Showing source instead.`, { count: MERMAID_MAX_TOTAL_CHARS_PER_PREVIEW });
    }
    return undefined;
}
function renderMarkdownHtml(markdown, mermaidEnabled, inlineImagesEnabled = false) {
    const renderer = getMarkdownRenderer();
    const images = [];
    if (!mermaidEnabled && !inlineImagesEnabled) {
        return { html: renderer.render(markdown || ""), mermaidBlocks: [], images };
    }
    const mermaidBlocks = [];
    const placeholderNonce = createMermaidPlaceholderNonce();
    let totalMermaidChars = 0;
    const originalFence = renderer.renderer.rules.fence;
    const originalImage = renderer.renderer.rules.image;
    if (inlineImagesEnabled) {
        // Canonical asset images become nonce placeholders inside <code>; real <img> elements are
        // created by code after sanitization (materializeImagePlaceholders). Everything else keeps
        // the escaped-text rendering.
        renderer.renderer.rules.image = (tokens, idx) => {
            const token = tokens[idx];
            const src = token?.attrGet?.("src") ?? "";
            if (!CANONICAL_ASSET_SRC_RE.test(src) || images.length >= MAX_IMAGES_PER_PREVIEW) {
                return renderImageTokenAsText(token);
            }
            const placeholder = `${IMAGE_PLACEHOLDER_PREFIX}${placeholderNonce}_${images.length}__`;
            const { alt, width } = parseImageAlt(String(token?.content ?? ""));
            images.push({
                placeholder,
                path: src,
                alt,
                title: String(token?.attrGet?.("title") ?? ""),
                width,
            });
            return `<code>${escapeHTML(placeholder)}</code>`;
        };
    }
    if (!mermaidEnabled) {
        try {
            return { html: renderer.render(markdown || ""), mermaidBlocks, images };
        }
        finally {
            renderer.renderer.rules.image = originalImage;
        }
    }
    renderer.renderer.rules.fence = (tokens, idx) => {
        const token = tokens[idx];
        if (getFenceLanguage(token?.info ?? "") !== "mermaid") {
            return renderFenceToken(token);
        }
        const displaySource = token?.content ?? "";
        const renderSource = stripUnsupportedMermaidDirectives(displaySource);
        const placeholder = buildMermaidPlaceholder(placeholderNonce, mermaidBlocks.length);
        const warningMessage = getMermaidWarningMessage(mermaidBlocks.length, displaySource, renderSource, totalMermaidChars);
        if (!warningMessage) {
            totalMermaidChars += displaySource.length;
        }
        mermaidBlocks.push({ placeholder, displaySource, renderSource, warningMessage });
        return `<pre><code>${escapeHTML(placeholder)}</code></pre>\n`;
    };
    try {
        return {
            html: renderer.render(markdown || ""),
            mermaidBlocks,
            images,
        };
    }
    finally {
        renderer.renderer.rules.fence = originalFence;
        renderer.renderer.rules.image = originalImage;
    }
}
function isSafeLinkHref(href) {
    const value = href.trim();
    if (!value) {
        return false;
    }
    if (value.startsWith("#") ||
        value.startsWith("?") ||
        (value.startsWith("/") && !value.startsWith("//")) ||
        value.startsWith("./") ||
        value.startsWith("../")) {
        return true;
    }
    if (value.startsWith("//")) {
        return false;
    }
    const normalized = value.replace(/[\u0000-\u001f\u007f\s]+/g, "");
    const schemeMatch = normalized.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/);
    if (!schemeMatch) {
        return true;
    }
    const scheme = schemeMatch[1].toLowerCase();
    return scheme === "http" || scheme === "https";
}
function isExternalHref(href) {
    const value = href.trim().toLowerCase();
    return value.startsWith("http://") || value.startsWith("https://");
}
function sanitizeMarkdownHtml(markdownHtml) {
    const sanitized = getDOMPurify().sanitize(markdownHtml, {
        ALLOWED_TAGS: SAFE_TAGS,
        ALLOWED_ATTR: SAFE_ATTRS,
        ALLOW_ARIA_ATTR: false,
        ALLOW_DATA_ATTR: false,
    });
    const template = document.createElement("template");
    template.innerHTML = sanitized;
    for (const element of Array.from(template.content.querySelectorAll("img, iframe, object, embed, script, svg"))) {
        element.remove();
    }
    for (const link of Array.from(template.content.querySelectorAll("a"))) {
        const href = link.getAttribute("href") || "";
        if (!isSafeLinkHref(href)) {
            link.replaceWith(document.createTextNode(link.textContent || ""));
            continue;
        }
        if (isExternalHref(href)) {
            link.setAttribute("target", "_blank");
            link.setAttribute("rel", "noopener noreferrer");
        }
        else {
            link.removeAttribute("target");
            link.removeAttribute("rel");
        }
    }
    return template.innerHTML;
}
function beginRenderEpoch(container) {
    const nextEpoch = (renderEpochByContainer.get(container) || 0) + 1;
    renderEpochByContainer.set(container, nextEpoch);
    return nextEpoch;
}
function isActiveRender(container, epoch) {
    return renderEpochByContainer.get(container) === epoch;
}
function replaceMermaidPlaceholders(container, mermaidBlocks) {
    const blockByPlaceholder = new Map();
    for (const block of mermaidBlocks) {
        blockByPlaceholder.set(block.placeholder, block);
    }
    const hosts = [];
    for (const code of Array.from(container.querySelectorAll("pre > code"))) {
        const placeholder = code.textContent || "";
        const block = blockByPlaceholder.get(placeholder);
        if (!block) {
            continue;
        }
        const pre = code.parentElement;
        if (!pre) {
            continue;
        }
        const host = document.createElement("div");
        host.className = "todo-mermaid-host todo-mermaid-host--loading";
        const status = document.createElement("div");
        status.className = "todo-mermaid-status";
        status.textContent = previewText("todo.markdownPreview.rendering", "Rendering diagram...");
        const canvas = document.createElement("div");
        canvas.className = "mermaid todo-mermaid-canvas";
        canvas.textContent = block.displaySource;
        host.append(status, canvas);
        pre.replaceWith(host);
        hosts.push({ block, canvas, host });
    }
    return hosts;
}
function setMermaidFallbackState(host, displaySource, statusText, statusTone) {
    host.className = "todo-mermaid-host todo-mermaid-host--fallback";
    const status = document.createElement("div");
    status.className = `todo-mermaid-status todo-mermaid-status--${statusTone}`;
    status.textContent = statusText;
    const pre = document.createElement("pre");
    const code = document.createElement("code");
    code.textContent = displaySource;
    pre.append(code);
    host.replaceChildren(status, pre);
}
function setMermaidErrorState(host, displaySource) {
    setMermaidFallbackState(host, displaySource, previewText("todo.markdownPreview.renderFailed", "Could not render Mermaid diagram. Showing source instead."), "error");
}
function setMermaidSuccessState(host) {
    host.classList.remove("todo-mermaid-host--loading", "todo-mermaid-host--fallback");
    host.classList.add("todo-mermaid-host--ready");
    const status = host.querySelector(".todo-mermaid-status");
    status?.remove();
}
function getMermaidPreviewThemeKey() {
    if (typeof document === "undefined") {
        return "dark";
    }
    return document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
}
function readPreviewCssColor(variableName, fallback) {
    if (typeof document === "undefined") {
        return fallback;
    }
    const value = getComputedStyle(document.documentElement).getPropertyValue(variableName).trim();
    return value || fallback;
}
function buildMermaidPreviewThemeConfig(themeKey) {
    const isLight = themeKey === "light";
    const panel2 = readPreviewCssColor("--panel2", isLight ? "#e5dfd5" : "#0f172a");
    const text = readPreviewCssColor("--text", isLight ? "#111827" : "#e5e7eb");
    const accent = readPreviewCssColor("--accent", isLight ? "#3b82f6" : "#60a5fa");
    const border = readPreviewCssColor("--border", isLight ? "rgba(0, 0, 0, 0.12)" : "rgba(255, 255, 255, 0.08)");
    return {
        theme: "base",
        themeVariables: {
            darkMode: !isLight,
            background: panel2,
            mainBkg: panel2,
            secondBkg: panel2,
            tertiaryBkg: panel2,
            primaryTextColor: text,
            textColor: text,
            lineColor: text,
            primaryColor: accent,
            primaryBorderColor: border,
            edgeLabelBackground: "transparent",
        },
    };
}
function configureMermaidPreview(mermaid, themeKey) {
    if (mermaidConfiguredThemeKey === themeKey) {
        return;
    }
    const themeConfig = buildMermaidPreviewThemeConfig(themeKey);
    mermaid.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        maxTextSize: MERMAID_MAX_TEXT_SIZE,
        maxEdges: MERMAID_MAX_EDGES,
        suppressErrorRendering: true,
        ...themeConfig,
    });
    mermaidConfiguredThemeKey = themeKey;
}
function loadMermaid() {
    if (typeof window === "undefined") {
        return Promise.reject(new Error("Markdown preview is unavailable outside the browser"));
    }
    if (window.mermaid) {
        return Promise.resolve(getMermaidGlobal());
    }
    if (mermaidLoadPromise) {
        return mermaidLoadPromise;
    }
    mermaidLoadPromise = new Promise((resolve, reject) => {
        const existingScript = document.querySelector(`script[src="${MERMAID_SCRIPT_SRC}"]`);
        const handleLoad = () => {
            try {
                resolve(getMermaidGlobal());
            }
            catch (err) {
                mermaidLoadPromise = null;
                reject(err instanceof Error ? err : new Error(String(err)));
            }
        };
        const handleError = () => {
            mermaidLoadPromise = null;
            reject(new Error("Markdown preview is unavailable: missing /vendor/mermaid.min.js"));
        };
        if (existingScript) {
            existingScript.addEventListener("load", handleLoad, { once: true });
            existingScript.addEventListener("error", handleError, { once: true });
            return;
        }
        const script = document.createElement("script");
        script.src = MERMAID_SCRIPT_SRC;
        script.async = true;
        script.addEventListener("load", handleLoad, { once: true });
        script.addEventListener("error", handleError, { once: true });
        document.head.appendChild(script);
    });
    return mermaidLoadPromise;
}
/** Block tokens for note text, parsed exactly as the preview parses it. */
export function parseNoteMarkdownTokens(markdown) {
    return getMarkdownRenderer().parse(markdown, {});
}
export function renderMarkdownToSafeHtml(markdown) {
    const { html } = renderMarkdownHtml(markdown, false);
    return sanitizeMarkdownHtml(html);
}
function safeRelease(resource) {
    try {
        resource.release();
    }
    catch {
        // release() must be idempotent and best-effort; never let it break rendering.
    }
}
// releaseOwnedResources aborts pending acquisitions and releases every resource the container
// owns. It does not touch the DOM.
function releaseOwnedResources(container) {
    const owned = previewResourcesByContainer.get(container);
    if (!owned) {
        return;
    }
    previewResourcesByContainer.delete(container);
    owned.controller.abort();
    for (const resource of owned.owned) {
        safeRelease(resource);
    }
}
/**
 * Releases every server resource (for example native image files) acquired for a preview
 * container and cancels in-flight work for it. Call when the preview is hidden, replaced or its
 * dialog closes. Idempotent; safe on containers that never rendered.
 */
export function releaseMarkdownPreviewResources(container) {
    beginRenderEpoch(container);
    releaseOwnedResources(container);
    for (const img of Array.from(container.querySelectorAll("img.todo-notes-image__img"))) {
        img.removeAttribute("src");
    }
}
function imageUnavailableText(alt) {
    const label = previewText("todo.markdownPreview.imageUnavailable", "Image unavailable");
    return alt ? `${label}: ${alt}` : label;
}
function setImageUnavailable(wrapper, alt) {
    wrapper.className = "todo-notes-image todo-notes-image--unavailable";
    wrapper.textContent = imageUnavailableText(alt);
}
// materializeImagePlaceholders replaces placeholder <code> elements (never inside <pre>) with
// code-built image elements. A user cannot forge a placeholder: the nonce is random per render.
function materializeImagePlaceholders(container, images) {
    const byPlaceholder = new Map();
    for (const image of images) {
        byPlaceholder.set(image.placeholder, image);
    }
    const byPath = new Map();
    for (const code of Array.from(container.querySelectorAll("code"))) {
        if (code.parentElement?.tagName === "PRE") {
            continue;
        }
        const image = byPlaceholder.get(code.textContent || "");
        if (!image) {
            continue;
        }
        byPlaceholder.delete(image.placeholder);
        const wrapper = document.createElement("span");
        wrapper.className = "todo-notes-image todo-notes-image--loading";
        const img = document.createElement("img");
        img.className = "todo-notes-image__img";
        img.alt = image.alt;
        if (image.width != null) {
            // Stored widths are bounded by parseImageAlt; CSS still caps them at the container width.
            wrapper.classList.add("todo-notes-image--sized");
            img.style.width = `${image.width}px`;
        }
        if (image.title) {
            img.title = image.title;
        }
        img.decoding = "async";
        img.loading = "lazy";
        img.referrerPolicy = "no-referrer";
        img.draggable = false;
        wrapper.append(img);
        code.replaceWith(wrapper);
        const list = byPath.get(image.path) ?? [];
        list.push(img);
        byPath.set(image.path, list);
    }
    return byPath;
}
// loadPreviewImages acquires each distinct image path once (bounded concurrency) through the
// platform transport, so browsers and Capacitor share one code path. Stale results are released.
async function loadPreviewImages(container, renderEpoch, imagesByPath) {
    if (imagesByPath.size === 0) {
        return;
    }
    const state = { controller: new AbortController(), owned: [] };
    previewResourcesByContainer.set(container, state);
    const isCurrent = () => isActiveRender(container, renderEpoch) && previewResourcesByContainer.get(container) === state;
    const queue = Array.from(imagesByPath.entries());
    const transport = getAppRuntime().transport();
    const worker = async () => {
        for (let next = queue.shift(); next; next = queue.shift()) {
            const [path, imgs] = next;
            let resource;
            try {
                resource = await transport.acquireResource(path, { signal: state.controller.signal });
            }
            catch {
                if (isCurrent() && !state.controller.signal.aborted) {
                    for (const img of imgs) {
                        const wrapper = img.parentElement;
                        if (wrapper) {
                            setImageUnavailable(wrapper, img.alt);
                        }
                    }
                }
                continue;
            }
            if (!isCurrent() || !imgs.some((img) => container.contains(img))) {
                safeRelease(resource);
                continue;
            }
            state.owned.push(resource);
            for (const img of imgs) {
                const wrapper = img.parentElement;
                img.addEventListener("error", () => {
                    if (wrapper && container.contains(img)) {
                        setImageUnavailable(wrapper, img.alt);
                    }
                }, { once: true });
                img.addEventListener("load", () => {
                    wrapper?.classList.remove("todo-notes-image--loading");
                }, { once: true });
                img.src = resource.url;
            }
        }
    };
    const workers = [];
    const poolSize = Math.min(IMAGE_ACQUIRE_CONCURRENCY, queue.length);
    for (let i = 0; i < poolSize; i += 1) {
        workers.push(worker());
    }
    await Promise.all(workers);
}
export async function renderMarkdownPreviewInto(container, markdown, options = {}) {
    releaseOwnedResources(container);
    const renderEpoch = beginRenderEpoch(container);
    const isEmpty = markdown.trim() === "";
    container.classList.toggle("todo-markdown-preview--empty", isEmpty);
    if (isEmpty) {
        container.textContent = "";
        return;
    }
    const mermaidEnabled = !!options.mermaidEnabled;
    const inlineImagesEnabled = !!options.inlineImagesEnabled;
    const { html, mermaidBlocks, images } = renderMarkdownHtml(markdown, mermaidEnabled, inlineImagesEnabled);
    if (!isActiveRender(container, renderEpoch)) {
        return;
    }
    container.innerHTML = sanitizeMarkdownHtml(html);
    const imageLoad = images.length > 0
        ? loadPreviewImages(container, renderEpoch, materializeImagePlaceholders(container, images))
        : Promise.resolve();
    try {
        await renderMermaidHosts(container, renderEpoch, mermaidEnabled, mermaidBlocks);
    }
    finally {
        await imageLoad;
    }
}
async function renderMermaidHosts(container, renderEpoch, mermaidEnabled, mermaidBlocks) {
    if (!mermaidEnabled || mermaidBlocks.length === 0) {
        return;
    }
    const hosts = replaceMermaidPlaceholders(container, mermaidBlocks);
    if (hosts.length === 0) {
        return;
    }
    const renderableHosts = hosts.filter(({ block, host }) => {
        if (!block.warningMessage) {
            return true;
        }
        setMermaidFallbackState(host, block.displaySource, block.warningMessage, "warning");
        return false;
    });
    if (renderableHosts.length === 0) {
        return;
    }
    const themeKey = getMermaidPreviewThemeKey();
    const semanticEdgeConfig = await loadMermaidSemanticEdgeConfig();
    const mermaid = await loadMermaid();
    if (!isActiveRender(container, renderEpoch)) {
        return;
    }
    configureMermaidPreview(mermaid, themeKey);
    for (const { block, canvas, host } of renderableHosts) {
        if (!isActiveRender(container, renderEpoch) || !host.isConnected) {
            return;
        }
        try {
            const labelColors = buildMermaidSemanticLabelColors(block.renderSource, semanticEdgeConfig);
            canvas.textContent = block.renderSource;
            await mermaid.run({ nodes: [canvas] });
            if (!isActiveRender(container, renderEpoch) || !host.isConnected) {
                return;
            }
            paintMermaidSemanticLabelBackgrounds(canvas, labelColors);
            setMermaidSuccessState(host);
        }
        catch {
            if (!isActiveRender(container, renderEpoch) || !host.isConnected) {
                return;
            }
            setMermaidErrorState(host, block.displaySource);
        }
    }
}
