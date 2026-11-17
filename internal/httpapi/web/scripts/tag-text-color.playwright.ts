import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";
import { build } from "esbuild";

type Rgba = readonly [number, number, number, number];

type PillPaint = {
  readonly color: string;
  readonly backgroundColor: string;
  readonly backgroundImage: string;
  readonly ancestorBackgroundColors: readonly string[];
  readonly styleAttribute: string;
};

async function productionRendererBundle(): Promise<string> {
  const result = await build({
    bundle: true,
    format: "iife",
    platform: "browser",
    target: "es2020",
    write: false,
    stdin: {
      contents: `
        import { renderStoryCardContentHtml } from "./modules/views/story-card-content.ts";
        import { buildOmniRowContent } from "./modules/views/omni-tags.ts";
        globalThis.__tagContrastProduction = { renderStoryCardContentHtml, buildOmniRowContent };
      `,
      loader: "ts",
      resolveDir: process.cwd(),
      sourcefile: "tag-contrast-production-entry.ts",
    },
  });
  if (result.outputFiles.length !== 1) throw new Error("Expected one production renderer bundle");
  return result.outputFiles[0].text;
}

function parseCssColor(value: string): Rgba {
  const match = /rgba?\(([^)]+)\)/.exec(value);
  if (!match) throw new Error(`Unsupported computed color: ${value}`);
  const parts = match[1].replaceAll(",", " ").replace("/", " ").split(/\s+/).filter(Boolean);
  const channel = (part: string): number => part.endsWith("%") ? Number.parseFloat(part) * 2.55 : Number.parseFloat(part);
  const alpha = parts[3] == null ? 1 : parts[3].endsWith("%") ? Number.parseFloat(parts[3]) / 100 : Number.parseFloat(parts[3]);
  return [channel(parts[0]), channel(parts[1]), channel(parts[2]), alpha];
}

function composite(foreground: Rgba, background: Rgba): Rgba {
  const alpha = foreground[3] + background[3] * (1 - foreground[3]);
  if (alpha === 0) return [0, 0, 0, 0];
  return [
    (foreground[0] * foreground[3] + background[0] * background[3] * (1 - foreground[3])) / alpha,
    (foreground[1] * foreground[3] + background[1] * background[3] * (1 - foreground[3])) / alpha,
    (foreground[2] * foreground[3] + background[2] * background[3] * (1 - foreground[3])) / alpha,
    alpha,
  ];
}

function luminance(color: Rgba): number {
  const channels = color.slice(0, 3).map((channel) => {
    const encoded = channel / 255;
    return encoded <= 0.04045 ? encoded / 12.92 : ((encoded + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrast(left: Rgba, right: Rgba): number {
  const l1 = luminance(left);
  const l2 = luminance(right);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

function renderedBackground(paint: PillPaint): Rgba {
  let background = parseCssColor(paint.backgroundColor);
  if (paint.backgroundImage !== "none") background = composite(parseCssColor(paint.backgroundImage), background);
  for (const ancestor of paint.ancestorBackgroundColors) {
    if (background[3] >= 1) break;
    background = composite(background, parseCssColor(ancestor));
  }
  expect(background[3]).toBe(1);
  return background;
}

function expectColor(value: string, expected: readonly [number, number, number, number]): void {
  const actual = parseCssColor(value);
  for (let index = 0; index < expected.length; index++) expect(actual[index]).toBeCloseTo(expected[index], 3);
}

async function readPaint(page: Page, selector: string): Promise<PillPaint> {
  return page.locator(selector).evaluate((element) => {
    const style = getComputedStyle(element);
    const ancestorBackgroundColors: string[] = [];
    for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
      ancestorBackgroundColors.push(getComputedStyle(ancestor).backgroundColor);
    }
    return {
      color: style.color,
      backgroundColor: style.backgroundColor,
      backgroundImage: style.backgroundImage,
      ancestorBackgroundColors,
      styleAttribute: element.getAttribute("style") ?? "",
    };
  });
}

async function expectReadable(page: Page, selector: string): Promise<PillPaint> {
  const paint = await readPaint(page, selector);
  expect(contrast(parseCssColor(paint.color), renderedBackground(paint)), selector).toBeGreaterThanOrEqual(4.5);
  return paint;
}

async function computedBackground(page: Page, selector: string): Promise<string> {
  return page.locator(selector).evaluate((element) => getComputedStyle(element).backgroundColor);
}

async function settle(page: Page): Promise<void> {
  await page.evaluate(() => new Promise<void>((resolveFrame) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolveFrame()));
  }));
}

async function settleBackgroundTransition(page: Page, selector: string): Promise<void> {
  await page.locator(selector).evaluate((element) => {
    const milliseconds = (value: string): number => value.endsWith("ms") ? Number.parseFloat(value) : Number.parseFloat(value) * 1000;
    const style = getComputedStyle(element);
    const durations = style.transitionDuration.split(",").map((value) => milliseconds(value.trim()));
    const delays = style.transitionDelay.split(",").map((value) => milliseconds(value.trim()));
    const total = Math.max(0, ...durations.map((duration, index) => duration + (delays[index] ?? delays[0] ?? 0)));
    return new Promise<void>((resolveTransition) => setTimeout(resolveTransition, total + 20));
  });
}

test("production tinted pills follow surfaces, wallpaper, hover, and live theme changes", async ({ page }) => {
  const color = "#ff0000";

  await page.setContent(`<!doctype html>
    <html data-theme="light" data-wallpaper-active data-wallpaper-source="image">
      <body>
        <div class="wallpaper-shell wallpaper-shell--visible" aria-hidden="true">
          <div class="wallpaper-shell__image" style="background-image:linear-gradient(90deg,#000 0 50%,#fff 50%)"></div>
          <div class="wallpaper-shell__overlay"></div>
        </div>
        <div id="app">
          <div id="idle">Idle</div>
          <main class="container">
            <button id="member-card" class="card" type="button"></button>
            <button id="public-card" class="card card--public" type="button"></button>
            <div id="list" class="list__item">
              <span id="status" class="status-pill">Status</span>
            </div>
            <div class="filters--omni">
              <div id="member-strong"></div>
              <div id="member-subtle"></div>
              <div id="public-strong"></div>
            </div>
          </main>
        </div>
        <div id="toast"></div>
      </body>
    </html>`);
  await page.addStyleTag({ content: readFileSync(resolve(process.cwd(), "styles.css"), "utf8") });
  await page.addScriptTag({ content: await productionRendererBundle() });
  await page.evaluate((tagColor) => {
    const production = (globalThis as any).__tagContrastProduction;
    const story = {
      localId: 7,
      title: "Contrast",
      tags: [{ name: "Tag", color: tagColor }],
      priority: { name: "Priority", color: tagColor },
      points: null,
      due: null,
    };
    document.querySelector("#member-card")!.innerHTML = production.renderStoryCardContentHtml(story, { tagTone: "accent", tooltips: false });
    document.querySelector("#public-card")!.innerHTML = production.renderStoryCardContentHtml(story, { tagTone: "theme", tooltips: false });
    const memberStrong = production.buildOmniRowContent({
      applied: [{ name: "Member", color: tagColor }],
      candidates: [],
      browse: false,
      attrs: { tag: "data-member-tag", clear: "data-member-clear" },
      tone: "accent",
      clearLabel: () => "Clear member tag",
    });
    const memberSubtle = production.buildOmniRowContent({
      applied: [],
      candidates: [{ name: "Browse", color: tagColor }],
      browse: true,
      attrs: { tag: "data-member-tag", clear: "data-member-clear" },
      tone: "accent",
      clearLabel: () => "",
    });
    const publicStrong = production.buildOmniRowContent({
      applied: [{ name: "Public", color: tagColor }],
      candidates: [],
      browse: false,
      attrs: { tag: "data-public-tag", clear: "data-public-clear" },
      tone: "theme",
      clearLabel: () => "Clear public tag",
    });
    document.querySelector("#member-strong")!.innerHTML = memberStrong.pinnedHtml;
    document.querySelector("#member-subtle")!.innerHTML = memberSubtle.candidatesHtml;
    document.querySelector("#public-strong")!.innerHTML = publicStrong.pinnedHtml;
    const priority = document.querySelector<HTMLElement>("#member-card .card__priority")!;
    const status = document.querySelector<HTMLElement>("#status")!;
    status.style.cssText = `border-color:${tagColor};background:${tagColor}20;color:${priority.style.color}`;
  }, color);
  await settle(page);

  await expect(page.locator("#member-card > .card__content > .card__tags .card__tags-list > .tag")).toHaveCount(1);
  await expect(page.locator("#member-card > .card__content > .card__tags .card__badges > .card__priority")).toHaveCount(1);
  await expect(page.locator("#member-strong .omni-tag-pill--applied")).toHaveCount(1);
  await expect(page.locator("#member-subtle .omni-tag-pill--browse")).toHaveCount(1);

  const wallpaper = await page.evaluate(() => ({
    shellDisplay: getComputedStyle(document.querySelector(".wallpaper-shell")!).display,
    image: getComputedStyle(document.querySelector(".wallpaper-shell__image")!).backgroundImage,
    overlay: getComputedStyle(document.querySelector(".wallpaper-shell__overlay")!).backgroundColor,
    container: getComputedStyle(document.querySelector(".container")!).backgroundColor,
  }));
  expect(wallpaper.shellDisplay).toBe("block");
  expect(wallpaper.image).toContain("linear-gradient");
  expectColor(wallpaper.overlay, [247, 247, 244, 0.82]);
  expectColor(wallpaper.container, [0, 0, 0, 0]);

  const themes = ["light", "dark"] as const;
  let lightMemberColor = "";
  for (const theme of themes) {
    await page.evaluate((nextTheme) => { document.documentElement.dataset.theme = nextTheme; }, theme);
    await page.locator("#idle").hover();
    await settle(page);
    await settleBackgroundTransition(page, "#member-card");
    await settleBackgroundTransition(page, "#list");

    const normalCardSurface = await computedBackground(page, "#member-card");
    const normalListSurface = await computedBackground(page, "#list");
    if (theme === "light") {
      expectColor(normalCardSurface, [245, 243, 239, 1]);
      expectColor(normalListSurface, [245, 243, 239, 1]);
    }

    const memberTag = await expectReadable(page, "#member-card .card__tags-list > .tag");
    await expectReadable(page, "#member-card .card__priority");
    const publicTag = await expectReadable(page, "#public-card .card__tags-list > .tag");
    expect(memberTag.color).not.toBe(publicTag.color);
    if (theme === "light") lightMemberColor = memberTag.color;
    else expect(memberTag.color).not.toBe(lightMemberColor);

    await page.locator("#member-card").hover();
    await settleBackgroundTransition(page, "#member-card");
    const hoveredCardSurface = await computedBackground(page, "#member-card");
    if (theme === "light") {
      expect(hoveredCardSurface).not.toBe(normalCardSurface);
      expectColor(hoveredCardSurface, [240, 237, 232, 1]);
    }
    await expectReadable(page, "#member-card .card__tags-list > .tag");
    await expectReadable(page, "#member-card .card__priority");

    await page.locator("#idle").hover();
    await expectReadable(page, "#status");
    await page.locator("#list").hover();
    await settleBackgroundTransition(page, "#list");
    const hoveredListSurface = await computedBackground(page, "#list");
    if (theme === "light") {
      expect(hoveredListSurface).not.toBe(normalListSurface);
      expectColor(hoveredListSurface, [240, 237, 232, 1]);
    }
    await expectReadable(page, "#status");

    const strong = await expectReadable(page, "#member-strong .omni-tag-pill");
    const subtle = await expectReadable(page, "#member-subtle .omni-tag-pill");
    const publicPill = await expectReadable(page, "#public-strong .omni-tag-pill");
    expect(strong.styleAttribute).toContain("background-color:var(--bg)");
    expect(strong.styleAttribute).toContain("#ff000020");
    expect(subtle.styleAttribute).toContain("background-color:var(--bg)");
    expect(subtle.styleAttribute).toContain("#ff00000d");
    expect(parseCssColor(strong.backgroundColor)[3]).toBe(1);
    expect(parseCssColor(subtle.backgroundColor)[3]).toBe(1);
    expect(strong.backgroundImage).not.toBe("none");
    expect(subtle.backgroundImage).not.toBe("none");
    expect(strong.color).not.toBe(publicPill.color);

    const beforeWallpaperChange = renderedBackground(strong);
    await page.locator(".wallpaper-shell__image").evaluate((element) => {
      (element as HTMLElement).style.backgroundImage = "linear-gradient(90deg,#ff00ff,#00ffff)";
    });
    const afterWallpaperChange = renderedBackground(await readPaint(page, "#member-strong .omni-tag-pill"));
    for (let index = 0; index < beforeWallpaperChange.length; index++) {
      expect(afterWallpaperChange[index]).toBeCloseTo(beforeWallpaperChange[index], 6);
    }
  }
});
